package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// dialPlan answers dials in order: a Conn, or an error. Past the end it
// keeps answering with the last entry. gate, when set, holds each dial
// until a value arrives on it.
type dialPlan struct {
	mu    sync.Mutex
	steps []any // Conn or error
	calls int
	gate  chan struct{}
}

func (d *dialPlan) dial(ctx context.Context, _ HostID, _ Prompts) (Conn, error) {
	if d.gate != nil {
		select {
		case <-d.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	d.mu.Lock()
	step := d.steps[min(d.calls, len(d.steps)-1)]
	d.calls++
	d.mu.Unlock()
	if err, ok := step.(error); ok {
		return nil, err
	}
	conn, _ := step.(Conn)
	return conn, nil
}

func (d *dialPlan) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

var errUnreachable = &sshx.Error{Kind: sshx.ErrConnect, Msg: "cannot reach devbox"}

// reconnectEngine returns an engine with auto-forward off, the given
// backoff for every attempt, and a 2 s wait for connections accepted while
// reconnecting.
func reconnectEngine(t *testing.T, d *dialPlan, backoff time.Duration) *Engine {
	t.Helper()
	return newEngineWith(t, Options{
		Dial: d.dial, Interval: time.Second,
		Backoff:  func(int) time.Duration { return backoff },
		ConnWait: 2 * time.Second,
		Settings: func(HostID) HostSettings { return HostSettings{Filter: detect.DefaultFilter()} },
	})
}

// echoThrough writes msg to the local port and reads it back.
func echoThrough(t *testing.T, port uint16, msg string) {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatalf("dial the local port: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("echo = %q, %v; want %q", buf, err, msg)
	}
}

func TestLostConnectionPausesAndResumes(t *testing.T) {
	echo := echoServer(t)
	c1, c2 := newFakeConn(), newFakeConn()
	d := &dialPlan{steps: []any{c1, c2}}
	e := reconnectEngine(t, d, 10*time.Millisecond)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c1.end(errors.New("read tcp: connection reset by peer"))
	lost := next(t, e, func(s SessionChanged) bool { return s.State == Reconnecting })
	if lost.Err == nil || !strings.Contains(lost.Err.Error(), "devbox") || lost.Attempt != 1 {
		t.Errorf("lost = %+v, want an error naming the host on attempt 1", lost)
	}
	paused := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Paused })
	if paused.Forward.Pause != Disconnected || paused.Forward.PausedAt.IsZero() {
		t.Errorf("paused forward = %+v, want reason Disconnected", paused.Forward)
	}
	next(t, e, func(s SessionChanged) bool { return s.State == Connected })
	resumed := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Active })
	if resumed.Forward.Pause != NotPaused || resumed.Forward.Spec.LocalPort != f.Spec.LocalPort {
		t.Errorf("resumed forward = %+v", resumed.Forward)
	}
	echoThrough(t, f.Spec.LocalPort, "through the new connection")
}

func TestConnectionWaitsForReconnect(t *testing.T) {
	echo := echoServer(t)
	c1, c2 := newFakeConn(), newFakeConn()
	d := &dialPlan{steps: []any{c1, c2}}
	e := reconnectEngine(t, d, 10*time.Millisecond)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.gate = make(chan struct{})
	d.mu.Unlock()
	c1.end(errors.New("broken pipe"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Paused })
	done := make(chan struct{})
	go func() {
		defer close(done)
		echoThrough(t, f.Spec.LocalPort, "accepted while disconnected")
	}()
	time.Sleep(200 * time.Millisecond) // the connection is waiting now
	d.gate <- struct{}{}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting connection never got through")
	}
}

func TestConnectionGivesUpWhileDisconnected(t *testing.T) {
	echo := echoServer(t)
	c1 := newFakeConn()
	d := &dialPlan{steps: []any{c1, errUnreachable}}
	e := newEngineWith(t, Options{
		Dial: d.dial, Backoff: func(int) time.Duration { return time.Hour }, ConnWait: 200 * time.Millisecond,
		Settings: func(HostID) HostSettings { return HostSettings{Filter: detect.DefaultFilter()} },
	})
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c1.end(errors.New("broken pipe"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Paused })
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(f.Spec.LocalPort)))
	if err != nil {
		t.Fatalf("the paused forward's listener is gone: %v", err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("read data from a forward with no connection")
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("the connection was held %v, want it closed after the 200ms wait", waited)
	}
}

