package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// listen formats one ss line for a port owned by process.
func listen(port uint16, process string) string {
	return fmt.Sprintf("LISTEN 0 5 0.0.0.0:%d 0.0.0.0:* users:((%q,pid=7,fd=3))\n", port, process)
}

func autoOptions(dial DialFunc) Options {
	return Options{
		Dial: dial, Interval: time.Second, Debounce: 2,
		Settings: func(HostID) HostSettings { return HostSettings{Auto: true, Filter: detect.DefaultFilter()} },
	}
}

// noEvent fails if an event matching pred arrives within d.
func noEvent[T Event](t *testing.T, e *Engine, d time.Duration, pred func(T) bool) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case ev := <-e.Events():
			if v, ok := ev.(T); ok && (pred == nil || pred(v)) {
				t.Fatalf("unexpected %T: %+v", v, v)
			}
		case <-timeout:
			return
		}
	}
}

func isAutoForwardOf(port uint16) func(ForwardChanged) bool {
	return func(c ForwardChanged) bool {
		return c.Forward.Spec.Auto && c.Forward.Spec.RemotePort == port && c.Forward.State == Active
	}
}

func TestAutoForwardAfterDebounce(t *testing.T) {
	p := freePort(t)
	c := newFakeConn(listen(22, "sshd"))
	e := newEngineWith(t, autoOptions(dialTo(c)))
	e.Connect("devbox")
	on := next[AutoChanged](t, e, nil)
	if !on.On || on.Host != "devbox" {
		t.Errorf("AutoChanged = %+v, want on for devbox", on)
	}
	c.send(listen(22, "sshd"), listen(p, "jupyter-lab"))
	first := next(t, e, func(pc PortsChanged) bool { return len(pc.Ports) == 2 })
	if len(first.Added) != 0 {
		t.Errorf("first sighting Added = %v, want none yet", first.Added)
	}
	c.send(listen(22, "sshd"), listen(p, "jupyter-lab"))
	got := next(t, e, isAutoForwardOf(p))
	if got.Forward.Spec.LocalPort != p || got.Forward.Spec.Label != "Jupyter" {
		t.Errorf("auto forward = %+v, want local %d labeled Jupyter", got.Forward.Spec, p)
	}
}

func TestPortsCarryLabelsAndIgnored(t *testing.T) {
	c := newFakeConn(listen(22, "sshd") + listen(8000, "python3") + listen(5173, "node"))
	o := autoOptions(dialTo(c))
	o.Labels = map[uint16]string{8000: "API"}
	e := newEngineWith(t, o)
	e.Connect("devbox")
	pc := next(t, e, func(pc PortsChanged) bool { return len(pc.Ports) == 3 })
	byPort := map[uint16]Port{}
	for _, p := range pc.Ports {
		byPort[p.Port] = p
	}
	if !byPort[22].Ignored || byPort[8000].Ignored || byPort[5173].Ignored {
		t.Errorf("Ignored: 22=%v 8000=%v 5173=%v", byPort[22].Ignored, byPort[8000].Ignored, byPort[5173].Ignored)
	}
	if byPort[8000].Label != "API" || byPort[5173].Label != "Vite" {
		t.Errorf("labels: 8000=%q 5173=%q, want the user's API and the built-in Vite", byPort[8000].Label, byPort[5173].Label)
	}
}

func TestAutoForwardSkipsBaseline(t *testing.T) {
	p := freePort(t)
	c := newFakeConn(listen(p, "node"))
	e := newEngineWith(t, autoOptions(dialTo(c)))
	e.Connect("devbox")
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	noEvent[ForwardChanged](t, e, 300*time.Millisecond, nil)
}

func TestAutoForwardOffAndToggle(t *testing.T) {
	p := freePort(t)
	c := newFakeConn("") // an empty first snapshot is the baseline
	o := autoOptions(dialTo(c))
	o.Settings = func(HostID) HostSettings { return HostSettings{Auto: false, Filter: detect.DefaultFilter()} }
	e := newEngineWith(t, o)
	e.Connect("devbox")
	if on := next[AutoChanged](t, e, nil); on.On {
		t.Fatal("AutoChanged says on, settings say off")
	}
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	noEvent[ForwardChanged](t, e, 300*time.Millisecond, nil)

	e.SetAuto("devbox", true)
	if on := next[AutoChanged](t, e, nil); !on.On {
		t.Error("SetAuto(true) did not report on")
	}
	e.SetAuto("devbox", true) // no change: no event
	noEvent[AutoChanged](t, e, 200*time.Millisecond, nil)
	// Turning auto on doesn't sweep up ports that are already listed.
	noEvent[ForwardChanged](t, e, 200*time.Millisecond, nil)
}

