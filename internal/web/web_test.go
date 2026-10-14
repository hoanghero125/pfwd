package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/hoanghero125/pfwd/internal/ipc"
)

// fakeBackend records calls and answers them from results.
type fakeBackend struct {
	mu      sync.Mutex
	calls   []string // "method params"
	results map[string]any
	errs    map[string]error
}

func (b *fakeBackend) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if method != "stats" {
		b.calls = append(b.calls, strings.TrimSpace(method+" "+string(params)))
	}
	if err := b.errs[method]; err != nil {
		return nil, err
	}
	if r, ok := b.results[method]; ok {
		return r, nil
	}
	return map[string]bool{"ok": true}, nil
}

func (b *fakeBackend) Watch(ctx context.Context, send func(ipc.Event) error) error {
	if err := send(ipc.Event{Event: "session_changed", Data: json.RawMessage(`{"host":"devbox","state":"connected"}`)}); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (b *fakeBackend) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.calls) == 0 {
		return ""
	}
	return b.calls[len(b.calls)-1]
}

// testServer runs a server until the test ends.
type testServer struct {
	*Server
	b    *fakeBackend
	now  time.Time
	base string
}

func start(t *testing.T) *testServer {
	t.Helper()
	b := &fakeBackend{results: map[string]any{}, errs: map[string]error{}}
	ts := &testServer{b: b, now: time.Unix(1_700_000_000, 0)}
	assets := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>pfwd</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	ts.Server = New(b, Options{Assets: assets, Version: "v0.6.0", Now: func() time.Time { return ts.now }, StatsEvery: 50 * time.Millisecond})
	ln, err := ts.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ts.base = ts.Addr()
	done := make(chan error, 1)
	go func() { done <- ts.Serve(t.Context(), ln) }()
	t.Cleanup(func() {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve didn't return after its context ended")
		}
	})
	return ts
}

// browser is a client with a cookie jar that doesn't follow redirects.
func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// reply is what do keeps of a response.
type reply struct {
	StatusCode int
	Header     http.Header
}

func do(t *testing.T, c *http.Client, method, url, body string, header map[string]string) (reply, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header}, string(data)
}

