//go:build e2e

// Package e2e runs pfwd against real sshd containers: a server starts on
// the remote, the TUI shows it, it gets forwarded (by enter, or on its own
// with auto-forward), and an HTTP request to localhost reaches it. Run it
// with `make e2e` (needs Docker with compose).
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

// runFor processes messages for d, whatever they show.
func (d *driver) runFor(dur time.Duration) {
	deadline := time.After(dur)
	for {
		select {
		case msg := <-d.msgs:
			d.update(msg)
		case <-deadline:
			return
		}
	}
}

// key presses one key.
func (d *driver) key(k rune) {
	d.update(tea.KeyPressMsg{Code: k, Text: string(k)})
}

// selectPort moves the cursor to the row of a remote port. Row positions
// vary: Docker's DNS resolver also listens in the container, on
// 127.0.0.11 and a random port.
func (d *driver) selectPort(t *testing.T, port string) {
	t.Helper()
	d.key('g')
	for range 20 {
		if strings.Contains(d.selectedRow(), " :"+port+" ") {
			return
		}
		d.key('j')
	}
	t.Fatalf("could not select the :%s row:\n%s", port, d.view())
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

// newHost returns the HostConfig for a service's sshd.
func newHost(t *testing.T, service string) *sshx.HostConfig {
	return &sshx.HostConfig{
		Alias:                 service,
		HostName:              "127.0.0.1",
		Port:                  sshPort(t, service),
		User:                  "pfwd",
		IdentityFiles:         []string{keyPath},
		IdentityAgent:         "none",
		KnownHostsFiles:       []string{filepath.Join(t.TempDir(), "known_hosts")},
		StrictHostKeyChecking: "accept-new",
	}
}

// dialer dials hc for the engine.
func dialer(hc *sshx.HostConfig) engine.DialFunc {
	return func(ctx context.Context, _ engine.HostID, p engine.Prompts) (engine.Conn, error) {
		c, err := sshx.Dial(ctx, hc, sshx.DialOptions{Prompter: p})
		if err != nil {
			return nil, err
		}
		return c, nil
	}
}

// startServer runs python3's HTTP server on the remote until the test
// ends.
func startServer(t *testing.T, service, port string) time.Time {
	t.Helper()
	if out, err := compose("exec", "-d", "-u", "pfwd", service, "python3", "-m", "http.server", port, "--bind", "127.0.0.1"); err != nil {
		t.Fatalf("start the http server: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = compose("exec", service, "pkill", "-f", "http.server "+port) })
	return time.Now()
}

// get checks that a request to a local port reaches the remote server.
func get(t *testing.T, port string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatalf("GET through the forward: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET through the forward: %s", resp.Status)
	}
}

func forwardDetectedPort(t *testing.T, service, process string) {
	settings := engine.DefaultSettings()
	settings.Auto = false
	eng := engine.New(engine.Options{
		Interval: 2 * time.Second,
		Dial:     dialer(newHost(t, service)),
		Settings: func(engine.HostID) engine.HostSettings { return settings },
	})
	defer eng.Close()
	host := engine.HostID(service)
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)

	// sshd itself listens on :22, which the default ignore list hides, so
	// the count of hidden ports shows once detection runs.
	d.waitFor(t, "ignored hidden", 20*time.Second)

	started := startServer(t, service, "8123")
	d.waitFor(t, ":8123", 3*time.Second)
	t.Logf("%s: :8123 appeared %v after the server started", service, time.Since(started).Round(time.Millisecond))
	if process != "" && !strings.Contains(d.view(), process) {
		t.Errorf("the process column lacks %q:\n%s", process, d.view())
	}

	d.selectPort(t, "8123")
	d.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	d.waitFor(t, "Forwarded :8123 to localhost:8123", 5*time.Second)
	get(t, "8123")
}

// TestE2EAutoForward: a server that starts after pfwd connects is
// forwarded with no key press, labeled and probed; x stops it and the
// next snapshots leave it stopped.
func TestE2EAutoForward(t *testing.T) {
	for _, service := range []string{"ss", "proc"} {
		t.Run(service, func(t *testing.T) { autoForward(t, service) })
	}
}

func autoForward(t *testing.T, service string) {
	const interval = 2 * time.Second
	eng := engine.New(engine.Options{
		Interval:  interval,
		Debounce:  1,
		HTTPProbe: true,
		Dial:      dialer(newHost(t, service)),
	})
	defer eng.Close()
	host := engine.HostID(service)
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)
	d.waitFor(t, "ignored hidden", 20*time.Second)

	// Ports in the first snapshot stay as they are; this one is new.
	started := startServer(t, service, "6006")
	d.waitFor(t, "Forwarded :6006 to localhost:6006 (auto)", 3*interval)
	t.Logf("%s: :6006 was forwarded %v after the server started", service, time.Since(started).Round(time.Millisecond))
	d.waitFor(t, "TensorBoard http", 5*time.Second)
	get(t, "6006")

	d.selectPort(t, "6006")
	d.key('x')
	d.waitFor(t, "Stopped :6006", 5*time.Second)
	d.runFor(3 * interval)
	for _, f := range eng.Forwards() {
		if f.Spec.RemotePort == 6006 {
			t.Fatalf("auto-forward recreated :6006 after x:\n%s", d.view())
		}
	}
}
