//go:build e2e

// Package e2e runs pfwd against real sshd containers: a server starts on
// the remote, the TUI shows it, enter forwards it, and an HTTP request to
// localhost reaches it. Run it with `make e2e` (needs Docker with compose).
package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/crypto/ssh"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/tui"
)

// keyPath is the private key the containers trust, created by TestMain.
var keyPath string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "pfwd-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	authorized, err := writeKey(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.Setenv("PFWD_E2E_AUTHORIZED_KEY", authorized); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if out, err := compose("up", "-d", "--build", "--wait"); err != nil {
		fmt.Fprintf(os.Stderr, "docker compose up: %v\n%s\n", err, out)
		_, _ = compose("down", "-v", "--remove-orphans")
		return 1
	}
	defer func() { _, _ = compose("down", "-v", "--remove-orphans") }()
	return m.Run()
}

func writeKey(dir string) (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "pfwd-e2e")
	if err != nil {
		return "", err
	}
	keyPath = filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func compose(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", append([]string{"compose", "-f", "compose.yaml"}, args...)...)
	return cmd.CombinedOutput()
}

func sshPort(t *testing.T, service string) int {
	t.Helper()
	out, err := compose("port", service, "22")
	if err != nil {
		t.Fatalf("docker compose port %s 22: %v\n%s", service, err, out)
	}
	ap, err := netip.ParseAddrPort(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse %q: %v", out, err)
	}
	return int(ap.Port())
}

// driver runs a tui.Model the way Bubble Tea would, without a renderer:
// commands run in goroutines and their messages come back through msgs.
type driver struct {
	m    tui.Model
	msgs chan tea.Msg
}

func newDriver(m tui.Model) *driver {
	d := &driver{m: m, msgs: make(chan tea.Msg, 1024)}
	d.update(tea.WindowSizeMsg{Width: 100, Height: 30})
	d.exec(m.Init())
	return d
}

func (d *driver) exec(cmd tea.Cmd) {
	if cmd != nil {
		go func() { d.msgs <- cmd() }()
	}
}

func (d *driver) update(msg tea.Msg) {
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			d.exec(c)
		}
		return
	}
	if msg == nil {
		return
	}
	next, cmd := d.m.Update(msg)
	m, ok := next.(tui.Model)
	if !ok {
		panic(fmt.Sprintf("Update returned %T, not tui.Model", next))
	}
	d.m = m
	d.exec(cmd)
}

func (d *driver) view() string {
	return ansi.Strip(d.m.View().Content)
}

// selectedRow returns the table line with the ▶ cursor.
func (d *driver) selectedRow() string {
	for _, line := range strings.Split(d.view(), "\n") {
		if strings.HasPrefix(line, "▶") {
			return line
		}
	}
	return ""
}

// waitFor processes messages until the view contains want.
func (d *driver) waitFor(t *testing.T, want string, timeout time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	deadline := time.After(timeout)
	for !strings.Contains(d.view(), want) {
		select {
		case msg := <-d.msgs:
			d.update(msg)
		case <-deadline:
			t.Fatalf("after %v the screen still lacks %q:\n%s", timeout, want, d.view())
		}
	}
	return time.Since(start)
}

func TestE2EForwardDetectedPort(t *testing.T) {
	cases := []struct {
		service string
		process string // shown only when ss names it
	}{
		{"ss", "python3"},
		{"proc", ""},
	}
	for _, c := range cases {
		t.Run(c.service, func(t *testing.T) {
			forwardDetectedPort(t, c.service, c.process)
		})
	}
}

func forwardDetectedPort(t *testing.T, service, process string) {
	hc := &sshx.HostConfig{
		Alias:                 service,
		HostName:              "127.0.0.1",
		Port:                  sshPort(t, service),
		User:                  "pfwd",
		IdentityFiles:         []string{keyPath},
		IdentityAgent:         "none",
		KnownHostsFiles:       []string{filepath.Join(t.TempDir(), "known_hosts")},
		StrictHostKeyChecking: "accept-new",
	}
	eng := engine.New(engine.Options{
		Interval: 2 * time.Second,
		Dial: func(ctx context.Context, _ engine.HostID, p sshx.HostKeyPrompter) (engine.Conn, error) {
			c, err := sshx.Dial(ctx, hc, sshx.DialOptions{Prompter: p})
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	})
	defer eng.Close()
	host := engine.HostID(service)
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)

	// sshd itself is listening, so the table appears once detection runs.
	d.waitFor(t, ":22", 20*time.Second)

	if out, err := compose("exec", "-d", "-u", "pfwd", service, "python3", "-m", "http.server", "8123", "--bind", "127.0.0.1"); err != nil {
		t.Fatalf("start the http server: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = compose("exec", service, "pkill", "-f", "http.server 8123") })
	started := time.Now()
	d.waitFor(t, ":8123", 3*time.Second)
	t.Logf("%s: :8123 appeared %v after the server started", service, time.Since(started).Round(time.Millisecond))
	if process != "" && !strings.Contains(d.view(), process) {
		t.Errorf("the process column lacks %q:\n%s", process, d.view())
	}

	// Move the cursor to the :8123 row. Its position varies: Docker's DNS
	// resolver also listens in the container, on 127.0.0.11 and a random port.
	d.update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	for range 20 {
		if strings.Contains(d.selectedRow(), " :8123 ") {
			break
		}
		d.update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if !strings.Contains(d.selectedRow(), " :8123 ") {
		t.Fatalf("could not select the :8123 row:\n%s", d.view())
	}
	d.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	d.waitFor(t, "Forwarded :8123 to localhost:8123", 5*time.Second)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8123/")
	if err != nil {
		t.Fatalf("GET through the forward: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET through the forward: %s", resp.Status)
	}
}
