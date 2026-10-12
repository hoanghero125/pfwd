package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// defaultConnectTimeout applies when the ssh config sets no ConnectTimeout.
const defaultConnectTimeout = 10 * time.Second

// DialOptions configures Dial.
type DialOptions struct {
	// Prompter is asked about unknown host keys. Nil refuses them unless
	// AcceptNew or StrictHostKeyChecking accept-new/no applies.
	Prompter HostKeyPrompter
	// AcceptNew trusts unknown host keys without asking, like
	// StrictHostKeyChecking accept-new. Changed keys are still refused.
	AcceptNew bool
	// Logger receives debug details; nil discards them.
	Logger *slog.Logger
	// Keepalive sends keepalive@openssh.com requests and drops the
	// connection when they go unanswered; the zero value turns it off.
	Keepalive Keepalive

	// Resolve looks up a ProxyJump hop ("host" or "user@host") in the ssh
	// config. Without it, a host with ProxyJump can't be reached.
	Resolve func(ctx context.Context, target string) (*HostConfig, error)

	// dialAgent replaces openAgent in tests.
	dialAgent func(*HostConfig) (io.ReadWriteCloser, error)
}

// Conn is an authenticated SSH connection. Close it when done, even after
// Wait has returned: Close also stops its keepalive goroutine.
type Conn struct {
	client *ssh.Client
	alias  string // for error messages
	log    *slog.Logger

	kaCancel context.CancelFunc
	kaWG     sync.WaitGroup
	// reason says why pfwd dropped the connection, for Wait.
	reason atomic.Pointer[error]
	// under are the jump host connections this one runs through.
	under []io.Closer
}

// Dial connects to the host, verifies its host key and authenticates. It
// goes through the host's ProxyJump hops or ProxyCommand when it has them.
func Dial(ctx context.Context, hc *HostConfig, o DialOptions) (*Conn, error) {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	hops := 0
	return dialHost(ctx, hc, o, &hops, nil)
}

// dialHost connects to hc over its transport (a channel through via when
// via is set) and runs the SSH handshake. hops counts the connections this
// Dial has made.
func dialHost(ctx context.Context, hc *HostConfig, o DialOptions, hops *int, via *Conn) (*Conn, error) {
	*hops++
	if *hops > maxHops {
		return nil, &Error{Kind: ErrConnect, Msg: fmt.Sprintf(
			"reaching %s takes more than %d hops; check ProxyJump in ~/.ssh/config for a loop", hc.Alias, maxHops)}
	}
	log := o.Logger
	timeout := hc.ConnectTimeout
	if timeout <= 0 {
		timeout = defaultConnectTimeout
	}

	checker, err := newHostKeyChecker(hc, o.Prompter, o.AcceptNew, log)
	if err != nil {
		return nil, &Error{Kind: ErrHostKey, Err: err, Msg: err.Error()}
	}

	openAgentFn := o.dialAgent
	if openAgentFn == nil {
		openAgentFn = openAgent
	}
	var agentKeys []ssh.Signer
	if hc.IdentityAgent != "none" {
		if conn, err := openAgentFn(hc); err != nil {
			log.Debug("no ssh-agent", "err", err)
		} else {
			// The agent signs during the handshake, so it stays open until Dial returns.
			defer conn.Close()
			if agentKeys, err = agent.NewClient(conn).Signers(); err != nil {
				log.Debug("ssh-agent did not list keys", "err", err)
			}
		}
	}
	files, certs, notes := loadKeyFiles(hc.IdentityFiles)
	certFiles, certNotes := loadCertFiles(hc.CertificateFiles)
	notes = append(notes, certNotes...)
	signers, moreNotes := orderSigners(hc, agentKeys, files, append(certs, certFiles...))
	notes = append(notes, moreNotes...)
	log.Debug("offering keys", "host", hc.Alias, "agent_keys", len(agentKeys), "key_files", len(files), "offered", len(signers))

	addr := net.JoinHostPort(hc.HostName, strconv.Itoa(hc.Port))
	nc, under, err := transport(ctx, hc, o, hops, via, timeout)
	if err != nil {
		return nil, err
	}

	// The handshake clock pauses while the user reads the host key prompt.
	clock := startClock(nc, timeout)
	checker.beforePrompt = clock.pause
	checker.afterPrompt = clock.resume
	stop := context.AfterFunc(ctx, func() { nc.Close() })

	cfg := &ssh.ClientConfig{
		User: hc.User,
		// Password and keyboard-interactive are offered only to learn that
		// the server wants them: their callbacks end the handshake with a
		// marker error instead of prompting (prompts arrive in v0.3.0).
		Auth: []ssh.AuthMethod{
			ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
				return signers, nil
			}),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				if len(questions) == 0 {
					return nil, nil // an informational round; answering it is free
				}
				return nil, errWantsCode
			}),
			ssh.PasswordCallback(func() (string, error) {
				return "", errWantsPassword
			}),
		},
		HostKeyCallback:   checker.callback(ctx),
		HostKeyAlgorithms: checker.algorithms(),
	}
	sc, chans, reqs, err := ssh.NewClientConn(nc, addr, cfg)
	clock.stop()
	if !stop() {
		// ctx was canceled and closed the connection.
		if err == nil {
			sc.Close()
		}
		closeAll(under)
		return nil, ctx.Err()
	}
	if err != nil {
		nc.Close()
		closeAll(under)
		err = handshakeError(hc, addr, err, len(signers), notes)
		if pc, ok := nc.(*procConn); ok {
			err = withProxyOutput(err, pc.said())
		}
		return nil, err
	}
	c := &Conn{client: ssh.NewClient(sc, chans, reqs), alias: hc.Alias, log: log, under: under}
	if o.Keepalive.Interval > 0 {
		var kctx context.Context
		kctx, c.kaCancel = context.WithCancel(context.Background())
		c.kaWG.Add(1)
		go c.keepalive(kctx, o.Keepalive)
	}
	return c, nil
}