func TestStoppedAutoForwardIsNotRecreated(t *testing.T) {
	p := freePort(t)
	c := newFakeConn("") // an empty first snapshot is the baseline
	e := newEngineWith(t, autoOptions(dialTo(c)))
	e.Connect("devbox")
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	f := next(t, e, isAutoForwardOf(p)).Forward
	if err := e.Stop(f.ID); err != nil {
		t.Fatal(err)
	}
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Stopped })
	// The port stays, then flaps away and back: nothing is re-forwarded.
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	c.send()
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	noEvent[ForwardChanged](t, e, 400*time.Millisecond, nil)

	// A manual forward clears the decline.
	if _, err := e.Forward("devbox", ForwardSpec{RemotePort: p}); err != nil {
		t.Fatalf("manual Forward after a stop: %v", err)
	}
}

func TestAutoForwardRespectsFilter(t *testing.T) {
	p, q := freePort(t), freePort(t)
	c := newFakeConn("") // an empty first snapshot is the baseline
	o := autoOptions(dialTo(c))
	o.Settings = func(HostID) HostSettings {
		f := detect.DefaultFilter()
		f.AutoPorts = detect.PortSet{{Lo: q, Hi: q}}
		return HostSettings{Auto: true, Filter: f}
	}
	e := newEngineWith(t, o)
	e.Connect("devbox")
	for range 2 {
		c.send(listen(p, "node"), listen(q, "node"), listen(2222, "sshd"))
	}
	got := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.Spec.Auto })
	if got.Forward.Spec.RemotePort != q {
		t.Errorf("auto-forwarded %d, want only %d (auto_ports)", got.Forward.Spec.RemotePort, q)
	}
	noEvent[ForwardChanged](t, e, 300*time.Millisecond, func(ch ForwardChanged) bool { return ch.Forward.Spec.RemotePort != q })
}

func holdLocal(t *testing.T, port uint16) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
}

func TestAutoForwardBusyPortUsesSuggested(t *testing.T) {
	p := freePort(t)
	holdLocal(t, p)
	c := newFakeConn("") // an empty first snapshot is the baseline
	e := newEngineWith(t, autoOptions(dialTo(c)))
	e.Connect("devbox")
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	got := next(t, e, isAutoForwardOf(p))
	if got.Forward.Spec.LocalPort == p || got.Forward.Spec.LocalPort == 0 {
		t.Errorf("LocalPort = %d, want a free port other than %d", got.Forward.Spec.LocalPort, p)
	}
}

func TestAutoForwardBusyPortRandom(t *testing.T) {
	p := freePort(t)
	holdLocal(t, p)
	c := newFakeConn("") // an empty first snapshot is the baseline
	o := autoOptions(dialTo(c))
	o.OnConflict = ConflictRandom
	e := newEngineWith(t, o)
	e.Connect("devbox")
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	if got := next(t, e, isAutoForwardOf(p)); got.Forward.Spec.LocalPort == p {
		t.Errorf("LocalPort = %d, want another port", got.Forward.Spec.LocalPort)
	}
}

func TestAutoForwardBusyPortFailIsDeclined(t *testing.T) {
	p := freePort(t)
	holdLocal(t, p)
	c := newFakeConn("") // an empty first snapshot is the baseline
	o := autoOptions(dialTo(c))
	o.OnConflict = ConflictFail
	e := newEngineWith(t, o)
	e.Connect("devbox")
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	failed := next[AutoForwardFailed](t, e, nil)
	var inUse *forward.PortInUseError
	if failed.Port != p || !errors.As(failed.Err, &inUse) {
		t.Errorf("AutoForwardFailed = %+v", failed)
	}
	// After a flap the declined port is not tried again.
	c.send()
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	noEvent[AutoForwardFailed](t, e, 300*time.Millisecond, nil)
}

func TestProbeSetsScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	remote := netip.MustParseAddrPort(srv.Listener.Addr().String()).Port()
	c := newFakeConn("") // an empty first snapshot is the baseline
	o := Options{Dial: dialTo(c), Interval: time.Second, HTTPProbe: true,
		Settings: func(HostID) HostSettings { return HostSettings{Auto: false, Filter: detect.DefaultFilter()} }}
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{RemotePort: remote, LocalPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := next(t, e, func(ch ForwardChanged) bool {
		return ch.Forward.ID == f.ID && ch.Forward.Scheme != forward.SchemeUnknown
	})
	if got.Forward.Scheme != forward.SchemeHTTP {
		t.Errorf("Scheme = %v, want http", got.Forward.Scheme)
	}
	if fs := e.Forwards(); len(fs) != 1 || fs[0].Scheme != forward.SchemeHTTP {
		t.Errorf("Forwards = %+v", fs)
	}
}

func TestProbeSkipsDatabases(t *testing.T) {
	var mu sync.Mutex
	dialed := 0
	c := newFakeConn("") // an empty first snapshot is the baseline
	o := Options{Dial: func(context.Context, HostID, sshx.HostKeyPrompter) (Conn, error) {
		return &countingConn{fakeConn: c, onDial: func() { mu.Lock(); dialed++; mu.Unlock() }}, nil
	}, Interval: time.Second, HTTPProbe: true,
		Settings: func(HostID) HostSettings { return HostSettings{Auto: false, Filter: detect.DefaultFilter()} }}
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{RemotePort: 5432, LocalPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	// Known not to speak HTTP from the start, so o and y don't treat it as
	// a web server.
	if f.Scheme != forward.SchemeNone {
		t.Errorf("Scheme = %v, want none", f.Scheme)
	}
	noEvent[ForwardChanged](t, e, 300*time.Millisecond, func(ch ForwardChanged) bool { return ch.Forward.Scheme != forward.SchemeNone })
	mu.Lock()
	defer mu.Unlock()
	if dialed != 0 {
		t.Errorf("the probe dialed Postgres %d times", dialed)
	}
}

type countingConn struct {
	*fakeConn
	onDial func()
}

func (c *countingConn) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c.onDial()
	return c.fakeConn.DialContext(ctx, network, addr)
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []string
}

func (n *fakeNotifier) Notify(_ context.Context, title, body string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, title+"|"+body)
	return nil
}

func (n *fakeNotifier) got() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.calls...)
}

func TestNotifierHearsAutoForwards(t *testing.T) {
	p := freePort(t)
	c := newFakeConn("") // an empty first snapshot is the baseline
	n := &fakeNotifier{}
	o := autoOptions(dialTo(c))
	o.Notifier = n
	e := newEngineWith(t, o)
	e.Connect("devbox")
	evil := "node\x1b]52;c;ZQ==\x07"
	c.send(listen(p, evil))
	c.send(listen(p, evil))
	next(t, e, isAutoForwardOf(p))
	deadline := time.Now().Add(5 * time.Second)
	for len(n.got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	calls := n.got()
	if len(calls) != 1 || !strings.Contains(calls[0], "devbox") || !strings.Contains(calls[0], itoa(p)) {
		t.Fatalf("Notify calls = %q, want one naming devbox and :%d", calls, p)
	}
	if strings.ContainsAny(calls[0], "\x1b\x07") {
		t.Errorf("notification text kept control characters: %q", calls[0])
	}
	// Manual forwards don't notify.
	if _, err := e.Forward("devbox", ForwardSpec{RemotePort: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if len(n.got()) != 1 {
		t.Errorf("a manual forward sent a notification: %q", n.got())
	}
}

// TestManualForwardOfAutoForwardedPort: enter on a port whose
// auto-forward the TUI hasn't shown yet gets that forward back, not a
// "port busy" conflict and a second forward on the next port.
func TestManualForwardOfAutoForwardedPort(t *testing.T) {
	p := freePort(t)
	c := newFakeConn("")
	e := newEngineWith(t, autoOptions(dialTo(c)))
	e.Connect("devbox")
	c.send(listen(p, "node"))
	c.send(listen(p, "node"))
	auto := next(t, e, isAutoForwardOf(p))

	f, err := e.Forward("devbox", ForwardSpec{RemotePort: p, LocalPort: p})
	if err != nil {
		t.Fatalf("Forward = %v; want the existing auto-forward", err)
	}
	if f.ID != auto.Forward.ID {
		t.Errorf("Forward returned %s, want the auto-forward %s", f.ID, auto.Forward.ID)
	}
	if n := len(e.Forwards()); n != 1 {
		t.Errorf("%d forwards, want 1", n)
	}
}
