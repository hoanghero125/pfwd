package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/state"
)

// Conn is an established connection to a host: it runs the detector's
// commands and dials forward targets. *sshx.Conn implements it.
type Conn interface {
	detect.Runner
	forward.Dialer
	// Listen asks the server to listen and forward connections here (-R).
	Listen(bind string, port uint16) (net.Listener, error)
	Close() error
	// Wait blocks until the connection ends.
	Wait() error
}

// DialFunc connects to a host, asking p about unknown host keys and for
// credentials.
type DialFunc func(ctx context.Context, host HostID, p Prompts) (Conn, error)

// Options configures an Engine.
type Options struct {
	Dial DialFunc
	// Interval is the detection polling interval; 0 means 2 s.
	Interval time.Duration
	// Logger receives debug details; nil discards them.
	Logger *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Debounce is how many snapshots a new port must appear in before it
	// is auto-forwarded; 0 means 2.
	Debounce int
	// Settings returns a host's auto-forward settings; nil means
	// DefaultSettings for every host.
	Settings func(HostID) HostSettings
	// Labels are user labels by port; they win over the built-in ones.
	Labels map[uint16]string
	// HTTPProbe checks each new forward for HTTP or HTTPS.
	HTTPProbe bool
	// OnConflict is what auto-forward does when the local port is taken:
	// ConflictNext or ConflictAsk (the default) use a free port nearby,
	// ConflictRandom any free port, ConflictFail gives up.
	OnConflict string
	// BindAddr is the default local address; "" means 127.0.0.1.
	BindAddr string
	// MaxConns caps open connections per forward; 0 means 256.
	MaxConns int
	// Notifier, when set, is told about auto-forwards: one notification
	// for those made within NotifyDelay of each other.
	Notifier Notifier
	// NotifyDelay is how long a notification waits for more auto-forwards
	// to join it; 0 means 2 s.
	NotifyDelay time.Duration
	// MaxBackoff caps the wait between reconnect attempts; 0 means 30 s.
	MaxBackoff time.Duration
	// Backoff returns the wait before reconnect attempt n (from 1); nil
	// means 0.5 s doubling up to MaxBackoff, with jitter.
	Backoff func(attempt int) time.Duration
	// ConnWait is how long a connection accepted while reconnecting waits
	// for the host; 0 means 10 s.
	ConnWait time.Duration
	// AutoExpire removes an auto-forward whose port has been gone this long,
	// unless it is pinned; 0 means 10 minutes.
	AutoExpire time.Duration
	// State remembers forwards between runs and restores them on each
	// host's first connection; nil remembers nothing.
	State *state.Store
	// PrivilegedOffset moves a local port below 1024 that this user can't
	// bind to port+offset; 0 means 10000, and a negative value turns it off.
	PrivilegedOffset int
	// AllowLANSocks lets a SOCKS proxy listen on a non-loopback address.
	AllowLANSocks bool

	// listen replaces forward.Listen in tests.
	listen forward.ListenFunc
}

// shutdownTimeout bounds Close.
const shutdownTimeout = 3 * time.Second

// Engine owns sessions and forwards. Its methods are safe for concurrent
// use. Every goroutine it starts ends before Close returns.
type Engine struct {
	o      Options
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	events chan Event

	mu       sync.Mutex
	closed   bool
	sessions map[HostID]*session
	hosts    map[HostID]*hostState
	forwards map[string]*fwd
	nextID   int
	// notes are auto-forwards waiting to be announced, by host.
	notes map[HostID][]note

	// persistMu orders writes of state.json.
	persistMu sync.Mutex
}

type fwd struct {
	Forward
	local  *forward.Local
	cancel context.CancelFunc
	done   chan struct{}
	// want is the local port state.json keeps for a restored forward that
	// had to listen on another one this run; 0 means Spec.LocalPort.
	want uint16
	// base adds up the counters of a remote forward's earlier listeners.
	base forward.Stats
}

