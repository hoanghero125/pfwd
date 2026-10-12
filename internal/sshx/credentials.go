package sshx

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// CredentialKind is what a credential prompt asks for.
type CredentialKind int

// Credential kinds.
const (
	// Passphrase unlocks an encrypted key file.
	Passphrase CredentialKind = iota
	// Password is the user's password on the server.
	Password
	// Code answers a keyboard-interactive question from the server, most
	// often a one-time code.
	Code
)

// CredentialQuestion is one credential prompt.
type CredentialQuestion struct {
	// Host is the alias being logged in to; a jump host's for a hop.
	Host string
	Kind CredentialKind
	// Prompt is the key file for Passphrase, the user for Password, and
	// the server's question for Code. A server's text is not cleaned.
	Prompt string
	// Instruction is the server's keyboard-interactive name and
	// instruction, if it sent any.
	Instruction string
	// Echo shows the answer as it is typed. Secrets never echo.
	Echo bool
	// Retry is set when the previous answer was wrong.
	Retry bool
}

// CredentialPrompter asks the user for a credential. A user who cancels
// gets ErrCanceled. The caller zeroes the returned slice after use.
type CredentialPrompter interface {
	AskCredential(ctx context.Context, q CredentialQuestion) ([]byte, error)
}

// ErrCanceled is returned by a CredentialPrompter when the user cancels.
var ErrCanceled = errors.New("canceled")

// maxTries is how many times a passphrase, password or code is asked for
// before giving up, like OpenSSH's NumberOfPasswordPrompts.
const maxTries = 3

// KeyCache keeps what the user typed to log in, in memory only, for the
// life of a host session, so reconnects don't ask again: decrypted keys
// and passwords. One-time codes are never kept. A nil KeyCache keeps
// nothing. Its methods are safe for concurrent use.
type KeyCache struct {
	mu        sync.Mutex
	signers   map[string]ssh.Signer // by key file path
	passwords map[string][]byte     // by passwordKey
}

// NewKeyCache returns an empty cache. Call Clear when the session ends.
func NewKeyCache() *KeyCache {
	return &KeyCache{signers: map[string]ssh.Signer{}, passwords: map[string][]byte{}}
}

// Clear zeroes the kept passwords and drops the decrypted keys.
func (k *KeyCache) Clear() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for key, pw := range k.passwords {
		clear(pw)
		delete(k.passwords, key)
	}
	clear(k.signers)
}

func (k *KeyCache) signer(path string) ssh.Signer {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.signers[path]
}

func (k *KeyCache) setSigner(path string, s ssh.Signer) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.signers[path] = s
}

// password returns a copy of the kept password, or nil.
func (k *KeyCache) password(key string) []byte {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if pw, ok := k.passwords[key]; ok {
		return append([]byte(nil), pw...)
	}
	return nil
}

// setPassword keeps a copy of pw.
func (k *KeyCache) setPassword(key string, pw []byte) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	clear(k.passwords[key])
	k.passwords[key] = append([]byte(nil), pw...)
}

func (k *KeyCache) forgetPassword(key string) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	clear(k.passwords[key])
	delete(k.passwords, key)
}

// passwordKey names a login: user, host and port.
func passwordKey(hc *HostConfig) string {
	return hc.User + "@" + net.JoinHostPort(hc.HostName, strconv.Itoa(hc.Port))
}

// passphraseError is three wrong passphrases for a key file.
type passphraseError struct{ path string }

func (e *passphraseError) Error() string {
	return "wrong passphrase for " + e.path
}

// asker asks the user for credentials during one handshake, pausing the
// handshake clock while they type.
type asker struct {
	ctx    context.Context
	hc     *HostConfig
	p      CredentialPrompter
	keys   *KeyCache
	pause  func()
	resume func()

	// password is the last password typed, kept once the login succeeds.
	password []byte
	// usedKept is set when the kept password was sent in this login.
	usedKept bool
	// triedPassword and triedCode say which methods were attempted, for
	// the error message.
	triedPassword, triedCode bool
	// canceled is set once the user cancels a prompt: x/crypto moves on to
	// the next method after an error, so every later prompt is refused
	// without asking, and the login ends.
	canceled bool
	// lastQuestions are the previous keyboard-interactive round's
	// questions; the same ones again mean the answer was wrong.
	lastQuestions []string
}

