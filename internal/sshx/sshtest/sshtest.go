// Package sshtest runs an in-process SSH server for tests. It supports
// publickey and password auth, exec requests answered by a Go handler, and
// direct-tcpip channels, so the SSH layer, the detector and the forwarders
// can be tested without a real sshd.
package sshtest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Options configures a test server. The zero value accepts no clients.
type Options struct {
	// AuthorizedKeys enables publickey auth for these keys.
	AuthorizedKeys []ssh.PublicKey
	// Password enables password auth when non-empty.
	Password string
	// HostKey is the server's host key; nil generates a fresh ed25519 key.
	HostKey ssh.Signer
	// ExtraHostKeys are offered in addition to HostKey, for example an RSA
	// key next to an ed25519 one.
	ExtraHostKeys []ssh.Signer
	// Exec answers exec requests. Its context ends when the client closes
	// the session or the server stops. Nil makes every command exit 127.
	Exec func(ctx context.Context, cmd string, stdout io.Writer) int
	// DenyTCPForwarding rejects direct-tcpip channels, like an sshd with
	// AllowTcpForwarding no.
	DenyTCPForwarding bool
}

// Server is a running test server. It stops, and waits for all of its
// goroutines, when the test that started it ends.
type Server struct {
	// Addr is the listening address, host:port.
	Addr string
	// HostKey is the server's primary host key.
	HostKey ssh.Signer

	opts   Options
	ln     net.Listener
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Start starts a server on 127.0.0.1 with a random port.
func Start(tb testing.TB, o Options) *Server {
	tb.Helper()
	hostKey := o.HostKey
	if hostKey == nil {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			tb.Fatalf("sshtest: generate host key: %v", err)
		}
		if hostKey, err = ssh.NewSignerFromKey(priv); err != nil {
			tb.Fatalf("sshtest: host key signer: %v", err)
		}
	}

	cfg := &ssh.ServerConfig{}
	if len(o.AuthorizedKeys) > 0 {
		cfg.PublicKeyCallback = func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			for _, k := range o.AuthorizedKeys {
				if bytes.Equal(k.Marshal(), key.Marshal()) {
					return nil, nil
				}
			}
			return nil, errors.New("sshtest: key not authorized")
		}
	}
	if o.Password != "" {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if subtle.ConstantTimeCompare(pass, []byte(o.Password)) == 1 {
				return nil, nil
			}
			return nil, errors.New("sshtest: wrong password")
		}
	}
	cfg.AddHostKey(hostKey)
	for _, k := range o.ExtraHostKeys {
		cfg.AddHostKey(k)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("sshtest: listen: %v", err)
	}
	ctx, cancel := context.WithCancel(tb.Context())
	s := &Server{
		Addr:    ln.Addr().String(),
		HostKey: hostKey,
		opts:    o,
		ln:      ln,
		ctx:     ctx,
		cancel:  cancel,
		conns:   make(map[net.Conn]struct{}),
	}
	s.wg.Add(1)
	go s.acceptLoop(cfg)
	tb.Cleanup(s.stop)
	return s
}

// Host returns the server's IP address.
func (s *Server) Host() string {
	return netip.MustParseAddrPort(s.Addr).Addr().String()
}

// Port returns the server's port.
func (s *Server) Port() int {
	return int(netip.MustParseAddrPort(s.Addr).Port())
}

// KnownHostsLine returns a known_hosts line that trusts the primary host key.
func (s *Server) KnownHostsLine() string {
	return knownhosts.Line([]string{knownhosts.Normalize(s.Addr)}, s.HostKey.PublicKey())
}

func (s *Server) stop() {
	s.cancel()
	s.ln.Close()
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) acceptLoop(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[nc] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serveConn(nc, cfg)
	}
}

func (s *Server) serveConn(nc net.Conn, cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	defer func() {
		nc.Close()
		s.mu.Lock()
		delete(s.conns, nc)
		s.mu.Unlock()
	}()

	sconn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(req.Type == "keepalive@openssh.com", nil)
			}
		}
	}()

	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			s.wg.Add(1)
			go s.handleSession(nch)
		case "direct-tcpip":
			if s.opts.DenyTCPForwarding {
				_ = nch.Reject(ssh.Prohibited, "tcp forwarding is disabled")
				continue
			}
			s.wg.Add(1)
			go s.handleDirectTCPIP(nch)
		default:
			_ = nch.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

func (s *Server) handleSession(nch ssh.NewChannel) {
	defer s.wg.Done()
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	// reqs closes when the client closes the channel, which ends the handler's context.
	defer cancel()

	started := false
	for req := range reqs {
		if req.Type != "exec" || started {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			continue
		}
		started = true
		_ = req.Reply(true, nil)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			code := 127
			if s.opts.Exec != nil {
				code = s.opts.Exec(ctx, payload.Command, ch)
			}
			_ = ch.CloseWrite()
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
			ch.Close()
		}()
	}
}

func (s *Server) handleDirectTCPIP(nch ssh.NewChannel) {
	defer s.wg.Done()
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &p); err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
		return
	}
	var d net.Dialer
	target, err := d.DialContext(s.ctx, "tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
	if err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	defer target.Close()
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	stop := context.AfterFunc(s.ctx, func() {
		target.Close()
		ch.Close()
	})
	defer stop()

	discarded := make(chan struct{})
	go func() {
		defer close(discarded)
		ssh.DiscardRequests(reqs)
	}()
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(target, ch)
		if tc, ok := target.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(ch, target)
		_ = ch.CloseWrite()
	}()
	copies.Wait()
	// The request stream only ends when the channel closes.
	ch.Close()
	<-discarded
}
