package sshx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// fakeAsker answers credential prompts from a script; past its end it
// repeats the last answer. A nil answer cancels.
type fakeAsker struct {
	mu      sync.Mutex
	answers []string
	delay   time.Duration
	asked   []CredentialQuestion
}

func (a *fakeAsker) AskCredential(ctx context.Context, q CredentialQuestion) ([]byte, error) {
	a.mu.Lock()
	a.asked = append(a.asked, q)
	n := min(len(a.asked), len(a.answers)) - 1
	answer := a.answers[n]
	a.mu.Unlock()
	select {
	case <-time.After(a.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if answer == "<cancel>" {
		return nil, ErrCanceled
	}
	return []byte(answer), nil
}

func (a *fakeAsker) questions() []CredentialQuestion {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]CredentialQuestion(nil), a.asked...)
}

// encryptedKeyServer starts a server that only trusts an encrypted key and
// returns a config that offers it.
func encryptedKeyServer(t *testing.T, passphrase string) (*HostConfig, string) {
	t.Helper()
	dir := t.TempDir()
	path, signer := writeKey(t, dir, "id_locked", passphrase)
	hc := startKeylessServer(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{signer.PublicKey()}})
	hc.IdentityFiles = []string{path}
	return hc, path
}

func TestDialAsksForPassphraseOnce(t *testing.T) {
	hc, path := encryptedKeyServer(t, "hunter2")
	ask := &fakeAsker{answers: []string{"hunter2"}}
	keys := NewKeyCache()
	defer keys.Clear()
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	qs := ask.questions()
	if len(qs) != 1 {
		t.Fatalf("asked %d times, want once: the decrypted key is kept for reconnects", len(qs))
	}
	if q := qs[0]; q.Kind != Passphrase || q.Prompt != path || q.Host != "uni" || q.Echo {
		t.Errorf("question = %+v", q)
	}
}

func TestDialWrongPassphraseAsksAgain(t *testing.T) {
	hc, _ := encryptedKeyServer(t, "hunter2")
	ask := &fakeAsker{answers: []string{"wrong", "hunter2"}}
	mustDial(t, hc, DialOptions{Credentials: ask})
	qs := ask.questions()
	if len(qs) != 2 || qs[0].Retry || !qs[1].Retry {
		t.Errorf("questions = %+v, want two, the second marked Retry", qs)
	}
}

func TestDialThreeWrongPassphrases(t *testing.T) {
	hc, path := encryptedKeyServer(t, "hunter2")
	ask := &fakeAsker{answers: []string{"wrong"}}
	_, err := Dial(t.Context(), hc, DialOptions{Credentials: ask, dialAgent: noAgent})
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("err = %v, want ErrAuth naming the key and the passphrase", err)
	}
	if n := len(ask.questions()); n != 3 {
		t.Errorf("asked %d times, want 3", n)
	}
}

func TestDialDoesNotAskForUnacceptedKey(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	enc, _ := writeKey(t, t.TempDir(), "id_locked", "hunter2")
	f.hc.IdentityFiles = append([]string{enc}, f.hc.IdentityFiles...)
	ask := &fakeAsker{answers: []string{"hunter2"}}
	mustDial(t, f.hc, DialOptions{Credentials: ask})
	if n := len(ask.questions()); n != 0 {
		t.Errorf("asked %d times for a key the server doesn't accept", n)
	}
}

func TestDialAsksForPassword(t *testing.T) {
	hc := startKeylessServer(t, sshtest.Options{Password: "hunter2"})
	ask := &fakeAsker{answers: []string{"hunter2"}}
	keys := NewKeyCache()
	defer keys.Clear()
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	qs := ask.questions()
	if len(qs) != 1 {
		t.Fatalf("asked %d times, want once: the password is kept for reconnects", len(qs))
	}
	if q := qs[0]; q.Kind != Password || q.Prompt != "student" || q.Host != "uni" || q.Echo {
		t.Errorf("question = %+v", q)
	}
}