func TestReconnectRetriesUntilUp(t *testing.T) {
	c1, c2 := newFakeConn(), newFakeConn()
	d := &dialPlan{steps: []any{c1, errUnreachable, errUnreachable, c2}}
	e := reconnectEngine(t, d, 10*time.Millisecond)
	connected(t, e, "devbox")
	c1.end(errors.New("broken pipe"))
	var attempts []int
	for {
		s := next[SessionChanged](t, e, nil)
		if s.State == Connected {
			break
		}
		if s.State == Reconnecting && !s.RetryAt.IsZero() {
			attempts = append(attempts, s.Attempt)
		}
	}
	if want := []int{1, 2, 3}; !equalInts(attempts, want) {
		t.Errorf("waits before attempts %v, want %v", attempts, want)
	}
	if n := d.count(); n != 4 {
		t.Errorf("dialed %d times, want 4", n)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStopRetryingThenReconnect(t *testing.T) {
	echo := echoServer(t)
	c1, c2 := newFakeConn(), newFakeConn()
	d := &dialPlan{steps: []any{c1, errUnreachable}}
	e := reconnectEngine(t, d, time.Hour)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c1.end(errors.New("broken pipe"))
	next(t, e, func(s SessionChanged) bool { return s.State == Reconnecting && !s.RetryAt.IsZero() })
	e.StopRetrying("devbox")
	stopped := next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	if stopped.Err == nil || !strings.Contains(stopped.Err.Error(), "stopped") {
		t.Errorf("Err = %v, want one saying retrying stopped", stopped.Err)
	}
	if fs := e.Forwards(); len(fs) != 1 || fs[0].State != Paused {
		t.Errorf("Forwards = %+v, want the forward kept Paused", fs)
	}
	d.mu.Lock()
	d.steps = append(d.steps, c2)
	d.calls = len(d.steps) - 1
	d.mu.Unlock()
	e.Reconnect("devbox")
	next(t, e, func(s SessionChanged) bool { return s.State == Connected })
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Active })
	echoThrough(t, f.Spec.LocalPort, "after reconnecting by hand")
}

func TestReconnectSkipsTheWait(t *testing.T) {
	c1, c2 := newFakeConn(), newFakeConn()
	d := &dialPlan{steps: []any{c1, c2}}
	e := reconnectEngine(t, d, time.Hour)
	connected(t, e, "devbox")
	c1.end(errors.New("broken pipe"))
	next(t, e, func(s SessionChanged) bool { return s.State == Reconnecting && !s.RetryAt.IsZero() })
	e.Reconnect("devbox")
	next(t, e, func(s SessionChanged) bool { return s.State == Connected })
}

func TestPermanentErrorStopsRetrying(t *testing.T) {
	c1 := newFakeConn()
	authErr := &sshx.Error{Kind: sshx.ErrAuth, Msg: "authentication to devbox failed"}
	d := &dialPlan{steps: []any{c1, authErr}}
	e := reconnectEngine(t, d, 10*time.Millisecond)
	connected(t, e, "devbox")
	c1.end(errors.New("broken pipe"))
	failed := next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	if !errors.Is(failed.Err, sshx.ErrAuth) {
		t.Errorf("Err = %v, want ErrAuth", failed.Err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := d.count(); n != 2 {
		t.Errorf("dialed %d times, want 2: an auth failure isn't retried", n)
	}
}

func TestFirstConnectFailureIsNotRetried(t *testing.T) {
	d := &dialPlan{steps: []any{errUnreachable}}
	e := reconnectEngine(t, d, 10*time.Millisecond)
	e.Connect("devbox")
	next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	time.Sleep(100 * time.Millisecond)
	if n := d.count(); n != 1 {
		t.Errorf("dialed %d times, want 1", n)
	}
	e.Reconnect("devbox") // tries again by hand
	next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	if n := d.count(); n != 2 {
		t.Errorf("dialed %d times after Reconnect, want 2", n)
	}
}

func TestCloseDuringBackoff(t *testing.T) {
	c1 := newFakeConn()
	d := &dialPlan{steps: []any{c1, errUnreachable}}
	e := New(Options{Dial: d.dial, Backoff: func(int) time.Duration { return time.Hour }})
	connected(t, e, "devbox")
	c1.end(errors.New("broken pipe"))
	next(t, e, func(s SessionChanged) bool { return s.State == Reconnecting && !s.RetryAt.IsZero() })
	start := time.Now()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Close took %v during a backoff wait", took)
	}
}

func TestStopWhileReconnecting(t *testing.T) {
	echo := echoServer(t)
	c1 := newFakeConn()
	d := &dialPlan{steps: []any{c1, errUnreachable}}
	e := reconnectEngine(t, d, time.Hour)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c1.end(errors.New("broken pipe"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Paused })
	if err := e.Stop(f.ID); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(f.Spec.LocalPort)))
	if err != nil {
		t.Fatalf("the stopped forward still holds its port: %v", err)
	}
	ln.Close()
}

