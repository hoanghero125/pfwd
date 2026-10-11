package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fakeConn answers the probe, streams one frame per entry of frames and
// then blocks, dials targets directly on this machine, and ends Wait when
// it is closed or dropped.
type fakeConn struct {
	probe    string
	probeErr error
	frames   []string
	dialErr  error // returned by DialContext when set

	once    sync.Once
	gone    chan struct{}
	waitErr error
}

func newFakeConn(frames ...string) *fakeConn {
	return &fakeConn{probe: "PFWD_OS Linux\nHAVE_SS\n", frames: frames, gone: make(chan struct{})}
}

func (c *fakeConn) Output(context.Context, string) ([]byte, error) {
	return []byte(c.probe), c.probeErr
}

func (c *fakeConn) Start(ctx context.Context, _ string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		for _, f := range c.frames {
			if _, err := io.WriteString(pw, "\x1ePFWD\n"+f+"\x1ePFWD END\n"); err != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
		case <-c.gone:
		}
		pw.Close()
	}()
	return pr, nil
}

func (c *fakeConn) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	select {
	case <-c.gone:
		return nil, errors.New("connection closed")
	default:
	}
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

func (c *fakeConn) end(err error) {
	c.once.Do(func() {
		c.waitErr = err
		close(c.gone)
	})
}

func (c *fakeConn) Close() error { c.end(nil); return nil }

func (c *fakeConn) Wait() error {
	<-c.gone
	return c.waitErr
}

func newEngine(t *testing.T, dial DialFunc) *Engine {
	t.Helper()
	e := New(Options{Dial: dial, Interval: time.Second})
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return e
}

func dialTo(c *fakeConn) DialFunc {
	return func(context.Context, HostID, sshx.HostKeyPrompter) (Conn, error) { return c, nil }
}

// next returns the next event matching pred, failing after 5 seconds.
func next[T Event](t *testing.T, e *Engine, pred func(T) bool) T {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-e.Events():
			if !ok {
				t.Fatal("event channel closed")
			}
			if v, ok := ev.(T); ok && (pred == nil || pred(v)) {
				return v
			}
		case <-timeout:
			var zero T
			t.Fatalf("timed out waiting for %T", zero)
		}
	}
}

func connected(t *testing.T, e *Engine, host HostID) {
	t.Helper()
	e.Connect(host)
	next(t, e, func(s SessionChanged) bool { return s.State == Connected })
}

func echoServer(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return netip.MustParseAddrPort(ln.Addr().String()).Port()
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return netip.MustParseAddrPort(ln.Addr().String()).Port()
}

const ssLine = `LISTEN 0 5 0.0.0.0:8123 0.0.0.0:* users:(("python3",pid=10,fd=3))` + "\n"

func TestConnectEmitsConnectingConnectedPorts(t *testing.T) {
	e := newEngine(t, dialTo(newFakeConn(ssLine)))
	e.Connect("devbox")
	first := next[SessionChanged](t, e, nil)
	if first.Host != "devbox" || first.State != Connecting {
		t.Errorf("first event = %+v, want Connecting", first)
	}
	next(t, e, func(s SessionChanged) bool { return s.State == Connected })
	ports := next[PortsChanged](t, e, nil)
	if ports.Host != "devbox" || ports.Strategy != detect.SS || ports.OS != "Linux" || len(ports.Ports) != 1 || ports.Ports[0].Port != 8123 {
		t.Errorf("PortsChanged = %+v", ports)
	}
}

func TestConnectTwiceIsANoOp(t *testing.T) {
	dials := 0
	var mu sync.Mutex
	c := newFakeConn()
	e := newEngine(t, func(context.Context, HostID, sshx.HostKeyPrompter) (Conn, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return c, nil
	})
	connected(t, e, "devbox")
	e.Connect("devbox")
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if dials != 1 {
		t.Errorf("dialed %d times, want 1", dials)
	}
}

func TestConnectFailure(t *testing.T) {
	authErr := &sshx.Error{Kind: sshx.ErrAuth, Msg: "authentication to devbox failed"}
	e := newEngine(t, func(context.Context, HostID, sshx.HostKeyPrompter) (Conn, error) { return nil, authErr })
	e.Connect("devbox")
	failed := next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	if !errors.Is(failed.Err, sshx.ErrAuth) {
		t.Errorf("Err = %v, want ErrAuth", failed.Err)
	}
	if _, err := e.Forward("devbox", ForwardSpec{RemotePort: 80}); err == nil {
		t.Error("Forward on a failed session succeeded")
	}
}