// loggedIn logs a browser in and returns it with the session's token.
func (ts *testServer) loggedIn(t *testing.T) (*http.Client, string) {
	t.Helper()
	c := browser(t)
	u, err := ts.LoginURL()
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := do(t, c, "GET", u, "", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	resp, body := do(t, c, "GET", ts.base+"/api/session", "", nil)
	var info struct{ Token, Version string }
	if err := json.Unmarshal([]byte(body), &info); err != nil || resp.StatusCode != 200 || info.Token == "" || info.Version != "v0.6.0" {
		t.Fatalf("session: %d %s", resp.StatusCode, body)
	}
	return c, info.Token
}

func TestLoginFlow(t *testing.T) {
	ts := start(t)
	u, err := ts.LoginURL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "http://127.0.0.1:") || !strings.Contains(u, "/login?token=") {
		t.Fatalf("login URL %q", u)
	}
	c := browser(t)
	resp, _ := do(t, c, "GET", u, "", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("login: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	cookie := resp.Header.Get("Set-Cookie")
	for _, want := range []string{cookieName + "=", "HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("cookie %q lacks %s", cookie, want)
		}
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("login Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
	if resp, body := do(t, browser(t), "GET", u, "", nil); resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "pfwd ui") {
		t.Errorf("the token worked twice: %d %q", resp.StatusCode, body)
	}
	u2, _ := ts.LoginURL()
	ts.now = ts.now.Add(loginTTL + time.Second)
	if resp, _ := do(t, browser(t), "GET", u2, "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("an expired token worked: %d", resp.StatusCode)
	}
	if resp, _ := do(t, browser(t), "GET", ts.base+"/login?token=nope", "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a made-up token worked: %d", resp.StatusCode)
	}
}

func TestAPINeedsSessionAndToken(t *testing.T) {
	ts := start(t)
	if resp, _ := do(t, browser(t), "GET", ts.base+"/api/hosts", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no cookie: %d", resp.StatusCode)
	}
	c, token := ts.loggedIn(t)
	if resp, _ := do(t, c, "GET", ts.base+"/api/hosts", "", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cookie without the token: %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "GET", ts.base+"/api/hosts", "", map[string]string{tokenHeader: token + "x"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a wrong token: %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "GET", ts.base+"/api/hosts", "", map[string]string{tokenHeader: token}); resp.StatusCode != http.StatusOK {
		t.Errorf("cookie and token: %d", resp.StatusCode)
	}
	// The token is handed out once: a second tab with the cookie gets none.
	resp, body := do(t, c, "GET", ts.base+"/api/session", "", nil)
	if resp.StatusCode != http.StatusForbidden || strings.Contains(body, token) {
		t.Errorf("second /api/session: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, c, "GET", ts.base+"/api/session", "", map[string]string{tokenHeader: token})
	if resp.StatusCode != http.StatusOK || strings.Contains(body, token) {
		t.Errorf("/api/session with the token: %d %s", resp.StatusCode, body)
	}
}

func TestRequestsFromElsewhereRefused(t *testing.T) {
	ts := start(t)
	c, token := ts.loggedIn(t)
	auth := func(extra map[string]string) map[string]string {
		h := map[string]string{tokenHeader: token}
		for k, v := range extra {
			h[k] = v
		}
		return h
	}
	port := strings.TrimPrefix(ts.base, "http://127.0.0.1:")
	for _, tc := range []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"rebound Host", map[string]string{"Host": "evil.example:" + port}, http.StatusMisdirectedRequest},
		{"other port", map[string]string{"Host": "127.0.0.1:1"}, http.StatusMisdirectedRequest},
		{"other Origin", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"other localhost port", map[string]string{"Origin": "http://127.0.0.1:1"}, http.StatusForbidden},
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"own Origin", map[string]string{"Origin": ts.base, "Sec-Fetch-Site": "same-origin"}, http.StatusOK},
	} {
		if resp, _ := do(t, c, "GET", ts.base+"/api/hosts", "", auth(tc.header)); resp.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
	if resp, _ := do(t, browser(t), "GET", ts.base+"/", "", map[string]string{"Host": "localhost:" + port}); resp.StatusCode != http.StatusOK {
		t.Errorf("the app as localhost: %d", resp.StatusCode)
	}
	if resp, _ := do(t, browser(t), "GET", ts.base+"/", "", map[string]string{"Host": "evil.example:" + port}); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("the app under a rebound Host: %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	ts := start(t)
	u, _ := ts.LoginURL()
	for _, path := range []string{"/", "/api/hosts", strings.TrimPrefix(u, ts.base), "/nope"} {
		resp, _ := do(t, browser(t), "GET", ts.base+path, "", nil)
		h := resp.Header
		if h.Get("Content-Security-Policy") != csp || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: headers %v", path, h)
		}
	}
}

func TestStaticFiles(t *testing.T) {
	ts := start(t)
	c := browser(t)
	resp, body := do(t, c, "GET", ts.base+"/", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "<title>pfwd</title>") || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("index: %d %q %q", resp.StatusCode, body, resp.Header.Get("Cache-Control"))
	}
	resp, _ = do(t, c, "GET", ts.base+"/assets/app.js", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if resp, _ := do(t, c, "GET", ts.base+"/assets/", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a directory listing: %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "POST", ts.base+"/", "", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %d", resp.StatusCode)
	}
}

func TestAPIMapsToBackendCalls(t *testing.T) {
	ts := start(t)
	ts.b.results["list"] = map[string]any{"hosts": []any{map[string]any{"host": "devbox", "ports": []any{}}}}
	c, token := ts.loggedIn(t)
	for _, tc := range []struct {
		method, path, body, want string
	}{
		{"GET", "/api/hosts", "", "hosts"},
		{"POST", "/api/hosts/devbox/connect", "", `connect {"target":"devbox"}`},
		{"POST", "/api/hosts/alice@gpu:2222/disconnect", "", `disconnect {"host":"alice@gpu:2222"}`},
		{"POST", "/api/hosts/devbox/reconnect", "", `reconnect {"host":"devbox"}`},
		{"GET", "/api/hosts/devbox/ports", "", `list {"host":"devbox"}`},
		{"PATCH", "/api/hosts/devbox/settings", `{"auto":false}`, `set_auto {"host":"devbox","on":false}`},
		{"POST", "/api/forwards", `{"host":"devbox","spec":{"kind":"local","remote":5173}}`, `forward {"host":"devbox","spec":{"kind":"local","remote":5173}}`},
		{"DELETE", "/api/forwards/7", "", `stop {"ids":["7"]}`},
		{"PATCH", "/api/forwards/7", `{"label":"API"}`, `set_label {"id":"7","label":"API"}`},
		{"PATCH", "/api/forwards/7", `{"pinned":true}`, `set_pinned {"id":"7","pinned":true}`},
		{"GET", "/api/config", "", "config_get"},
		{"PUT", "/api/config", `{"text":"[ui]\n"}`, `config_put {"text":"[ui]\n"}`},
		{"PATCH", "/api/config", `{"values":{"forward.auto":true}}`, `config_set {"values":{"forward.auto":true}}`},
		{"POST", "/api/prompts/3", `{"accept":true}`, `answer {"accept":true,"id":"3"}`},
	} {
		resp, body := do(t, c, tc.method, ts.base+tc.path, tc.body, map[string]string{tokenHeader: token})
		if resp.StatusCode != 200 {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, resp.StatusCode, body)
			continue
		}
		if got := ts.b.last(); got != tc.want {
			t.Errorf("%s %s called %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
	resp, body := do(t, c, "GET", ts.base+"/api/hosts/devbox/ports", "", map[string]string{tokenHeader: token})
	if resp.StatusCode != 200 || !strings.HasPrefix(body, `{"host":"devbox"`) {
		t.Errorf("ports: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, c, "PATCH", ts.base+"/api/forwards/7", `{}`, map[string]string{tokenHeader: token}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an empty PATCH: %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "POST", ts.base+"/api/forwards", `{not json`, map[string]string{tokenHeader: token}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a bad body: %d", resp.StatusCode)
	}
	big := `{"text":"` + strings.Repeat("x", maxBody) + `"}`
	if resp, _ := do(t, c, "PUT", ts.base+"/api/config", big, map[string]string{tokenHeader: token}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a body over the cap: %d", resp.StatusCode)
	}
}

func TestErrorStatus(t *testing.T) {
	ts := start(t)
	c, token := ts.loggedIn(t)
	for code, want := range map[string]int{
		ipc.CodeUsage: 400, ipc.CodeNotFound: 404, ipc.CodeBind: 409, ipc.CodeConnect: 502,
		ipc.CodeAuth: 502, ipc.CodeHostKey: 502, ipc.CodeInternal: 500,
	} {
		ts.b.errs["hosts"] = ipc.Errorf(code, "it broke; fix it")
		resp, body := do(t, c, "GET", ts.base+"/api/hosts", "", map[string]string{tokenHeader: token})
		var e struct {
			Error ipc.Error `json:"error"`
		}
		if resp.StatusCode != want || json.Unmarshal([]byte(body), &e) != nil || e.Error.Code != code || e.Error.Message != "it broke; fix it" {
			t.Errorf("%s: %d %s, want %d", code, resp.StatusCode, body, want)
		}
	}
	ts.b.results["list"] = map[string]any{"hosts": []any{}}
	if resp, _ := do(t, c, "GET", ts.base+"/api/hosts/nope/ports", "", map[string]string{tokenHeader: token}); resp.StatusCode != 404 {
		t.Errorf("ports of an unknown host: %d", resp.StatusCode)
	}
}

func TestEventsStream(t *testing.T) {
	ts := start(t)
	c, token := ts.loggedIn(t)
	ts.b.results["stats"] = map[string]any{"forwards": []any{map[string]any{"id": "1", "bytes_in": 5}}}
	ws := strings.Replace(ts.base, "http", "ws", 1) + "/api/events?token=" + token
	if _, err := wsDial(t, ws, &websocket.DialOptions{HTTPClient: c}); err == nil {
		t.Error("a WebSocket without an Origin was accepted")
	}
	if _, err := wsDial(t, ws, &websocket.DialOptions{HTTPClient: c, HTTPHeader: http.Header{"Origin": {"http://evil.example"}}}); err == nil {
		t.Error("a WebSocket from another site was accepted")
	}
	noToken := strings.Replace(ws, token, "", 1)
	if _, err := wsDial(t, noToken, &websocket.DialOptions{HTTPClient: c, HTTPHeader: http.Header{"Origin": {ts.base}}}); err == nil {
		t.Error("a WebSocket without the token was accepted")
	}
	conn, err := wsDial(t, ws, &websocket.DialOptions{HTTPClient: c, HTTPHeader: http.Header{"Origin": {ts.base}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	var got []string
	for len(got) < 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read: %v (got %q)", err, got)
		}
		var e ipc.Event
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, e.Event+" "+string(e.Data))
	}
	if !strings.HasPrefix(got[0], "session_changed ") || !strings.HasPrefix(got[1], `stats {"forwards":[{"bytes_in":5,"id":"1"}]}`) {
		t.Errorf("events = %q", got)
	}
}

// wsDial opens a WebSocket and closes the handshake's response body.
func wsDial(t *testing.T, url string, o *websocket.DialOptions) (*websocket.Conn, error) {
	t.Helper()
	conn, resp, err := websocket.Dial(t.Context(), url, o)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return conn, err
}

func TestListenLoopbackOnly(t *testing.T) {
	s := New(&fakeBackend{}, Options{})
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !strings.HasPrefix(ln.Addr().String(), "127.0.0.1:") || !strings.HasPrefix(s.Addr(), "http://127.0.0.1:") {
		t.Errorf("listening on %s", ln.Addr())
	}
	if _, err := s.Listen(70000); err == nil {
		t.Error("port 70000 accepted")
	}
}

// TestServeEndsWithOpenStream: an open event stream doesn't hold up
// shutdown.
func TestServeEndsWithOpenStream(t *testing.T) {
	b := &fakeBackend{results: map[string]any{}, errs: map[string]error{}}
	s := New(b, Options{})
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	c := browser(t)
	u, _ := s.LoginURL()
	do(t, c, "GET", u, "", nil)
	_, body := do(t, c, "GET", s.Addr()+"/api/session", "", nil)
	var info struct{ Token string }
	_ = json.Unmarshal([]byte(body), &info)
	conn, err := wsDial(t, strings.Replace(s.Addr(), "http", "ws", 1)+"/api/events?token="+info.Token,
		&websocket.DialOptions{HTTPClient: c, HTTPHeader: http.Header{"Origin": {s.Addr()}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if _, _, err := conn.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(shutdownWait + 2*time.Second):
		t.Fatal("Serve didn't return with a stream open")
	}
}
