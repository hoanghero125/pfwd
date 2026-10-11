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
)

// Conn is an established connection to a host: it runs the detector's
// commands and dials forward targets. *sshx.Conn implements it.
type Conn interface {
	detect.Runner
	forward.Dialer
	Close() error
	// Wait blocks until the connection ends.
	Wait() error
}

// DialFunc connects to a host, asking p about unknown host keys.
type DialFunc func(ctx context.Context, host HostID, p sshx.HostKeyPrompter) (Conn, error)

// Options configures an Engine.
type Options struct {
	Dial DialFunc
	// Interval is the detection polling interval; 0 means 2 s.
	Interval time.Duration
	// Logger receives debug details; nil discards them.
	Logger *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
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
	forwards map[string]*fwd
	nextID   int
}

type session struct {
	state SessionState
	conn  Conn
}

type fwd struct {
	Forward
	local  *forward.Local
	cancel context.CancelFunc
	done   chan struct{}
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
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		o:        o,
		log:      o.Logger,
		ctx:      ctx,
		cancel:   cancel,
		events:   make(chan Event, 256),
		sessions: make(map[HostID]*session),
		forwards: make(map[string]*fwd),
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

// Connect starts connecting to host in the background; events report the
// progress. It does nothing if the host is connecting or connected.
func (e *Engine) Connect(host HostID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	if s, ok := e.sessions[host]; ok && (s.state == Connecting || s.state == Connected) {
		return
	}
	s := &session{state: Connecting}
	e.sessions[host] = s
	e.wg.Add(1)
	go e.runSession(host, s)
}

func (e *Engine) setSession(s *session, state SessionState, conn Conn) {
	e.mu.Lock()
	s.state, s.conn = state, conn
	e.mu.Unlock()
}

func (e *Engine) runSession(host HostID, s *session) {
	defer e.wg.Done()
	e.emit(SessionChanged{Host: host, State: Connecting})
	conn, err := e.o.Dial(e.ctx, host, prompter{e: e, host: host})
	if e.ctx.Err() != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return
	}
	if err != nil {
		e.log.Info("connect failed", "host", host, "err", err)
		e.setSession(s, SessionFailed, nil)
		e.emit(SessionChanged{Host: host, State: SessionFailed, Err: err})
		return
	}
	e.log.Info("connected", "host", host)
	e.setSession(s, Connected, conn)
	e.emit(SessionChanged{Host: host, State: Connected})

	sctx, stop := context.WithCancel(e.ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := detect.Watch(sctx, conn, e.o.Interval, e.log, func(u detect.Update) {
			e.emit(PortsChanged{Host: host, Strategy: u.Strategy, OS: u.OS, Ports: u.Ports})
		})
		if err != nil {
			e.log.Info("detection failed", "host", host, "err", err)
			e.emit(PortsChanged{Host: host, Strategy: detect.None, Err: err})
		}
	}()
	waited := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		waited <- conn.Wait()
	}()

	select {
	case <-e.ctx.Done():
		stop()
		_ = conn.Close()
		wg.Wait()
	case werr := <-waited:
		stop()
		wg.Wait()
		if e.ctx.Err() != nil {
			return
		}
		lost := &sshx.Error{Kind: sshx.ErrConnect, Err: werr, Msg: fmt.Sprintf(
			"connection to %s was lost (%v); pfwd reconnects on its own from v0.3.0, so restart it for now", host, describe(werr))}
		e.log.Info("connection lost", "host", host, "err", werr)
		e.setSession(s, SessionFailed, nil)
		e.emit(SessionChanged{Host: host, State: SessionFailed, Err: lost})
		e.failForwards(host, errors.New("disconnected"))
	}
}

func describe(err error) string {
	if err == nil {
		return "closed by the server"
	}
	return err.Error()
}

// failForwards marks the host's active forwards Failed and closes their
// listeners. They stay listed until the user stops them.
func (e *Engine) failForwards(host HostID, reason error) {
	e.mu.Lock()
	var failed []*fwd
	for _, f := range e.forwards {
		if f.Host == host && f.State == Active {
			f.State, f.Err = Failed, reason
			f.cancel()
			failed = append(failed, f)
		}
	}
	e.mu.Unlock()
	for _, f := range failed {
		<-f.done
		e.emit(ForwardChanged{Forward: e.snapshot(f)})
	}
}

