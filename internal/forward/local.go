package forward

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Dialer opens connections on the far side of the tunnel. *sshx.Conn and
// *net.Dialer implement it.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// defaultMaxConns caps open connections per forward, to protect the SSH
// connection they share.
const defaultMaxConns = 256

// dialTimeout bounds opening the channel for one accepted connection.
const dialTimeout = 30 * time.Second

// Options configures a Local forward.
type Options struct {
	// MaxConns caps concurrent connections; 0 means 256. Connections past
	// the cap wait in the listen backlog until one closes.
	MaxConns int
	// Logger receives debug details; nil discards them.
	Logger *slog.Logger
	// OnDialError, if set, is called with every failed dial to the target,
	// from the goroutine serving that connection. It must not block.
	OnDialError func(error)
	// Handshake, if set, decides each connection's target (SOCKS5) instead
	// of the forward's fixed one.
	Handshake Handshake
	// HandshakeTimeout bounds a Handshake; 0 means 10 s.
	HandshakeTimeout time.Duration
}

// defaultHandshakeTimeout bounds a client that connects and stalls.
const defaultHandshakeTimeout = 10 * time.Second

// Stats are a forward's counters. BytesIn flows from the server to local
// clients, BytesOut from local clients to the server.
type Stats struct {
	Conns      int64
	TotalConns int64
	BytesIn    int64
	BytesOut   int64
	// LastActive is the last time bytes moved; zero if they never did.
	LastActive time.Time
}

// Local forwards connections accepted on a local listener to target,
// dialed through a Dialer.
type Local struct {
	ln     net.Listener
	dialer Dialer
	target string
	sem    chan struct{}
	log    *slog.Logger
	onFail func(error)

	handshake        Handshake
	handshakeTimeout time.Duration

	conns, total, in, out, last atomic.Int64

	mu   sync.Mutex
	open map[net.Conn]struct{}
	wg   sync.WaitGroup
}

// NewLocal returns a forward from ln to target. Call Serve to run it.
func NewLocal(ln net.Listener, d Dialer, target string, o Options) *Local {
	if o.MaxConns <= 0 {
		o.MaxConns = defaultMaxConns
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.HandshakeTimeout <= 0 {
		o.HandshakeTimeout = defaultHandshakeTimeout
	}
	return &Local{
		handshake:        o.Handshake,
		handshakeTimeout: o.HandshakeTimeout,
		ln:               ln,
		dialer:           d,
		target:           target,
		sem:              make(chan struct{}, o.MaxConns),
		log:              o.Logger,
		onFail:           o.OnDialError,
		open:             make(map[net.Conn]struct{}),
	}
}

// Serve accepts connections until ctx ends or the listener fails. Before it
// returns it closes the listener and every open connection, and waits for
// their goroutines. It returns nil when ctx ended.
func (l *Local) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = l.ln.Close() })
	defer stop()
	err := l.acceptLoop(ctx)
	_ = l.ln.Close()
	l.mu.Lock()
	for c := range l.open {
		_ = c.Close()
	}
	l.open = nil // track refuses connections from now on
	l.mu.Unlock()
	l.wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (l *Local) acceptLoop(ctx context.Context) error {
	for {
		select {
		case l.sem <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		c, err := l.ln.Accept()
		if err != nil {
			<-l.sem
			return err
		}
		if !l.track(c) {
			// Serve is shutting down.
			_ = c.Close()
			<-l.sem
			return nil
		}
		l.wg.Add(1)
		go l.handle(ctx, c)
	}
}

// track records an open connection so Serve can close it; it reports false
// once Serve has started closing everything.
func (l *Local) track(c net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open == nil {
		return false
	}
	l.open[c] = struct{}{}
	return true
}

func (l *Local) untrack(c net.Conn) {
	l.mu.Lock()
	delete(l.open, c)
	l.mu.Unlock()
	_ = c.Close()
}

func (l *Local) handle(ctx context.Context, client net.Conn) {
	defer l.wg.Done()
	defer func() { <-l.sem }()
	defer l.untrack(client)
	l.conns.Add(1)
	l.total.Add(1)
	defer l.conns.Add(-1)

	target := l.target
	var reply func(error) error
	if l.handshake != nil {
		_ = client.SetDeadline(time.Now().Add(l.handshakeTimeout))
		t, r, err := l.handshake(client)
		if err != nil {
			l.log.Debug("forward handshake failed", "err", err)
			return
		}
		target, reply = t, r
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	remote, err := l.dialer.DialContext(dialCtx, "tcp", target)
	cancel()
	if reply != nil {
		if rerr := reply(err); rerr != nil && err == nil {
			_ = remote.Close()
			return
		}
		_ = client.SetDeadline(time.Time{})
	}
	if err != nil {
		l.log.Info("forward dial failed", "target", target, "err", err)
		if l.onFail != nil {
			l.onFail(err)
		}
		return
	}
	if !l.track(remote) {
		_ = remote.Close()
		return
	}
	defer l.untrack(remote)
	l.pipe(client, remote)
}

// pipe copies both ways. When one direction ends it half-closes the other
// side, so request/response protocols that signal "done" with EOF work, and
// it returns once both directions have ended.
func (l *Local) pipe(client, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&countingWriter{w: remote, n: &l.out, last: &l.last}, client)
		closeWrite(remote)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&countingWriter{w: client, n: &l.in, last: &l.last}, remote)
		closeWrite(client)
	}()
	wg.Wait()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		if err := cw.CloseWrite(); err == nil {
			return
		}
	}
	_ = c.Close()
}

// Stats returns the forward's counters.
func (l *Local) Stats() Stats {
	s := Stats{
		Conns:      l.conns.Load(),
		TotalConns: l.total.Load(),
		BytesIn:    l.in.Load(),
		BytesOut:   l.out.Load(),
	}
	if last := l.last.Load(); last != 0 {
		s.LastActive = time.Unix(0, last)
	}
	return s
}

type countingWriter struct {
	w    io.Writer
	n    *atomic.Int64
	last *atomic.Int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.n.Add(int64(n))
		c.last.Store(time.Now().UnixNano())
	}
	return n, err
}