func TestDialStalePasswordAsksAgain(t *testing.T) {
	hc := startKeylessServer(t, sshtest.Options{Password: "hunter2"})
	keys := NewKeyCache()
	defer keys.Clear()
	keys.setPassword(passwordKey(hc), []byte("old password"))
	ask := &fakeAsker{answers: []string{"hunter2"}}
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	if n := len(ask.questions()); n != 1 {
		t.Errorf("asked %d times, want once after the kept password failed", n)
	}
}

func TestDialWrongPasswords(t *testing.T) {
	hc := startKeylessServer(t, sshtest.Options{Password: "hunter2"})
	ask := &fakeAsker{answers: []string{"wrong"}}
	_, err := Dial(t.Context(), hc, DialOptions{Credentials: ask, dialAgent: noAgent})
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "keys offered") {
		t.Fatalf("err = %v, want ErrAuth about the password", err)
	}
	if n := len(ask.questions()); n != 3 {
		t.Errorf("asked %d times, want 3", n)
	}
}

func TestDialAsksForCodeEveryTime(t *testing.T) {
	hc := startKeylessServer(t, sshtest.Options{OneTimeCode: "123456"})
	ask := &fakeAsker{answers: []string{"123456"}}
	keys := NewKeyCache()
	defer keys.Clear()
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	qs := ask.questions()
	if len(qs) != 2 {
		t.Fatalf("asked %d times, want twice: one-time codes are never kept", len(qs))
	}
	if q := qs[0]; q.Kind != Code || q.Prompt != "Verification code: " || q.Echo {
		t.Errorf("question = %+v", q)
	}
}

func TestDialKeyThenCodeAsks(t *testing.T) {
	f := newFixture(t, sshtest.Options{OneTimeCode: "123456", KeyThenCode: true})
	ask := &fakeAsker{answers: []string{"123456"}}
	mustDial(t, f.hc, DialOptions{Credentials: ask})
	if n := len(ask.questions()); n != 1 {
		t.Errorf("asked %d times, want once", n)
	}
}

func TestDialCanceledCredential(t *testing.T) {
	hc := startKeylessServer(t, sshtest.Options{Password: "hunter2"})
	ask := &fakeAsker{answers: []string{"<cancel>"}}
	_, err := Dial(t.Context(), hc, DialOptions{Credentials: ask, dialAgent: noAgent})
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err = %v, want ErrAuth saying the login was canceled", err)
	}
	if n := len(ask.questions()); n != 1 {
		t.Errorf("asked %d times after a cancel, want 1", n)
	}
}

func TestDialCredentialPromptOutlivesHandshakeTimeout(t *testing.T) {
	hc := startKeylessServer(t, sshtest.Options{Password: "hunter2"})
	hc.ConnectTimeout = 300 * time.Millisecond
	ask := &fakeAsker{answers: []string{"hunter2"}, delay: time.Second}
	mustDial(t, hc, DialOptions{Credentials: ask})
}

func TestKeyCacheClearZeroes(t *testing.T) {
	keys := NewKeyCache()
	keys.setPassword("k", []byte("hunter2"))
	stored := keys.passwords["k"]
	keys.Clear()
	if !bytes.Equal(stored, make([]byte, len(stored))) {
		t.Errorf("Clear left the password in memory: %q", stored)
	}
	if keys.password("k") != nil {
		t.Error("the password survived Clear")
	}
	var none *KeyCache
	none.Clear() // a nil cache keeps nothing and must not panic
	if none.password("k") != nil {
		t.Error("a nil cache returned a password")
	}
}

func TestCredentialsNeverLogged(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hc := startKeylessServer(t, sshtest.Options{Password: "pw-s3cret"})
	mustDial(t, hc, DialOptions{Credentials: &fakeAsker{answers: []string{"wrong-pw-s3cret", "pw-s3cret"}}, Logger: logger})
	enc, _ := encryptedKeyServer(t, "pp-s3cret")
	mustDial(t, enc, DialOptions{Credentials: &fakeAsker{answers: []string{"pp-s3cret"}}, Logger: logger})
	if strings.Contains(logs.String(), "s3cret") {
		t.Errorf("a secret reached the log:\n%s", logs.String())
	}
}

