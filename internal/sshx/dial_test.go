package sshx

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"errors"
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
	"golang.org/x/crypto/ssh/agent"

	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

func ed25519Signer(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

// writeKey writes an OpenSSH private key (encrypted when passphrase is set)
// and its .pub file, and returns the key path and signer.
func writeKey(t *testing.T, dir, name, passphrase string) (string, ssh.Signer) {
	t.Helper()
	signer, priv := ed25519Signer(t)
	var block *pem.Block
	var err error
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".pub", ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, signer
}

type fakePrompter struct {
	mu     sync.Mutex
	answer bool
	delay  time.Duration
	asked  []HostKeyQuestion
}

func (p *fakePrompter) ConfirmHostKey(ctx context.Context, q HostKeyQuestion) (bool, error) {
	p.mu.Lock()
	p.asked = append(p.asked, q)
	p.mu.Unlock()
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return p.answer, nil
}

func (p *fakePrompter) questions() []HostKeyQuestion {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]HostKeyQuestion(nil), p.asked...)
}

func noAgent(*HostConfig) (io.ReadWriteCloser, error) { return nil, errors.New("no agent in tests") }

// keyringAgent serves an in-process ssh-agent holding keys, over a pipe per call.
func keyringAgent(t *testing.T, keys ...ed25519.PrivateKey) func(*HostConfig) (io.ReadWriteCloser, error) {
	t.Helper()
	kr := agent.NewKeyring()
	for _, k := range keys {
		if err := kr.Add(agent.AddedKey{PrivateKey: k}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	return func(*HostConfig) (io.ReadWriteCloser, error) {
		client, server := net.Pipe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = agent.ServeAgent(kr, server)
			server.Close()
		}()
		return client, nil
	}
}

type fixture struct {
	srv        *sshtest.Server
	hc         *HostConfig
	knownHosts string
	key        ssh.Signer
}

// newFixture starts a server that trusts one key file and returns a
// HostConfig that knows the server's host key.
func newFixture(t *testing.T, o sshtest.Options) *fixture {
	t.Helper()
	dir := t.TempDir()
	keyPath, key := writeKey(t, dir, "id_ed25519", "")
	o.AuthorizedKeys = append(o.AuthorizedKeys, key.PublicKey())
	srv := sshtest.Start(t, o)
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte(srv.KnownHostsLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &fixture{
		srv: srv,
		hc: &HostConfig{
			Alias:                 "testhost",
			HostName:              srv.Host(),
			Port:                  srv.Port(),
			User:                  "tester",
			IdentityFiles:         []string{keyPath},
			KnownHostsFiles:       []string{kh},
			StrictHostKeyChecking: "ask",
		},
		knownHosts: kh,
		key:        key,
	}
}

func mustDial(t *testing.T, hc *HostConfig, o DialOptions) *Conn {
	t.Helper()
	if o.dialAgent == nil {
		o.dialAgent = noAgent
	}
	c, err := Dial(t.Context(), hc, o)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func echoExec(_ context.Context, cmd string, stdout io.Writer) int {
	_, _ = fmt.Fprintf(stdout, "ran %s\n", cmd)
	return 0
}

func TestDialKeyFile(t *testing.T) {
	f := newFixture(t, sshtest.Options{Exec: echoExec})
	c := mustDial(t, f.hc, DialOptions{})
	out, err := c.Output(t.Context(), "uname -s")
	if err != nil || string(out) != "ran uname -s\n" {
		t.Fatalf("Output = %q, %v", out, err)
	}
}

func TestDialAgent(t *testing.T) {
	_, agentKey := ed25519Signer(t)
	agentPub, err := ssh.NewSignerFromKey(agentKey)
	if err != nil {
		t.Fatal(err)
	}
	// Only the agent's key is authorized, and the config has no key files.
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{agentPub.PublicKey()}})
	kh := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(kh, []byte(srv.KnownHostsLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hc := &HostConfig{Alias: "a", HostName: srv.Host(), Port: srv.Port(), User: "u", KnownHostsFiles: []string{kh}, StrictHostKeyChecking: "ask"}
	mustDial(t, hc, DialOptions{dialAgent: keyringAgent(t, agentKey)})
}

func TestDialAgentFailsThenKeyFile(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	_, wrong := ed25519Signer(t)
	mustDial(t, f.hc, DialOptions{dialAgent: keyringAgent(t, wrong)})
}

func TestDialIdentitiesOnlySkipsOtherAgentKeys(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	f.hc.IdentitiesOnly = true
	_, other := ed25519Signer(t)
	// Works through the key file; the unrelated agent key is not offered.
	mustDial(t, f.hc, DialOptions{dialAgent: keyringAgent(t, other)})
}

func TestDialWrongKey(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	other, _ := writeKey(t, t.TempDir(), "other", "")
	f.hc.IdentityFiles = []string{other}
	_, err := Dial(t.Context(), f.hc, DialOptions{dialAgent: noAgent})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "ssh-add") || !strings.Contains(err.Error(), "testhost") {
		t.Errorf("message lacks a fix or the host: %v", err)
	}
}

func TestDialEncryptedKeySkipped(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	enc, _ := writeKey(t, t.TempDir(), "id_locked", "hunter2")
	f.hc.IdentityFiles = []string{enc}
	_, err := Dial(t.Context(), f.hc, DialOptions{dialAgent: noAgent})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), enc) || !strings.Contains(err.Error(), "ssh-add") {
		t.Errorf("message should name the passphrase-protected key and ssh-add: %v", err)
	}
}

