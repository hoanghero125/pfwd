//go:build e2e && !windows

package e2e

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

// TestE2EBinary runs the real pfwd binary in a pseudo-terminal: the host
// comes from an ssh config passed with -F, the unknown host key is accepted
// with accept-new, the table renders (with sshd on :22 hidden by the
// default ignore list), and q quits with exit code 0.
func TestE2EBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pfwd")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/pfwd")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cfg := filepath.Join(dir, "ssh_config")
	config := fmt.Sprintf(`Host e2e-ss
  HostName 127.0.0.1
  Port %d
  User pfwd
  IdentityFile %s
  IdentitiesOnly yes
  IdentityAgent none
  UserKnownHostsFile %s
  StrictHostKeyChecking accept-new
`, sshPort(t, "ss"), keyPath, filepath.Join(dir, "known_hosts"))
	if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-F", cfg, "e2e-ss")
	cmd.Env = append(os.Environ(), "HOME="+dir, "XDG_STATE_HOME="+filepath.Join(dir, "state"), "TERM=xterm-256color")
	term, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatalf("start pfwd in a pty: %v", err)
	}
	defer term.Close()

	var mu sync.Mutex
	var screen bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			mu.Lock()
			screen.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	shown := func() string {
		mu.Lock()
		defer mu.Unlock()
		return ansi.Strip(screen.String())
	}

	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(shown(), "ignored hidden") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the table never counted :22 as hidden; the terminal got:\n%s", shown())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := io.WriteString(term, "q"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pfwd exited with %v; the terminal got:\n%s", err, shown())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("pfwd did not quit after q")
	}
	logPath := filepath.Join(dir, "state", "pfwd", "pfwd.log")
	if runtime.GOOS == "darwin" {
		// macOS keeps state under Library, whatever XDG_STATE_HOME says.
		logPath = filepath.Join(dir, "Library", "Application Support", "pfwd", "pfwd.log")
	}
	if data, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(data), `"msg":"connected"`) {
		t.Errorf("pfwd.log = %q, %v; want a connected entry", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "known_hosts")); err != nil || !strings.Contains(string(data), "ssh-ed25519") {
		t.Errorf("known_hosts = %q, %v; want the accepted host key", data, err)
	}
}