func TestForwardWhileReconnecting(t *testing.T) {
	c1 := newFakeConn()
	d := &dialPlan{steps: []any{c1, errUnreachable}}
	e := reconnectEngine(t, d, time.Hour)
	connected(t, e, "devbox")
	c1.end(errors.New("broken pipe"))
	next(t, e, func(s SessionChanged) bool { return s.State == Reconnecting })
	_, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: 80})
	if err == nil || !strings.Contains(err.Error(), "reconnecting") {
		t.Errorf("err = %v, want one saying the host is reconnecting", err)
	}
}

func TestDefaultBackoff(t *testing.T) {
	mid := defaultBackoff(30*time.Second, func() float64 { return 0.5 })
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := mid(i + 1); got != w {
			t.Errorf("attempt %d = %v, want %v", i+1, got, w)
		}
	}
	if got := mid(1000); got != 30*time.Second {
		t.Errorf("attempt 1000 = %v, want the 30s cap", got)
	}
	low := defaultBackoff(30*time.Second, func() float64 { return 0 })
	high := defaultBackoff(30*time.Second, func() float64 { return 1 })
	if low(3) != 1600*time.Millisecond || high(3) != 2400*time.Millisecond {
		t.Errorf("jitter bounds = %v..%v, want 1.6s..2.4s", low(3), high(3))
	}
}

func TestCredentialPromptRoundTrip(t *testing.T) {
	c := newFakeConn()
	got := make(chan string, 2)
	e := newEngine(t, func(ctx context.Context, _ HostID, p Prompts) (Conn, error) {
		secret, err := p.AskCredential(ctx, sshx.CredentialQuestion{Kind: sshx.Password, Prompt: "alice"})
		if err != nil {
			got <- "error: " + err.Error()
			return nil, err
		}
		got <- string(secret)
		return c, nil
	})
	e.Connect("devbox")
	prompt := next[CredentialPrompt](t, e, nil)
	if prompt.Host != "devbox" || prompt.Question.Prompt != "alice" {
		t.Errorf("prompt = %+v", prompt)
	}
	prompt.Answer([]byte("hunter2"))
	prompt.Cancel() // a second reply is ignored
	if s := <-got; s != "hunter2" {
		t.Errorf("the dialer got %q", s)
	}
	next(t, e, func(s SessionChanged) bool { return s.State == Connected })
}

func TestCredentialPromptCancel(t *testing.T) {
	got := make(chan error, 1)
	e := newEngine(t, func(ctx context.Context, _ HostID, p Prompts) (Conn, error) {
		_, err := p.AskCredential(ctx, sshx.CredentialQuestion{Kind: sshx.Code, Prompt: "Code: "})
		got <- err
		return nil, err
	})
	e.Connect("devbox")
	next[CredentialPrompt](t, e, nil).Cancel()
	if err := <-got; !errors.Is(err, sshx.ErrCanceled) {
		t.Errorf("err = %v, want sshx.ErrCanceled", err)
	}
}