func TestHostKeyPromptRoundTrip(t *testing.T) {
	answer := make(chan bool, 1)
	c := newFakeConn()
	e := newEngine(t, func(ctx context.Context, _ HostID, p sshx.HostKeyPrompter) (Conn, error) {
		ok, err := p.ConfirmHostKey(ctx, sshx.HostKeyQuestion{Host: "devbox", KeyType: "ssh-ed25519", Fingerprint: "SHA256:abc"})
		if err != nil {
			return nil, err
		}
		answer <- ok
		return c, nil
	})
	e.Connect("devbox")
	prompt := next[HostKeyPrompt](t, e, nil)
	if prompt.Host != "devbox" || prompt.Question.Fingerprint != "SHA256:abc" {
		t.Errorf("prompt = %+v", prompt)
	}
	prompt.Answer(true)
	prompt.Answer(false) // a second answer is ignored, not blocked on
	if !<-answer {
		t.Error("the dialer saw false, want true")
	}
	next(t, e, func(s SessionChanged) bool { return s.State == Connected })
}

func TestForwardActiveAndPipes(t *testing.T) {
	echo := echoServer(t)
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	local := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: local, RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "1" || f.State != Active || f.Spec.BindAddr != "127.0.0.1" || f.Spec.RemoteHost != "127.0.0.1" || f.StartedAt.IsZero() {
		t.Errorf("Forward = %+v", f)
	}
	changed := next[ForwardChanged](t, e, nil)
	if changed.Forward.ID != "1" || changed.Forward.State != Active {
		t.Errorf("ForwardChanged = %+v", changed)
	}

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(local)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
	// The counters move right after each write, so the echo can arrive
	// before they do; wait for them instead of reading once.
	deadline := time.Now().Add(5 * time.Second)
	for {
		fs := e.Forwards()
		if len(fs) == 1 && fs[0].Stats.BytesOut == 5 && fs[0].Stats.BytesIn == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Forwards = %+v, want 5 bytes each way", fs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestForwardDefaultsLocalPortToRemote(t *testing.T) {
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	p := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{RemotePort: p, RemoteHost: "::1"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Spec.LocalPort != p || f.Spec.RemoteHost != "::1" {
		t.Errorf("Spec = %+v", f.Spec)
	}
}

func TestForwardPortInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	p := netip.MustParseAddrPort(busy.Addr().String()).Port()
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	_, err = e.Forward("devbox", ForwardSpec{LocalPort: p, RemotePort: 80})
	var inUse *forward.PortInUseError
	if !errors.As(err, &inUse) || inUse.Suggested == 0 {
		t.Fatalf("Forward = %v, want *PortInUseError with a suggestion", err)
	}
	if fs := e.Forwards(); len(fs) != 0 {
		t.Errorf("Forwards = %+v, want none", fs)
	}
}

func TestForwardNotConnected(t *testing.T) {
	e := newEngine(t, dialTo(newFakeConn()))
	_, err := e.Forward("devbox", ForwardSpec{RemotePort: 80})
	if err == nil || !strings.Contains(err.Error(), "devbox") {
		t.Errorf("Forward before Connect = %v, want an error naming the host", err)
	}
}

func TestForwardUnsupportedKind(t *testing.T) {
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	for _, k := range []ForwardKind{Remote, Dynamic} {
		if _, err := e.Forward("devbox", ForwardSpec{Kind: k, RemotePort: 80, LocalPort: freePort(t)}); err == nil {
			t.Errorf("Forward(%v) succeeded", k)
		}
	}
}

func TestForwardRejectsPortZero(t *testing.T) {
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	if _, err := e.Forward("devbox", ForwardSpec{}); err == nil {
		t.Error("Forward with remote port 0 succeeded")
	}
}

func TestStopRemovesAndEmitsStopped(t *testing.T) {
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	local := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: local, RemotePort: 80})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Stop(f.ID); err != nil {
		t.Fatal(err)
	}
	next(t, e, func(c ForwardChanged) bool { return c.Forward.ID == f.ID && c.Forward.State == Stopped })
	if fs := e.Forwards(); len(fs) != 0 {
		t.Errorf("Forwards = %+v, want none", fs)
	}
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(local)), time.Second); err == nil {
		c.Close()
		t.Error("the listener is still open after Stop")
	}
	if err := e.Stop(f.ID); err == nil {
		t.Error("stopping an unknown forward succeeded")
	}
}