func TestDialEncryptedKeyHeldByAgent(t *testing.T) {
	dir := t.TempDir()
	signer, priv := ed25519Signer(t)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "id_locked")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{signer.PublicKey()}})
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte(srv.KnownHostsLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hc := &HostConfig{Alias: "a", HostName: srv.Host(), Port: srv.Port(), User: "u", IdentityFiles: []string{keyPath}, IdentitiesOnly: true, KnownHostsFiles: []string{kh}, StrictHostKeyChecking: "ask"}
	mustDial(t, hc, DialOptions{dialAgent: keyringAgent(t, priv)})
}

func TestDialUnknownHost(t *testing.T) {
	cases := []struct {
		name      string
		strict    string
		acceptNew bool
		prompter  *fakePrompter
		wantErr   bool
		wantAsked bool
		wantAdded bool
	}{
		{name: "ask yes", strict: "ask", prompter: &fakePrompter{answer: true}, wantAsked: true, wantAdded: true},
		{name: "ask no", strict: "ask", prompter: &fakePrompter{answer: false}, wantErr: true, wantAsked: true},
		{name: "ask without prompter", strict: "ask", wantErr: true},
		{name: "accept-new config", strict: "accept-new", prompter: &fakePrompter{}, wantAdded: true},
		{name: "accept-new flag", strict: "ask", acceptNew: true, prompter: &fakePrompter{}, wantAdded: true},
		{name: "strict no", strict: "no", prompter: &fakePrompter{}, wantAdded: true},
		{name: "strict yes", strict: "yes", prompter: &fakePrompter{answer: true}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, sshtest.Options{})
			// A fresh known_hosts in a directory that does not exist yet.
			kh := filepath.Join(t.TempDir(), "sub", "known_hosts")
			f.hc.KnownHostsFiles = []string{kh}
			f.hc.StrictHostKeyChecking = c.strict
			o := DialOptions{AcceptNew: c.acceptNew, dialAgent: noAgent}
			if c.prompter != nil {
				o.Prompter = c.prompter
			}
			conn, err := Dial(t.Context(), f.hc, o)
			if conn != nil {
				conn.Close()
			}
			if c.wantErr {
				if !errors.Is(err, ErrHostKey) {
					t.Fatalf("err = %v, want ErrHostKey", err)
				}
			} else if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			if c.prompter != nil {
				asked := c.prompter.questions()
				if c.wantAsked != (len(asked) == 1) {
					t.Errorf("asked = %v, want asked=%v", asked, c.wantAsked)
				}
				if len(asked) == 1 {
					q := asked[0]
					if q.Host != "testhost" || q.KeyType != "ssh-ed25519" || !strings.HasPrefix(q.Fingerprint, "SHA256:") {
						t.Errorf("question = %+v", q)
					}
				}
			}
			data, _ := os.ReadFile(kh)
			if added := strings.Contains(string(data), "ssh-ed25519"); added != c.wantAdded {
				t.Errorf("known_hosts = %q, want added=%v", data, c.wantAdded)
			}
			if c.wantAdded {
				if line := strings.TrimSpace(string(data)); line != strings.TrimSpace(f.srv.KnownHostsLine()) {
					t.Errorf("added line = %q, want %q", line, f.srv.KnownHostsLine())
				}
				// The new entry is trusted on the next connection without asking.
				f.hc.StrictHostKeyChecking = "yes"
				mustDial(t, f.hc, DialOptions{})
			}
		})
	}
}

