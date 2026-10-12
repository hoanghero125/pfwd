package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// session is one run of a host's connection loop: connect, serve, and after
// a loss back off and reconnect, until it fails for good, the user stops
// retrying, or the engine closes. Its fields are guarded by Engine.mu.
type session struct {
	state SessionState
	conn  Conn
	// ready is closed while connected. When the connection is lost an open
	// one replaces it, and dialers of paused forwards wait on it.
	ready chan struct{}
	// retry skips the backoff wait; Reconnect sends on it.
	retry chan struct{}
	// cancel ends the loop; StopRetrying calls it.
	cancel context.CancelFunc
}

// errStoppedRetrying ends a session the user stopped retrying.
var errStoppedRetrying = errors.New("stopped retrying")

// Connect starts connecting to host in the background; events report the
// progress. It does nothing while the host is connecting, connected or
// reconnecting.
func (e *Engine) Connect(host HostID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	if s, ok := e.sessions[host]; ok && s.state != SessionFailed && s.state != Closed {
		return
	}
	e.startSessionLocked(host)
}

// Reconnect tries host again now: it skips the wait while reconnecting,
// and starts over after a failure or StopRetrying.
func (e *Engine) Reconnect(host HostID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	s, ok := e.sessions[host]
	switch {
	case !ok || s.state == SessionFailed || s.state == Closed:
		e.startSessionLocked(host)
	case s.state == Reconnecting:
		select {
		case s.retry <- struct{}{}:
		default:
		}
	}
}

// StopRetrying gives up reconnecting to host. Its forwards stay, Paused,
// with their listeners; Reconnect tries again.
func (e *Engine) StopRetrying(host HostID) {
	e.mu.Lock()
	s, ok := e.sessions[host]
	reconnecting := ok && s.state == Reconnecting
	e.mu.Unlock()
	if reconnecting {
		s.cancel()
	}
}

// startSessionLocked starts a session loop for host; e.mu must be held.
func (e *Engine) startSessionLocked(host HostID) {
	ctx, cancel := context.WithCancel(e.ctx)
	s := &session{state: Connecting, ready: make(chan struct{}), retry: make(chan struct{}, 1), cancel: cancel}
	e.sessions[host] = s
	hs := e.hostLocked(host)
	e.wg.Add(1)
	go e.runSession(ctx, host, s, hs)
}

// permanent reports whether a dial error won't go away by retrying: a
// rejected login, a host key problem, or an unsupported setting.
func permanent(err error) bool {
	return errors.Is(err, sshx.ErrAuth) || errors.Is(err, sshx.ErrHostKey) || errors.Is(err, sshx.ErrUnsupported)
}

func (e *Engine) runSession(ctx context.Context, host HostID, s *session, hs *hostState) {
	defer e.wg.Done()
	defer s.cancel()
	e.emit(SessionChanged{Host: host, State: Connecting})
	e.mu.Lock()
	auto := hs.auto
	e.mu.Unlock()
	e.emit(AutoChanged{Host: host, On: auto})

	everUp := false
	attempt := 0 // reconnect attempts since the connection was lost
	for {
		conn, err := e.o.Dial(ctx, host, prompter{e: e, host: host})
		if ctx.Err() != nil {
			if conn != nil {
				_ = conn.Close()
			}
			e.ended(host, s)
			return
		}
		var cause error
		if err != nil {
			e.log.Info("connect failed", "host", host, "attempt", attempt, "err", err)
			if !everUp || permanent(err) {
				e.setState(s, SessionFailed)
				e.emit(SessionChanged{Host: host, State: SessionFailed, Err: err})
				return
			}
			cause = err
		} else {
			everUp = true
			attempt = 0
			cause = e.serveConn(ctx, host, s, hs, conn)
			if cause == nil {
				e.ended(host, s)
				return
			}
		}
		attempt++
		wait := e.o.Backoff(attempt)
		e.emit(SessionChanged{Host: host, State: Reconnecting, Err: cause, RetryAt: e.o.Now().Add(wait), Attempt: attempt})
		if attempt == 1 {
			e.pauseForwards(host, Disconnected)
		}
		if !e.sleep(ctx, s, wait) {
			e.ended(host, s)
			return
		}
		e.emit(SessionChanged{Host: host, State: Reconnecting, Err: cause, Attempt: attempt})
	}
}

// serveConn runs detection on conn until the connection ends. It returns
// why the connection was lost, or nil when ctx ended. Either way conn is
// closed when it returns.
func (e *Engine) serveConn(ctx context.Context, host HostID, s *session, hs *hostState, conn Conn) error {
	e.mu.Lock()
	s.state, s.conn = Connected, conn
	close(s.ready)
	e.mu.Unlock()
	e.log.Info("connected", "host", host)
	e.emit(SessionChanged{Host: host, State: Connected})
	e.resumeForwards(host)

	cctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		o := detect.WatchOptions{Interval: e.o.Interval, Logger: e.log, Tracker: hs.tracker}
		err := detect.Watch(cctx, conn, o, func(u detect.Update) { e.portsUpdated(host, hs, u) })
		if err != nil && cctx.Err() == nil {
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

	var werr error
	select {
	case <-ctx.Done():
	case werr = <-waited:
	}
	stop()
	_ = conn.Close()
	wg.Wait()

	e.mu.Lock()
	s.conn = nil
	if ctx.Err() == nil {
		s.state = Reconnecting
		s.ready = make(chan struct{})
	}
	e.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	e.log.Info("connection lost", "host", host, "err", werr)
	return &sshx.Error{Kind: sshx.ErrConnect, Err: werr, Msg: fmt.Sprintf(
		"connection to %s was lost (%v)", host, describe(werr))}
}

func describe(err error) string {
	if err == nil {
		return "closed by the server"
	}
	return err.Error()
}

// sleep waits d, or less when Reconnect asks to retry now. It reports false
// when ctx ended.
func (e *Engine) sleep(ctx context.Context, s *session, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.retry:
		return true
	case <-ctx.Done():
		return false
	}
}

