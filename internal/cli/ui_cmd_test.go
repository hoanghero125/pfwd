package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

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