func TestDialAppendsAfterLineWithoutNewline(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	other := sshtest.Start(t, sshtest.Options{})
	if err := os.WriteFile(f.knownHosts, []byte(strings.TrimSpace(other.KnownHostsLine())), 0o600); err != nil {
		t.Fatal(err)
	}
	mustDial(t, f.hc, DialOptions{AcceptNew: true})
	data, err := os.ReadFile(f.knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 2 {
		t.Errorf("known_hosts has %d lines, want 2:\n%s", len(lines), data)
	}
}

func TestDialHostKeyAlias(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	line := strings.Replace(f.srv.KnownHostsLine(), fmt.Sprintf("[%s]:%d", f.srv.Host(), f.srv.Port()), "devbox-key", 1)
	if err := os.WriteFile(f.knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.hc.HostKeyAlias = "devbox-key"
	f.hc.StrictHostKeyChecking = "yes"
	mustDial(t, f.hc, DialOptions{})
}

func TestDialChangedKey(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	impostor, _ := ed25519Signer(t)
	line := fmt.Sprintf("[%s]:%d %s", f.srv.Host(), f.srv.Port(), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(impostor.PublicKey()))))
	if err := os.WriteFile(f.knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &fakePrompter{answer: true}
	_, err := Dial(t.Context(), f.hc, DialOptions{Prompter: p, AcceptNew: true, dialAgent: noAgent})
	if !errors.Is(err, ErrHostKey) {
		t.Fatalf("err = %v, want ErrHostKey", err)
	}
	if !strings.Contains(err.Error(), f.knownHosts+":1") || !strings.Contains(err.Error(), "ssh-keygen -R") {
		t.Errorf("message should name %s:1 and ssh-keygen -R: %v", f.knownHosts, err)
	}
	if len(p.questions()) != 0 {
		t.Error("a changed key must never be offered for confirmation")
	}
}

func TestDialKnownHostDifferentAlgo(t *testing.T) {
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecSigner, err := ssh.NewSignerFromKey(ecPriv)
	if err != nil {
		t.Fatal(err)
	}
	edSigner, _ := ed25519Signer(t)
	// x/crypto prefers ECDSA over ed25519, so without HostKeyAlgorithms the
	// server would present the ECDSA key and look like a changed key.
	f := newFixture(t, sshtest.Options{HostKey: ecSigner, ExtraHostKeys: []ssh.Signer{edSigner}})
	edLine := fmt.Sprintf("[%s]:%d %s", f.srv.Host(), f.srv.Port(), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(edSigner.PublicKey()))))
	if err := os.WriteFile(f.knownHosts, []byte(edLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.hc.StrictHostKeyChecking = "yes"
	mustDial(t, f.hc, DialOptions{})
}

func TestDialPromptOutlivesHandshakeTimeout(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	f.hc.KnownHostsFiles = []string{filepath.Join(t.TempDir(), "known_hosts")}
	f.hc.ConnectTimeout = 300 * time.Millisecond
	mustDial(t, f.hc, DialOptions{Prompter: &fakePrompter{answer: true, delay: time.Second}})
}

func TestDialContextCanceledDuringPrompt(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	f.hc.KnownHostsFiles = []string{filepath.Join(t.TempDir(), "known_hosts")}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err := Dial(ctx, f.hc, DialOptions{Prompter: &fakePrompter{answer: true, delay: time.Hour}, dialAgent: noAgent})
	if err == nil {
		t.Fatal("Dial succeeded after its context was canceled")
	}
}

func TestDialUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddrPort(ln.Addr().String())
	ln.Close()
	hc := &HostConfig{Alias: "gone", HostName: "127.0.0.1", Port: int(addr.Port()), User: "u", StrictHostKeyChecking: "ask", ConnectTimeout: 2 * time.Second}
	_, err = Dial(t.Context(), hc, DialOptions{dialAgent: noAgent})
	if !errors.Is(err, ErrConnect) {
		t.Fatalf("err = %v, want ErrConnect", err)
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("message should name the host: %v", err)
	}
}