// prompter turns host key questions into HostKeyPrompt events.
type prompter struct {
	e    *Engine
	host HostID
}

func (p prompter) ConfirmHostKey(ctx context.Context, q sshx.HostKeyQuestion) (bool, error) {
	reply := make(chan bool, 1)
	p.e.emit(NewHostKeyPrompt(p.host, q, reply))
	select {
	case ok := <-reply:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Forward creates a forward on a connected host. It binds the local port
// before returning, so a busy port comes back as a
// *forward.PortInUseError.
func (e *Engine) Forward(host HostID, spec ForwardSpec) (Forward, error) {
	if spec.Kind != Local {
		return Forward{}, fmt.Errorf("%s forwards are not supported yet (planned for v0.4.0)", spec.Kind)
	}
	if spec.RemotePort == 0 {
		return Forward{}, errors.New("remote port 0 is not a port; choose a port from 1 to 65535")
	}
	if spec.BindAddr == "" {
		spec.BindAddr = "127.0.0.1"
	}
	if spec.LocalPort == 0 {
		spec.LocalPort = spec.RemotePort
	}
	if spec.RemoteHost == "" {
		spec.RemoteHost = "127.0.0.1"
	}

	conn, err := e.connOf(host)
	if err != nil {
		return Forward{}, err
	}
	ln, err := forward.Listen(spec.BindAddr, spec.LocalPort)
	if err != nil {
		return Forward{}, err
	}
	target := net.JoinHostPort(spec.RemoteHost, itoa(spec.RemotePort))
	local := forward.NewLocal(ln, conn, target, forward.Options{Logger: e.log})

	e.mu.Lock()
	if e.closed || e.sessions[host] == nil || e.sessions[host].conn != conn {
		e.mu.Unlock()
		_ = ln.Close()
		return Forward{}, fmt.Errorf("the connection to %s ended; forward again after reconnecting", host)
	}
	e.nextID++
	ctx, cancel := context.WithCancel(e.ctx)
	f := &fwd{
		Forward: Forward{ID: strconv.Itoa(e.nextID), Host: host, Spec: spec, State: Active, StartedAt: e.o.Now()},
		local:   local,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	e.forwards[f.ID] = f
	e.wg.Add(1)
	e.mu.Unlock()

	go e.serve(ctx, f)
	e.log.Info("forward started", "id", f.ID, "host", host, "local", spec.LocalPort, "target", target)
	snap := e.snapshot(f)
	e.emit(ForwardChanged{Forward: snap})
	return snap, nil
}

func (e *Engine) connOf(host HostID) (Conn, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("pfwd is shutting down")
	}
	s, ok := e.sessions[host]
	if !ok || s.state != Connected {
		return nil, fmt.Errorf("not connected to %s; wait for the connection, or fix the error shown and restart pfwd", host)
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

// Stop stops a forward, closes its listener and connections, and removes it.
func (e *Engine) Stop(id string) error {
	e.mu.Lock()
	f, ok := e.forwards[id]
	if ok {
		delete(e.forwards, id)
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
	return nil
}

func (e *Engine) snapshot(f *fwd) Forward {
	e.mu.Lock()
	snap := f.Forward
	e.mu.Unlock()
	snap.Stats = f.local.Stats()
	return snap
}

// Forwards returns all forwards with live counters, sorted by remote port
// and then ID.
func (e *Engine) Forwards() []Forward {
	e.mu.Lock()
	out := make([]Forward, 0, len(e.forwards))
	for _, f := range e.forwards {
		snap := f.Forward
		snap.Stats = f.local.Stats()
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
	e.mu.Unlock()
	e.cancel()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		close(e.events)
		return nil
	case <-time.After(shutdownTimeout):
		return errors.New("shutdown took longer than 3s; some connections may not have closed cleanly")
	}
}

func itoa(p uint16) string {
	return strconv.Itoa(int(p))
}