// ended handles a session whose context ended: nothing more when the engine
// is closing, else the user stopped retrying.
func (e *Engine) ended(host HostID, s *session) {
	if e.ctx.Err() != nil {
		return
	}
	e.setState(s, SessionFailed)
	e.log.Info("stopped reconnecting", "host", host)
	e.emit(SessionChanged{Host: host, State: SessionFailed, Err: &sshx.Error{Kind: sshx.ErrConnect, Err: errStoppedRetrying, Msg: fmt.Sprintf(
		"stopped reconnecting to %s; reconnect once the host is reachable", host)}})
}

func (e *Engine) setState(s *session, state SessionState) {
	e.mu.Lock()
	s.state = state
	e.mu.Unlock()
}

// pauseForwards pauses host's active forwards for reason, keeping their
// listeners.
func (e *Engine) pauseForwards(host HostID, reason PauseReason) {
	now := e.o.Now()
	e.mu.Lock()
	var changed []*fwd
	for _, f := range e.forwards {
		if f.Host == host && f.State == Active {
			f.State, f.Pause, f.PausedAt = Paused, reason, now
			changed = append(changed, f)
		}
	}
	e.mu.Unlock()
	for _, f := range changed {
		e.log.Info("forward paused", "id", f.ID, "reason", reason)
		e.emit(ForwardChanged{Forward: e.snapshot(f)})
	}
}

// resumeForwards makes host's forwards that paused for the lost connection
// active again.
func (e *Engine) resumeForwards(host HostID) {
	e.mu.Lock()
	var changed []*fwd
	for _, f := range e.forwards {
		if f.Host == host && f.State == Paused && f.Pause == Disconnected {
			f.State, f.Pause, f.PausedAt, f.Err = Active, NotPaused, time.Time{}, nil
			changed = append(changed, f)
		}
	}
	e.mu.Unlock()
	for _, f := range changed {
		e.log.Info("forward resumed", "id", f.ID)
		e.emit(ForwardChanged{Forward: e.snapshot(f)})
	}
}

// defaultBackoff waits 0.5 s before the first attempt and doubles each
// time, up to limit, then varies the wait by ±20 % so clients that lost the
// same server don't retry in step. jitter returns a number in [0, 1).
func defaultBackoff(limit time.Duration, jitter func() float64) func(int) time.Duration {
	return func(attempt int) time.Duration {
		d := limit
		if attempt >= 1 && attempt < 32 {
			if b := 500 * time.Millisecond << (attempt - 1); b > 0 && b < limit {
				d = b
			}
		}
		return time.Duration(float64(d) * (0.8 + 0.4*jitter())).Round(time.Millisecond)
	}
}

// fwdDialer dials through whatever connection the forward's host has now,
// so a forward's listener outlives the connection it started on.
type fwdDialer struct {
	e *Engine
	f *fwd // set before the forward serves
}

func (d *fwdDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.e.connFor(ctx, d.f)
	if err != nil {
		return nil, err
	}
	return conn.DialContext(ctx, network, addr)
}

// connFor returns the connection a new connection through f should use: at
// once while connected, after up to ConnWait while reconnecting, and an
// error while f's port is gone or the host gave up.
func (e *Engine) connFor(ctx context.Context, f *fwd) (Conn, error) {
	deadline := time.NewTimer(e.o.ConnWait)
	defer deadline.Stop()
	for {
		e.mu.Lock()
		portGone := f.State == Paused && f.Pause == PortGone
		s := e.sessions[f.Host]
		var conn Conn
		var ready chan struct{}
		down := s == nil || s.state == SessionFailed || s.state == Closed
		if !down {
			if s.state == Connected {
				conn = s.conn
			} else {
				ready = s.ready
			}
		}
		e.mu.Unlock()
		switch {
		case portGone:
			return nil, fmt.Errorf("port %d is no longer listening on %s", f.Spec.RemotePort, f.Host)
		case conn != nil:
			return conn, nil
		case down:
			return nil, fmt.Errorf("not connected to %s", f.Host)
		}
		select {
		case <-ready:
		case <-deadline.C:
			return nil, fmt.Errorf("%s is still reconnecting", f.Host)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// prompter turns prompts from the dialer into events.
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

func (p prompter) AskCredential(ctx context.Context, q sshx.CredentialQuestion) ([]byte, error) {
	reply := make(chan CredentialAnswer, 1)
	p.e.emit(NewCredentialPrompt(p.host, q, reply))
	select {
	case a := <-reply:
		if a.Canceled {
			return nil, sshx.ErrCanceled
		}
		return a.Secret, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// jitter is the default backoff's randomness.
func jitter() float64 { return rand.Float64() } //nolint:gosec // backoff jitter, not a secret