func TestDialHandshakeTimeout(t *testing.T) {
	// A server that accepts TCP but never speaks SSH.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(io.Discard, c)
	}()
	port := int(netip.MustParseAddrPort(ln.Addr().String()).Port())
	hc := &HostConfig{Alias: "mute", HostName: "127.0.0.1", Port: port, User: "u", StrictHostKeyChecking: "ask", ConnectTimeout: 300 * time.Millisecond}
	start := time.Now()
	_, err = Dial(t.Context(), hc, DialOptions{dialAgent: noAgent})
	if !errors.Is(err, ErrConnect) {
		t.Fatalf("err = %v, want ErrConnect", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("handshake timeout took %v", time.Since(start))
	}
}

func TestDialProxyUnsupported(t *testing.T) {
	for _, hc := range []*HostConfig{
		{Alias: "a", HostName: "a", Port: 22, ProxyJump: "bastion"},
		{Alias: "a", HostName: "a", Port: 22, ProxyCommand: "nc %h %p"},
	} {
		_, err := Dial(t.Context(), hc, DialOptions{dialAgent: noAgent})
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "v0.3.0") {
			t.Errorf("err = %v, want ErrUnsupported naming v0.3.0", err)
		}
	}
}

func TestConnStartStreams(t *testing.T) {
	ended := make(chan struct{})
	f := newFixture(t, sshtest.Options{Exec: func(ctx context.Context, _ string, stdout io.Writer) int {
		for i := range 3 {
			_, _ = fmt.Fprintf(stdout, "line %d\n", i)
		}
		<-ctx.Done()
		close(ended)
		return 0
	}})
	c := mustDial(t, f.hc, DialOptions{})
	r, err := c.Start(t.Context(), "loop")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("line 0\nline 1\nline 2\n"))
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "line 0\nline 1\nline 2\n" {
		t.Errorf("read %q", buf)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the reader did not end the remote command")
	}
}

func TestConnStartStopsOnCancel(t *testing.T) {
	ended := make(chan struct{})
	f := newFixture(t, sshtest.Options{Exec: func(ctx context.Context, _ string, stdout io.Writer) int {
		_, _ = io.WriteString(stdout, "x")
		<-ctx.Done()
		close(ended)
		return 0
	}})
	c := mustDial(t, f.hc, DialOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	r, err := c.Start(ctx, "loop")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("canceling the context did not end the remote command")
	}
}

func TestConnDialContextAndWait(t *testing.T) {
	f := newFixture(t, sshtest.Options{DenyTCPForwarding: true})
	c := mustDial(t, f.hc, DialOptions{})
	if conn, err := c.DialContext(t.Context(), "tcp", "127.0.0.1:1"); err == nil {
		conn.Close()
		t.Fatal("DialContext succeeded on a server that denies forwarding")
	}
	waited := make(chan error, 1)
	go func() { waited <- c.Wait() }()
	c.Close()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after Close")
	}
}

// TestDialKnownHostsDevNull covers configs that keep no host key memory
// (UserKnownHostsFile /dev/null with StrictHostKeyChecking no, as
// `vagrant ssh-config` writes): every connection works and nothing is saved.
func TestDialKnownHostsDevNull(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	f.hc.KnownHostsFiles = []string{os.DevNull}
	f.hc.StrictHostKeyChecking = "no"
	mustDial(t, f.hc, DialOptions{})
	// A different server on a new port, as after `vagrant destroy && vagrant up`.
	g := newFixture(t, sshtest.Options{})
	g.hc.KnownHostsFiles = []string{os.DevNull}
	g.hc.StrictHostKeyChecking = "no"
	mustDial(t, g.hc, DialOptions{})
}
