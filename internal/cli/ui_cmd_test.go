package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// noRedirect is a client that shows redirects instead of following them.
var noRedirect = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func login(t *testing.T, u string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestUIMethodStartsTheServerOnce(t *testing.T) {
	headlessEnv(t)
	path := startDaemon(t)
	var first, second uiJSON
	if err := call(t, path, "ui", uiParams{}, &first); err != nil {
		t.Fatal(err)
	}
	if err := call(t, path, "ui", uiParams{}, &second); err != nil {
		t.Fatal(err)
	}
	if first.Address == "" || first.Address != second.Address || first.URL == second.URL || first.Reused || !second.Reused {
		t.Errorf("ui twice: %+v then %+v", first, second)
	}
	if !strings.HasPrefix(first.URL, first.Address+"/login?token=") {
		t.Errorf("login URL %q", first.URL)
	}
	if code := login(t, first.URL); code != http.StatusSeeOther {
		t.Errorf("logging in with the URL: %d", code)
	}
	if code := login(t, first.URL); code != http.StatusForbidden {
		t.Errorf("the URL worked twice: %d", code)
	}
	var st statusJSON
	if err := call(t, path, "status", nil, &st); err != nil || st.Web != first.Address {
		t.Errorf("status = %+v, %v; want the web address", st, err)
	}
	if code, out, _ := run(t, "daemon", "status"); code != 0 || !strings.Contains(out, "web UI at "+first.Address) {
		t.Errorf("pfwd daemon status doesn't show the web UI: code %d:\n%s", code, out)
	}
}

func TestUICommand(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(5173, "node"))}, true)
	path := startDaemon(t, "-F", h.sshConfig)
	code, out, errOut := run(t, "ui", "--url", "testbox")
	if code != 0 || !strings.HasPrefix(out, "http://127.0.0.1:") || strings.Count(out, "\n") != 1 {
		t.Fatalf("pfwd ui --url: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code := login(t, strings.TrimSpace(out)); code != http.StatusSeeOther {
		t.Errorf("the printed URL doesn't log in: %d", code)
	}
	var st statusJSON
	if err := call(t, path, "status", nil, &st); err != nil || len(st.Hosts) != 1 || st.Hosts[0].Host != "testbox" {
		t.Errorf("pfwd ui testbox didn't connect it: %+v, %v", st, err)
	}
	code, out, _ = run(t, "ui", "--no-open")
	if code != 0 || !strings.Contains(out, "/login?token=") || !strings.Contains(out, "once") {
		t.Errorf("pfwd ui --no-open: code %d, %q", code, out)
	}
	if code, _, errOut := run(t, "ui", "-l", "alice"); code != exitUsage || !strings.Contains(errOut, "--login") {
		t.Errorf("pfwd ui -l: code %d, stderr %q", code, errOut)
	}
}

// TestUIOpensTheLoginLink: plain pfwd ui hands the browser the one-time
// link, and prints it when no browser opens.
func TestUIOpensTheLoginLink(t *testing.T) {
	headlessEnv(t)
	startDaemon(t)
	cmd := newUICmd(&rootFlags{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(t.Context())
	var opened string
	open := func(_ context.Context, u string) error { opened = u; return nil }
	if err := runUI(cmd, nil, uiOptions{open: open}); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/login\?token=[A-Za-z0-9_-]+$`).MatchString(opened) || !strings.Contains(out.String(), "Opened the pfwd web UI") {
		t.Errorf("opened %q; output %q", opened, out.String())
	}
	out.Reset()
	fail := func(context.Context, string) error { return errors.New("no browser here") }
	if err := runUI(cmd, nil, uiOptions{open: fail}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no browser here") || !strings.Contains(out.String(), "/login?token=") {
		t.Errorf("without a browser: %q", out.String())
	}
}

// oldDaemon answers like a v0.5.0 daemon, which has no ui call.
type oldDaemon struct{}

func (oldDaemon) Handle(_ context.Context, method string, _ json.RawMessage) (any, error) {
	if method == "status" {
		return statusJSON{Running: true, Version: "v0.5.0"}, nil
	}
	return nil, ipc.Errorf(ipc.CodeUsage, "unknown method %q; is this pfwd older than the daemon?", method)
}

func (oldDaemon) Watch(ctx context.Context, _ func(ipc.Event) error) error {
	<-ctx.Done()
	return nil
}

func TestUIWithAnOlderDaemon(t *testing.T) {
	headlessEnv(t)
	path := daemonSocket(t)
	ln, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ipc.NewServer(oldDaemon{}, nil).Serve(t.Context(), ln) }()
	code, _, errOut := run(t, "ui", "--url")
	if code != exitError || !strings.Contains(errOut, "v0.5.0") || !strings.Contains(errOut, "pfwd daemon stop") {
		t.Errorf("pfwd ui with a v0.5.0 daemon: code %d, stderr %q", code, errOut)
	}
}

func TestUIRefusesDaemonSettings(t *testing.T) {
	for _, flag := range []string{"--no-auto", "--auto", "--accept-new", "--bind=0.0.0.0"} {
		if code, _, errOut := run(t, "ui", flag); code != exitUsage || !strings.Contains(errOut, "config.toml") {
			t.Errorf("pfwd ui %s: code %d, stderr %q", flag, code, errOut)
		}
	}
}
