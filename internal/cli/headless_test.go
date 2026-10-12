package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// syncBuffer is a bytes.Buffer safe to read while a command writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeLinux answers pfwd's probe as a Linux server with ss, then streams
// one snapshot of lines and waits.
func fakeLinux(lines ...string) func(ctx context.Context, cmd string, stdout io.Writer) int {
	return func(ctx context.Context, cmd string, stdout io.Writer) int {
		if strings.Contains(cmd, "uname") {
			fmt.Fprint(stdout, "PFWD_OS Linux\nHAVE_SS\n")
			return 0
		}
		fmt.Fprint(stdout, "\x1ePFWD\n"+strings.Join(lines, "")+"\x1ePFWD END\n")
		<-ctx.Done()
		return 0
	}
}

func ssLine(port int, process string) string {
	return fmt.Sprintf("LISTEN 0 5 127.0.0.1:%d 0.0.0.0:* users:((%q,pid=7,fd=3))\n", port, process)
}

// testHost is an sshtest server and an ssh config naming it "testbox".
type testHost struct {
	srv        *sshtest.Server
	sshConfig  string
	knownHosts string
}

// newTestHost starts a server and writes an ssh config for it. With
// known, its host key is in known_hosts already.
func newTestHost(t *testing.T, o sshtest.Options, known bool) *testHost {
	t.Helper()
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	o.AuthorizedKeys = append(o.AuthorizedKeys, signer.PublicKey())
	srv := sshtest.Start(t, o)
	kh := filepath.Join(dir, "known_hosts")
	body := ""
	if known {
		body = srv.KnownHostsLine() + "\n"
	}
	if err := os.WriteFile(kh, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("Host testbox\n  HostName %s\n  Port %d\n  User tester\n  IdentityFile %s\n  IdentitiesOnly yes\n  IdentityAgent none\n  UserKnownHostsFile %s\n  ConnectTimeout 5\n",
		srv.Host(), srv.Port(), filepath.ToSlash(keyPath), filepath.ToSlash(kh))
	path := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return &testHost{srv: srv, sshConfig: path, knownHosts: kh}
}

// headlessEnv keeps a test away from the user's config, state and log.
func headlessEnv(t *testing.T) {
	t.Helper()
	writeConfig(t, "[ssh]\nuse_openssh_config = false\n\n[ui]\nnotifications = false\n")
	tempState(t)
	t.Setenv("PFWD_LOG", "")
}

func TestLsOneShot(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(5173, "node"), ssLine(6379, "redis-server"))}, true)
	code, out, errOut := run(t, "ls", "testbox", "-F", h.sshConfig)
	if code != 0 {
		t.Fatalf("pfwd ls: code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"PORT", ":5173", "node", ":6379", "redis-server", "Redis"} {
		if !strings.Contains(out, want) {
			t.Errorf("pfwd ls lacks %q:\n%s", want, out)
		}
	}

	code, out, _ = run(t, "ls", "testbox", "-F", h.sshConfig, "--json")
	var got struct {
		Hosts []struct {
			Host  string `json:"host"`
			State string `json:"state"`
			OS    string `json:"os"`
			Ports []struct {
				Port    int      `json:"port"`
				Addrs   []string `json:"addrs"`
				Process string   `json:"process"`
				Label   string   `json:"label"`
			} `json:"ports"`
			Forwards []any `json:"forwards"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil {
		t.Fatalf("pfwd ls --json: code %d, %v:\n%s", code, err, out)
	}
	if len(got.Hosts) != 1 || got.Hosts[0].Host != "testbox" || got.Hosts[0].State != "connected" || got.Hosts[0].OS != "Linux" ||
		len(got.Hosts[0].Ports) != 2 || got.Hosts[0].Ports[0].Port != 5173 || got.Hosts[0].Forwards == nil {
		t.Errorf("JSON = %+v", got)
	}
}

func TestLsNeedsAHost(t *testing.T) {
	headlessEnv(t)
	code, _, errOut := run(t, "ls")
	if code != 2 || !strings.Contains(errOut, "pfwd ls <host>") || !strings.Contains(errOut, "pfwd daemon start") {
		t.Errorf("pfwd ls: code %d, stderr %q; want 2 and both fixes", code, errOut)
	}
}

func TestLsUnknownHostKeyWithoutTerminal(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux()}, false)
	code, out, errOut := run(t, "ls", "testbox", "-F", h.sshConfig)
	if code != 5 || out != "" || !strings.Contains(errOut, "--accept-new") {
		t.Errorf("pfwd ls: code %d, stdout %q, stderr %q; want 5 and the --accept-new fix", code, out, errOut)
	}
	if data, _ := os.ReadFile(h.knownHosts); len(data) != 0 {
		t.Errorf("known_hosts was written: %q", data)
	}
}

// echoListener serves an echo server on 127.0.0.1 and returns its port.
func echoListener(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	return int(netip.MustParseAddrPort(ln.Addr().String()).Port())
}

func TestFwdForeground(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo"))}, true)
	local := freeLocalPort(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out, errOut := &syncBuffer{}, &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- executeContext(ctx, []string{"fwd", "testbox", fmt.Sprintf("%d:%d", local, echo), "-F", h.sshConfig}, out, errOut)
	}()
	deadline := time.Now().Add(10 * time.Second)
	want := fmt.Sprintf("Forwarded :%d to localhost:%d", echo, local)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("no %q:\nstdout %q\nstderr %q", want, out.String(), errOut.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, "ping"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Errorf("echo through the forward = %q, %v", buf, err)
	}
	_ = c.Close()
	cancel()
	if code := <-done; code != 130 {
		t.Errorf("exit code after an interrupt = %d, want 130; stderr %q", code, errOut.String())
	}
}

func TestFwdBindFailure(t *testing.T) {
	headlessEnv(t)
	writeConfig(t, "[ssh]\nuse_openssh_config = false\n\n[forward]\non_conflict = \"fail\"\n")
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux()}, true)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := int(netip.MustParseAddrPort(busy.Addr().String()).Port())
	code, _, errOut := run(t, "fwd", "testbox", fmt.Sprintf("%d:80", port), "-F", h.sshConfig)
	if code != 6 || !strings.Contains(errOut, "in use") {
		t.Errorf("pfwd fwd on a busy port: code %d, stderr %q; want 6", code, errOut)
	}
}

func TestFwdNeedsASpec(t *testing.T) {
	headlessEnv(t)
	for _, args := range [][]string{{"fwd", "testbox"}, {"fwd", "testbox", "notaport"}, {"fwd", "testbox", "-R", "x"}} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Errorf("pfwd %q: code %d, want 2", args, code)
		}
	}
}

// freeLocalPort returns a local port that was free a moment ago.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := int(netip.MustParseAddrPort(ln.Addr().String()).Port())
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
