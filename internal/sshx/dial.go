package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
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

	// dialAgent replaces openAgent in tests.
	dialAgent func(*HostConfig) (io.ReadWriteCloser, error)
}

// Conn is an authenticated SSH connection.
type Conn struct {
	client *ssh.Client
}

// Dial connects to the host, verifies its host key and authenticates.
func Dial(ctx context.Context, hc *HostConfig, o DialOptions) (*Conn, error) {
	if hc.ProxyJump != "" {
		return nil, unsupported(hc.Alias, "ProxyJump")
	}
	if hc.ProxyCommand != "" {
		return nil, unsupported(hc.Alias, "ProxyCommand")
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	timeout := hc.ConnectTimeout
	if timeout <= 0 {
		timeout = defaultConnectTimeout
	}

	checker, err := newHostKeyChecker(hc, o.Prompter, o.AcceptNew)
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
	files, notes := loadKeyFiles(hc.IdentityFiles)
	signers, moreNotes := orderSigners(hc, agentKeys, files)
	notes = append(notes, moreNotes...)
	log.Debug("offering keys", "host", hc.Alias, "agent_keys", len(agentKeys), "key_files", len(files), "offered", len(signers))

	addr := net.JoinHostPort(hc.HostName, strconv.Itoa(hc.Port))
	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	nc, err := d.DialContext(dialCtx, "tcp", addr)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
			"cannot reach %s (%s): %v; check the host name and port, and that `ssh %s` works", hc.Alias, addr, err, hc.Alias)}
	}

	// The handshake deadline pauses while the user reads the host key prompt.
	resetDeadline := func() { _ = nc.SetDeadline(time.Now().Add(timeout)) }
	resetDeadline()
	checker.beforePrompt = func() { _ = nc.SetDeadline(time.Time{}) }
	checker.afterPrompt = resetDeadline
	stop := context.AfterFunc(ctx, func() { nc.Close() })

	cfg := &ssh.ClientConfig{
		User: hc.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			return signers, nil
		})},
		HostKeyCallback:   checker.callback(ctx),
		HostKeyAlgorithms: checker.algorithms(),
	}
	sc, chans, reqs, err := ssh.NewClientConn(nc, addr, cfg)
	if !stop() {
		// ctx was canceled and closed the connection.
		if err == nil {
			sc.Close()
		}
		return nil, ctx.Err()
	}
	if err != nil {
		nc.Close()
		return nil, handshakeError(hc, addr, err, len(signers), notes)
	}
	_ = nc.SetDeadline(time.Time{})
	return &Conn{client: ssh.NewClient(sc, chans, reqs)}, nil
}

func unsupported(alias, option string) error {
	return &Error{Kind: ErrUnsupported, Msg: fmt.Sprintf(
		"%s uses %s, which pfwd does not support yet (planned for v0.3.0); connect to a host without it for now", alias, option)}
}

func handshakeError(hc *HostConfig, addr string, err error, offered int, notes []string) error {
	var e *Error
	if errors.As(err, &e) {
		return e
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
func (c *Conn) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return c.client.DialContext(ctx, network, addr)
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
	out, err := s.Output(cmd)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, err
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

// Close closes the connection.
func (c *Conn) Close() error {
	return c.client.Close()
}

// Wait blocks until the connection ends.
func (c *Conn) Wait() error {
	return c.client.Wait()
}
