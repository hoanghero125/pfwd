package sshx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

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