// New returns an Engine. Call Close when done with it.
func New(o Options) *Engine {
	if o.Interval <= 0 {
		o.Interval = 2 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Debounce <= 0 {
		o.Debounce = 2
	}
	if o.BindAddr == "" {
		o.BindAddr = "127.0.0.1"
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.Backoff == nil {
		o.Backoff = defaultBackoff(o.MaxBackoff, jitter)
	}
	if o.ConnWait <= 0 {
		o.ConnWait = 10 * time.Second
	}
	if o.AutoExpire <= 0 {
		o.AutoExpire = 10 * time.Minute
	}
	if o.NotifyDelay <= 0 {
		o.NotifyDelay = 2 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		o:        o,
		log:      o.Logger,
		ctx:      ctx,
		cancel:   cancel,
		events:   make(chan Event, 256),
		sessions: make(map[HostID]*session),
		hosts:    make(map[HostID]*hostState),
		forwards: make(map[string]*fwd),
		notes:    make(map[HostID][]note),
	}
}

// Events returns the event stream. It is closed by Close.
func (e *Engine) Events() <-chan Event {
	return e.events
}

func (e *Engine) emit(ev Event) {
	select {
	case <-e.ctx.Done():
		return
	default:
	}
	select {
	case e.events <- ev:
	case <-e.ctx.Done():
	}
}

// Forward creates a forward on a connected host. Local and Dynamic ones
// bind their local port before returning, so a busy port comes back as a
// *forward.PortInUseError; a Remote one has the server listen first, so a
// refusal comes back as an error.
func (e *Engine) Forward(host HostID, spec ForwardSpec) (Forward, error) {
	spec, err := e.normalize(spec)
	if err != nil {
		return Forward{}, err
	}
	conn, err := e.connOf(host)
	if err != nil {
		return Forward{}, err
	}
	if spec.Label == "" && spec.Kind == Local {
		e.mu.Lock()
		if p, ok := e.hostLocked(host).ports[spec.RemotePort]; ok {
			spec.Label = e.label(p)
		}
		e.mu.Unlock()
	}
	// A manual forward of what is already forwarded the same way (an
	// auto-forward the user hasn't seen yet) returns that forward. It is
	// checked again after a busy port, in case the other one won the race.
	if f := e.sameForward(host, spec); f != nil {
		return e.snapshot(f), nil
	}
	ln, port, err := e.listener(conn, spec)
	if err != nil {
		if f := e.sameForward(host, spec); f != nil {
			return e.snapshot(f), nil
		}
		return Forward{}, err
	}
	var mapped uint16
	if spec.Kind != Remote && port != spec.LocalPort {
		mapped, spec.LocalPort = spec.LocalPort, port
	}
	// f is set below, before Serve starts, so the dialer and the callback
	// always see it.
	var f *fwd
	dialer := &fwdDialer{e: e}
	local := e.newLocal(spec, ln, dialer, func(err error) { e.dialFailed(f, err) })

	e.mu.Lock()
	if e.closed || e.sessions[host] == nil || e.sessions[host].conn != conn {
		e.mu.Unlock()
		_ = ln.Close()
		return Forward{}, fmt.Errorf("the connection to %s ended; forward again after reconnecting", host)
	}
	hs := e.hostLocked(host)
	if spec.Auto && e.hasForwardLocked(host, spec.RemotePort) {
		e.mu.Unlock()
		_ = ln.Close()
		return Forward{}, errAlreadyForwarded
	}
	if !spec.Auto && spec.Kind == Local {
		delete(hs.declined, spec.RemotePort) // forwarding by hand clears a stop
	}
	e.nextID++
	ctx, cancel := context.WithCancel(e.ctx)
	f = &fwd{
		Forward: Forward{ID: strconv.Itoa(e.nextID), Host: host, Spec: spec, State: Active, StartedAt: e.o.Now(), MappedFrom: mapped},
		local:   local,
		cancel:  cancel,
		done:    make(chan struct{}),
		want:    mapped, // remembered as the port asked for, not the one it moved to
	}
	if spec.Kind != Local || detect.SkipHTTPProbe(spec.RemotePort, hs.ports[spec.RemotePort].Process) {
		f.Scheme = forward.SchemeNone // a database, a SOCKS proxy or a reverse forward: never opened as a page
	}
	dialer.f = f
	e.forwards[f.ID] = f
	e.wg.Add(1)
	e.mu.Unlock()

	if spec.Kind == Remote {
		go e.serveRemote(ctx, f)
	} else {
		go e.serve(ctx, f)
	}
	e.log.Info("forward started", "id", f.ID, "host", host, "kind", spec.Kind, "bind", spec.BindAddr, "local", spec.LocalPort, "remote", spec.RemotePort, "auto", spec.Auto)
	snap := e.snapshot(f)
	e.emit(ForwardChanged{Forward: snap})
	e.persist(host)
	if spec.Kind == Local {
		// The probe starts after the first event, so its result comes later.
		e.startProbe(ctx, f, conn, net.JoinHostPort(spec.RemoteHost, itoa(spec.RemotePort)))
	}
	return snap, nil
}

func (e *Engine) connOf(host HostID) (Conn, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("pfwd is shutting down")
	}
	s, ok := e.sessions[host]
	switch {
	case ok && s.state == Reconnecting:
		return nil, fmt.Errorf("%s is reconnecting; forward the port once it is back", host)
	case !ok || s.state != Connected || s.conn == nil:
		return nil, fmt.Errorf("not connected to %s; wait for the connection, or fix the error shown and reconnect", host)
	}
	return s.conn, nil
}

func (e *Engine) serve(ctx context.Context, f *fwd) {
	defer e.wg.Done()
	defer close(f.done)
	err := f.local.Serve(ctx)
	if err == nil {
		return
	}
	e.log.Info("forward failed", "id", f.ID, "err", err)
	e.mu.Lock()
	f.State, f.Err = Failed, err
	e.mu.Unlock()
	e.emit(ForwardChanged{Forward: e.snapshot(f)})
}

// dialFailed records a connection through f that could not reach its
// target. A server that refuses forwarding fails the forward for good and
// closes its listener. Other errors (nothing listening on the target, say)
// stay on the active forward so front ends can show them, and are emitted
// once per distinct message so a retrying client can't flood the events.
func (e *Engine) dialFailed(f *fwd, err error) {
	e.mu.Lock()
	switch {
	case f.State != Active:
		e.mu.Unlock()
		return
	case f.Spec.Kind == Dynamic:
		// One SOCKS destination failing, even refused by the server's
		// PermitOpen, says nothing about the others; the client got its
		// reply code.
		e.mu.Unlock()
		return
	case errors.Is(err, sshx.ErrForwardingRefused):
		f.State, f.Err = Failed, err
		f.cancel()
	case f.Err != nil && f.Err.Error() == err.Error():
		e.mu.Unlock()
		return
	default:
		f.Err = err
	}
	e.mu.Unlock()
	e.emit(ForwardChanged{Forward: e.snapshot(f)})
}

// Stop stops a forward, closes its listener and connections, and removes it.
func (e *Engine) Stop(id string) error {
	e.mu.Lock()
	f, ok := e.forwards[id]
	if ok {
		delete(e.forwards, id)
		// A stopped port is not auto-forwarded again this run.
		e.hostLocked(f.Host).declined[f.Spec.RemotePort] = true
	}
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no forward with id %q", id)
	}
	f.cancel()
	<-f.done
	e.mu.Lock()
	f.State = Stopped
	e.mu.Unlock()
	e.log.Info("forward stopped", "id", id)
	e.emit(ForwardChanged{Forward: e.snapshot(f)})
	e.persist(f.Host)
	return nil
}