func TestSessionDropFailsForwards(t *testing.T) {
	c := newFakeConn()
	e := newEngine(t, dialTo(c))
	connected(t, e, "devbox")
	local := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: local, RemotePort: 80})
	if err != nil {
		t.Fatal(err)
	}
	c.end(errors.New("read tcp: connection reset by peer"))
	lost := next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	if !errors.Is(lost.Err, sshx.ErrConnect) || !strings.Contains(lost.Err.Error(), "devbox") {
		t.Errorf("session error = %v, want ErrConnect naming the host", lost.Err)
	}
	failed := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Failed })
	if failed.Forward.Err == nil {
		t.Error("failed forward has no error")
	}
	if fs := e.Forwards(); len(fs) != 1 || fs[0].State != Failed {
		t.Errorf("Forwards = %+v, want the failed forward kept until stopped", fs)
	}
	if _, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: 80}); err == nil {
		t.Error("Forward after the session dropped succeeded")
	}
	if err := e.Stop(f.ID); err != nil {
		t.Errorf("Stop on a failed forward: %v", err)
	}
}

func TestProbeErrorReported(t *testing.T) {
	c := newFakeConn()
	c.probeErr = errors.New("exec request denied")
	e := newEngine(t, dialTo(c))
	e.Connect("devbox")
	pc := next[PortsChanged](t, e, nil)
	if pc.Strategy != detect.None || pc.Err == nil || !strings.Contains(pc.Err.Error(), "exec request denied") {
		t.Errorf("PortsChanged = %+v, want Strategy None with the probe error", pc)
	}
}

func TestCloseIsIdempotentAndClosesEvents(t *testing.T) {
	e := New(Options{Dial: dialTo(newFakeConn())})
	connected(t, e, "devbox")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	for range e.Events() {
	}
	if _, err := e.Forward("devbox", ForwardSpec{RemotePort: 80}); err == nil {
		t.Error("Forward after Close succeeded")
	}
}

func TestCloseWhileDialing(t *testing.T) {
	started := make(chan struct{})
	e := New(Options{Dial: func(ctx context.Context, _ HostID, _ sshx.HostKeyPrompter) (Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	e.Connect("devbox")
	<-started
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStateStrings(t *testing.T) {
	for s, want := range map[ForwardState]string{Starting: "starting", Active: "active", Paused: "paused", Failed: "failed", Stopped: "stopped"} {
		if s.String() != want {
			t.Errorf("%d = %q, want %q", s, s.String(), want)
		}
	}
	for s, want := range map[SessionState]string{Connecting: "connecting", Connected: "connected", SessionFailed: "failed", Closed: "closed"} {
		if s.String() != want {
			t.Errorf("%d = %q, want %q", s, s.String(), want)
		}
	}
}

func dialLocal(t *testing.T, port uint16) {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Read(make([]byte, 1)) // the forward closes it after the failed dial
	c.Close()
}

func TestForwardRefusedByServerFails(t *testing.T) {
	c := newFakeConn()
	c.dialErr = &sshx.Error{Kind: sshx.ErrForwardingRefused, Msg: "devbox refuses TCP forwarding (AllowTcpForwarding or DisableForwarding in its sshd_config); ask the server's admin to allow it"}
	e := newEngine(t, dialTo(c))
	connected(t, e, "devbox")
	local := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: local, RemotePort: 8888})
	if err != nil {
		t.Fatal(err)
	}
	dialLocal(t, local)
	failed := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Failed })
	if !errors.Is(failed.Forward.Err, sshx.ErrForwardingRefused) {
		t.Errorf("Err = %v, want ErrForwardingRefused", failed.Forward.Err)
	}
	if fs := e.Forwards(); len(fs) != 1 || fs[0].State != Failed {
		t.Errorf("Forwards = %+v", fs)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(local)), 200*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("the listener of a refused forward is still open")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestForwardDialErrorShownWhileActive(t *testing.T) {
	c := newFakeConn()
	c.dialErr = errors.New("devbox could not connect to 127.0.0.1:8888: connection refused; check that something still listens there")
	e := newEngine(t, dialTo(c))
	connected(t, e, "devbox")
	local := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: local, RemotePort: 8888})
	if err != nil {
		t.Fatal(err)
	}
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.Err == nil })
	dialLocal(t, local)
	got := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.Err != nil })
	if got.Forward.State != Active || !strings.Contains(got.Forward.Err.Error(), "connection refused") {
		t.Errorf("ForwardChanged = %+v, want Active with the dial error", got.Forward)
	}
	// The same error again is not re-sent, so a retrying browser can't
	// flood the event stream.
	dialLocal(t, local)
	select {
	case ev := <-e.Events():
		t.Errorf("unexpected event after a repeated error: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}