func TestDialWithoutPrompterNamesTheFix(t *testing.T) {
	hc := startKeylessServer(t, sshtest.Options{Password: "hunter2"})
	_, err := Dial(t.Context(), hc, DialOptions{dialAgent: noAgent})
	if !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "v0.3.0") || !strings.Contains(err.Error(), "password") {
		t.Errorf("err = %v, want ErrAuth about the password with no version promise", err)
	}
}

// startRawServer serves cfg on a free port until the test ends, rejecting
// every channel, and returns a config that trusts its host key.
func startRawServer(t *testing.T, cfg *ssh.ServerConfig) *HostConfig {
	t.Helper()
	hostSigner, _ := ed25519Signer(t)
	cfg.AddHostKey(hostSigner)
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
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			if sc, chans, reqs, err := ssh.NewServerConn(nc, cfg); err == nil {
				sc.Close()
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					_ = nch.Reject(ssh.Prohibited, "no")
				}
			}
			nc.Close()
		}
	}()
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte(knownhosts.Line([]string{knownhosts.Normalize(ln.Addr().String())}, hostSigner.PublicKey())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return &HostConfig{Alias: "box", HostName: host, Port: p, User: "me", KnownHostsFiles: []string{kh}, StrictHostKeyChecking: "yes"}
}

func TestCancelEndsTheLogin(t *testing.T) {
	dir := t.TempDir()
	path, signer := writeKey(t, dir, "id_locked", "hunter2")
	hc := startKeylessServer(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{signer.PublicKey()}, Password: "pw", OneTimeCode: "123"})
	hc.IdentityFiles = []string{path}
	ask := &fakeAsker{answers: []string{"<cancel>"}}
	_, err := Dial(t.Context(), hc, DialOptions{Credentials: ask, dialAgent: noAgent})
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("err = %v, want ErrAuth saying the login was canceled", err)
	}
	if n := len(ask.questions()); n != 1 {
		t.Errorf("asked %d times after one cancel, want 1", n)
	}
}

// TestStalePasswordForgottenWhenServerHangsUp: OpenSSH disconnects at
// MaxAuthTries instead of asking again, so the kept password that failed
// must be dropped, and the failure must not be retried as a network error.
func TestStalePasswordForgottenWhenServerHangsUp(t *testing.T) {
	hc := startRawServer(t, &ssh.ServerConfig{
		MaxAuthTries: 1,
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == "new" {
				return nil, nil
			}
			return nil, errors.New("wrong")
		},
	})
	keys := NewKeyCache()
	defer keys.Clear()
	keys.setPassword(passwordKey(hc), []byte("old"))
	_, err := Dial(t.Context(), hc, DialOptions{Credentials: &fakeAsker{answers: []string{"new"}}, Keys: keys, dialAgent: noAgent})
	if err == nil {
		t.Fatal("the stale password logged in")
	}
	if !errors.Is(err, ErrAuth) {
		t.Errorf("err = %v, want ErrAuth so reconnects stop", err)
	}
	if keys.password(passwordKey(hc)) != nil {
		t.Error("the password the server rejected is still kept")
	}
	ask := &fakeAsker{answers: []string{"new"}}
	mustDial(t, hc, DialOptions{Credentials: ask, Keys: keys})
	if n := len(ask.questions()); n != 1 {
		t.Errorf("asked %d times on the next try, want 1", n)
	}
}

// TestPasswordThenCodeIsNotARetry: a PAM login asks for the password, then
// a code; the code prompt must not say the password failed.
func TestPasswordThenCodeIsNotARetry(t *testing.T) {
	hc := startRawServer(t, &ssh.ServerConfig{
		KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			if a, err := ch("", "", []string{"Password: "}, []bool{false}); err != nil || len(a) != 1 || a[0] != "pw" {
				return nil, errors.New("wrong")
			}
			if a, err := ch("", "", []string{"Verification code: "}, []bool{false}); err != nil || len(a) != 1 || a[0] != "123" {
				return nil, errors.New("wrong")
			}
			return nil, nil
		},
	})
	ask := &fakeAsker{answers: []string{"pw", "123"}}
	mustDial(t, hc, DialOptions{Credentials: ask})
	for _, q := range ask.questions() {
		if q.Retry {
			t.Errorf("%q is marked as a retry after a correct answer", q.Prompt)
		}
	}
}