// withProxyOutput adds what the ProxyCommand printed to a handshake error.
func withProxyOutput(err error, said string) error {
	if said == "" {
		return err
	}
	var e *Error
	if errors.As(err, &e) {
		return &Error{Kind: e.Kind, Err: e.Err, Msg: e.Msg + ". The ProxyCommand said: " + said}
	}
	return fmt.Errorf("%w; the ProxyCommand said: %s", err, said)
}

// handshakeClock bounds the SSH handshake: with a deadline on conns that
// take one, else with a timer that closes the conn (SSH channels and
// Windows pipes have no deadlines). It pauses while the user answers a
// prompt.
type handshakeClock struct {
	nc      net.Conn
	timeout time.Duration
	timer   *time.Timer // nil when nc takes deadlines
}

func startClock(nc net.Conn, timeout time.Duration) *handshakeClock {
	c := &handshakeClock{nc: nc, timeout: timeout}
	if nc.SetDeadline(time.Now().Add(timeout)) != nil {
		c.timer = time.AfterFunc(timeout, func() { _ = nc.Close() })
	}
	return c
}

func (c *handshakeClock) pause() {
	if c.timer != nil {
		c.timer.Stop()
		return
	}
	_ = c.nc.SetDeadline(time.Time{})
}

func (c *handshakeClock) resume() {
	if c.timer != nil {
		c.timer.Reset(c.timeout)
		return
	}
	_ = c.nc.SetDeadline(time.Now().Add(c.timeout))
}

func (c *handshakeClock) stop() { c.pause() }

// Marker errors from the password and keyboard-interactive callbacks.
var (
	errWantsPassword = errors.New("the server asks for a password")
	errWantsCode     = errors.New("the server asks for keyboard-interactive input")
)