func (a *asker) ask(q CredentialQuestion) ([]byte, error) {
	if a.canceled {
		return nil, ErrCanceled
	}
	q.Host = a.hc.Alias
	a.pause()
	defer a.resume()
	answer, err := a.p.AskCredential(a.ctx, q)
	if errors.Is(err, ErrCanceled) {
		a.canceled = true
	}
	return answer, err
}

// lazySigner is an encrypted key whose passphrase is asked for only when
// the server accepts its public key, which x/crypto checks before it signs.
type lazySigner struct {
	path string
	pub  ssh.PublicKey
	a    *asker
}

func (s *lazySigner) PublicKey() ssh.PublicKey { return s.pub }

func (s *lazySigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	signer, err := s.unlock()
	if err != nil {
		return nil, err
	}
	return signer.Sign(rand, data)
}

func (s *lazySigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	signer, err := s.unlock()
	if err != nil {
		return nil, err
	}
	as, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		return nil, fmt.Errorf("%s can't sign with %s", s.path, algorithm)
	}
	return as.SignWithAlgorithm(rand, data, algorithm)
}

// unlock returns the decrypted key, from the cache or by asking for the
// passphrase up to maxTries times.
func (s *lazySigner) unlock() (ssh.Signer, error) {
	if signer := s.a.keys.signer(s.path); signer != nil {
		return signer, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	for try := range maxTries {
		pass, err := s.a.ask(CredentialQuestion{Kind: Passphrase, Prompt: s.path, Retry: try > 0})
		if err != nil {
			return nil, err
		}
		signer, err := ssh.ParsePrivateKeyWithPassphrase(data, pass)
		clear(pass)
		if err == nil {
			s.a.keys.setSigner(s.path, signer)
			return signer, nil
		}
		if !errors.Is(err, x509.IncorrectPasswordError) {
			return nil, fmt.Errorf("unlock %s: %w", s.path, err)
		}
	}
	return nil, &passphraseError{path: s.path}
}

// passwordCallback returns the kept password first, then asks.
func (a *asker) passwordCallback() func() (string, error) {
	key := passwordKey(a.hc)
	tries := 0
	return func() (string, error) {
		a.triedPassword = true
		if tries == 0 {
			if pw := a.keys.password(key); pw != nil {
				tries++
				a.usedKept = true
				defer clear(pw)
				a.setLast(pw)
				return string(pw), nil
			}
		} else {
			// The last password failed.
			a.keys.forgetPassword(key)
		}
		pw, err := a.ask(CredentialQuestion{Kind: Password, Prompt: a.hc.User, Retry: tries > 0})
		tries++
		if err != nil {
			return "", err
		}
		defer clear(pw)
		a.setLast(pw)
		return string(pw), nil
	}
}

func (a *asker) setLast(pw []byte) {
	clear(a.password)
	a.password = append([]byte(nil), pw...)
}

// codeCallback answers keyboard-interactive questions one by one. A round
// that repeats the previous round's questions is a retry: a PAM login that
// asks for the password and then a code is not.
func (a *asker) codeCallback() ssh.KeyboardInteractiveChallenge {
	return func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		if len(questions) == 0 {
			return nil, nil // an informational round; answering it is free
		}
		a.triedCode = true
		retry := slices.Equal(questions, a.lastQuestions)
		a.lastQuestions = slices.Clone(questions)
		info := instruction
		if name != "" {
			info = name + "\n" + instruction
		}
		answers := make([]string, len(questions))
		for i, q := range questions {
			reply, err := a.ask(CredentialQuestion{Kind: Code, Prompt: q, Instruction: info, Echo: i < len(echos) && echos[i], Retry: retry})
			if err != nil {
				return nil, err
			}
			answers[i] = string(reply)
			clear(reply)
		}
		return answers, nil
	}
}

// done keeps the password after a successful login and zeroes the rest. A
// kept password that the server refused is forgotten, so the next login
// asks for the new one; servers such as OpenSSH hang up at MaxAuthTries
// instead of asking again, so the password callback may never learn it.
func (a *asker) done(err error) {
	switch {
	case err == nil && a.password != nil:
		a.keys.setPassword(passwordKey(a.hc), a.password)
	case err != nil && a.usedKept && authRefused(err):
		a.keys.forgetPassword(passwordKey(a.hc))
	}
	clear(a.password)
	a.password = nil
}

// authRefused reports whether a handshake error means the server refused
// the login, rather than the connection failing.
func authRefused(err error) bool {
	s := err.Error()
	return strings.Contains(s, "unable to authenticate") || strings.Contains(s, "too many authentication failures")
}