func (e *Engine) snapshot(f *fwd) Forward {
	e.mu.Lock()
	snap := f.Forward
	local, base := f.local, f.base
	e.mu.Unlock()
	snap.Stats = addStats(local.Stats(), base)
	return snap
}

// Forwards returns all forwards with live counters, sorted by remote port
// and then ID.
func (e *Engine) Forwards() []Forward {
	e.mu.Lock()
	out := make([]Forward, 0, len(e.forwards))
	for _, f := range e.forwards {
		snap := f.Forward
		snap.Stats = addStats(f.local.Stats(), f.base)
		out = append(out, snap)
	}
	e.mu.Unlock()
	slices.SortFunc(out, func(a, b Forward) int {
		if a.Spec.RemotePort != b.Spec.RemotePort {
			return int(a.Spec.RemotePort) - int(b.Spec.RemotePort)
		}
		ai, _ := strconv.Atoi(a.ID)
		bi, _ := strconv.Atoi(b.ID)
		return ai - bi
	})
	return out
}

// Close stops every forward and session and waits for their goroutines,
// up to 3 seconds. It closes the event channel. Calling it again does
// nothing.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	hosts := make([]HostID, 0, len(e.hosts))
	for h := range e.hosts {
		hosts = append(hosts, h)
	}
	e.mu.Unlock()
	e.cancel()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	var err error
	select {
	case <-done:
		close(e.events)
	case <-time.After(shutdownTimeout):
		err = errors.New("shutdown took longer than 3s; some connections may not have closed cleanly")
	}
	// Quitting forgets what isn't pinned.
	for _, h := range hosts {
		e.persistOnly(h, true)
	}
	return err
}

func itoa(p uint16) string {
	return strconv.Itoa(int(p))
}