func handshakeError(hc *HostConfig, addr string, err error, offered int, notes []string) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, errWantsPassword) || errors.Is(err, errWantsCode) {
		what := "a password"
		if errors.Is(err, errWantsCode) {
			what = "a password or one-time code (keyboard-interactive login)"
		}
		return &Error{Kind: ErrAuth, Err: err, Msg: fmt.Sprintf(
			"logging in to %s as %s needs %s, which pfwd supports from v0.3.0. For now, use a host that accepts your key alone; `ssh-copy-id %s` installs your public key there",
			hc.Alias, hc.User, what, hc.Alias)}
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		var b strings.Builder
		if offered == 0 {
			fmt.Fprintf(&b, "authentication to %s as %s failed: no usable key was found in ssh-agent or the key files", hc.Alias, hc.User)
		} else {
			fmt.Fprintf(&b, "authentication to %s as %s failed: the server accepted none of the %d keys offered", hc.Alias, hc.User, offered)
		}
		for _, n := range notes {
			b.WriteString("; ")
			b.WriteString(n)
		}
		fmt.Fprintf(&b, ". Add your key to ssh-agent (`ssh-add <key>`) or set IdentityFile for %s in ~/.ssh/config, and check that `ssh %s` works", hc.Alias, hc.Alias)
		return &Error{Kind: ErrAuth, Err: err, Msg: b.String()}
	}
	return &Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
		"SSH handshake with %s (%s) failed: %v; check that `ssh %s` works", hc.Alias, addr, err, hc.Alias)}
}

// DialContext opens a direct-tcpip channel to addr, as seen from the server.
// A server that refuses forwarding returns an error that matches
// ErrForwardingRefused.
func (c *Conn) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := c.client.DialContext(ctx, network, addr)
	if err == nil {
		return conn, nil
	}
	var refused *ssh.OpenChannelError
	if errors.As(err, &refused) && refused.Reason == ssh.Prohibited {
		return nil, &Error{Kind: ErrForwardingRefused, Err: err, Msg: fmt.Sprintf(
			"%s refuses TCP forwarding (AllowTcpForwarding or DisableForwarding in its sshd_config); ask the server's admin to allow it", c.alias)}
	}
	return nil, fmt.Errorf("%s could not connect to %s: %w; check that something still listens there", c.alias, addr, err)
}

// Output runs cmd on the server and returns its stdout. A non-zero exit
// status is returned as an *ssh.ExitError together with the output.
func (c *Conn) Output(ctx context.Context, cmd string) ([]byte, error) {
	s, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	var out cappedBuffer
	s.Stdout = &out
	err = s.Run(cmd)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out.buf.Bytes(), err
}

// maxOutput caps what Output keeps. The probe prints a few lines; a server
// that prints more must not make pfwd buffer without limit.
const maxOutput = 64 << 10

// cappedBuffer keeps the first maxOutput bytes and drains the rest, so the
// remote command can still finish.
type cappedBuffer struct{ buf bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// Start runs cmd on the server and returns its stdout. Closing the reader,
// or canceling ctx, ends the session.
func (c *Conn) Start(ctx context.Context, cmd string) (io.ReadCloser, error) {
	s, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	stdout, err := s.StdoutPipe()
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("session stdout: %w", err)
	}
	if err := s.Start(cmd); err != nil {
		s.Close()
		return nil, fmt.Errorf("start remote command: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { s.Close() })
	return &sessionReader{Reader: stdout, session: s, stop: stop}, nil
}

type sessionReader struct {
	io.Reader
	session *ssh.Session
	stop    func() bool
}

func (r *sessionReader) Close() error {
	r.stop()
	if err := r.session.Close(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Close closes the connection and waits for its keepalive goroutine.
func (c *Conn) Close() error {
	err := c.client.Close()
	if c.kaCancel != nil {
		c.kaCancel()
	}
	c.kaWG.Wait()
	closeAll(c.under)
	return err
}

// Wait blocks until the connection ends. When pfwd dropped it because
// keepalives went unanswered, the error says so.
func (c *Conn) Wait() error {
	err := c.client.Wait()
	if r := c.reason.Load(); r != nil {
		return *r
	}
	return err
}
