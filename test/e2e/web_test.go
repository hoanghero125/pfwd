//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestE2EWebUI runs the real binary's web UI against the ss container:
// pfwd ui starts the daemon and prints a login link; the API and the event
// stream forward a detected port, which then answers locally. With
// PFWD_E2E_BROWSER=1 (CI), Playwright does the forwarding in a browser.
func TestE2EWebUI(t *testing.T) {
	dir := t.TempDir()
	bin := buildPfwd(t, dir)
	socket := e2eSocket(t)
	cmd := func(args ...string) *exec.Cmd {
		c := pfwdCmd(t, bin, dir, args...)
		c.Env = append(c.Env, "PFWD_SOCKET="+socket)
		return c
	}
	cmd("version") // writes the ssh config and config.toml
	// Auto-forward off, so the port waits in "detected" for a click.
	f, err := os.OpenFile(filepath.Join(dir, "config.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n[forward]\nauto = false\n")
	_ = f.Close()
	t.Cleanup(func() { _ = cmd("daemon", "stop").Run() })
	startServer(t, "ss", "8142")

	out, err := cmd("ui", "--url", "e2e-ss").Output()
	login := strings.TrimSpace(string(out))
	if err != nil || !strings.HasPrefix(login, "http://127.0.0.1:") {
		t.Fatalf("pfwd ui --url: %v %q", err, out)
	}
	base := login[:strings.Index(login, "/login")]
	port := strings.TrimPrefix(base, "http://127.0.0.1:")

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	call := func(method, path, token, body string, out any) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("X-PFWD-Token", token)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if out != nil && resp.StatusCode == 200 {
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("%s %s: %v\n%s", method, path, err, data)
			}
		}
		return resp.StatusCode
	}
	if code := call("GET", strings.TrimPrefix(login, base), "", "", nil); code != 200 {
		t.Fatalf("logging in: %d", code)
	}
	var session struct{ Token string }
	if call("GET", "/api/session", "", "", &session); session.Token == "" {
		t.Fatal("no session token after login")
	}
	if code := call("GET", "/api/hosts", "", "", nil); code != http.StatusForbidden {
		t.Errorf("an API call without the token: %d", code)
	}
	evil, _ := http.NewRequestWithContext(t.Context(), "GET", base+"/", nil)
	evil.Host = "evil.example:" + port
	if resp, err := http.DefaultClient.Do(evil); err != nil || resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("a rebound Host: %v %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}

	type hostPorts struct {
		State string `json:"state"`
		Ports []struct {
			Port int `json:"port"`
		} `json:"ports"`
		Forwards []struct {
			ID     string `json:"id"`
			Remote int    `json:"remote"`
			Local  int    `json:"local"`
			State  string `json:"state"`
		} `json:"forwards"`
	}
	waitFor := func(what string, ok func(hostPorts) bool) hostPorts {
		t.Helper()
		var hp hostPorts
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			hp = hostPorts{}
			if call("GET", "/api/hosts/e2e-ss/ports", session.Token, "", &hp) == 200 && ok(hp) {
				return hp
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("waiting for %s: last %+v", what, hp)
		return hp
	}
	waitFor("port 8142 detected", func(hp hostPorts) bool {
		for _, p := range hp.Ports {
			if p.Port == 8142 {
				return true
			}
		}
		return false
	})

	if os.Getenv("PFWD_E2E_BROWSER") == "1" {
		out, err := cmd("ui", "--url").Output()
		if err != nil {
			t.Fatal(err)
		}
		pw := exec.Command("npx", "playwright", "test", "--project=real", "--reporter=line")
		pw.Dir = filepath.Join("..", "..", "web")
		pw.Env = append(os.Environ(), "PFWD_UI_URL="+strings.TrimSpace(string(out)), "PFWD_E2E_HOST=e2e-ss", "PFWD_E2E_PORT=8142")
		if out, err := pw.CombinedOutput(); err != nil {
			t.Fatalf("Playwright in a real browser: %v\n%s", err, out)
		}
	} else {
		ws := "ws://127.0.0.1:" + port + "/api/events?token=" + session.Token
		conn, resp, err := websocket.Dial(t.Context(), ws, &websocket.DialOptions{HTTPClient: c, HTTPHeader: http.Header{"Origin": {base}}})
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			t.Fatalf("the event stream: %v", err)
		}
		defer func() { _ = conn.CloseNow() }()
		if code := call("POST", "/api/forwards", session.Token, `{"host":"e2e-ss","spec":{"kind":"local","remote":8142}}`, nil); code != 200 {
			t.Fatalf("POST /api/forwards: %d", code)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("no active forward on the event stream: %v", err)
			}
			var e struct {
				Event string `json:"event"`
				Data  struct {
					Remote int    `json:"remote"`
					State  string `json:"state"`
				} `json:"data"`
			}
			if json.Unmarshal(data, &e) == nil && e.Event == "forward_changed" && e.Data.Remote == 8142 && e.Data.State == "active" {
				break
			}
		}
	}

	hp := waitFor("an active forward of 8142", func(hp hostPorts) bool {
		return len(hp.Forwards) == 1 && hp.Forwards[0].Remote == 8142 && hp.Forwards[0].State == "active"
	})
	local := hp.Forwards[0].Local
	get(t, fmt.Sprint(local))
	if code := call("DELETE", "/api/forwards/"+hp.Forwards[0].ID, session.Token, "", nil); code != 200 {
		t.Fatalf("DELETE the forward: %d", code)
	}
	waitFor("no forwards", func(hp hostPorts) bool { return len(hp.Forwards) == 0 })
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 2*time.Second); err == nil {
		_ = conn.Close()
		t.Error("the stopped forward still accepts connections")
	}
	if out, err := cmd("daemon", "stop").CombinedOutput(); err != nil {
		t.Fatalf("pfwd daemon stop: %v\n%s", err, out)
	}
	if resp, err := http.Get(base + "/"); err == nil {
		_ = resp.Body.Close()
		t.Error("the web UI still answers after pfwd daemon stop")
	}
}
