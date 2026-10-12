//go:build e2e

package e2e

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/tui"
)

// proxiedHost returns a HostConfig for service that goes through a proxy
// the test can cut.
func proxiedHost(t *testing.T, service string) (*dropProxy, engine.DialFunc) {
	t.Helper()
	hc := newHost(t, service)
	p := startProxy(t, "127.0.0.1:"+strconv.Itoa(hc.Port))
	hc.Port = p.port()
	return p, dialer(hc)
}

// manualEngine is an engine with auto-forward off that retries quickly.
func manualEngine(dial engine.DialFunc) *engine.Engine {
	settings := engine.DefaultSettings()
	settings.Auto = false
	return engine.New(engine.Options{
		Interval:   time.Second,
		Dial:       dial,
		MaxBackoff: 2 * time.Second,
		Settings:   func(engine.HostID) engine.HostSettings { return settings },
	})
}

// forwardOf returns the engine's forward of a remote port.
func forwardOf(eng *engine.Engine, port uint16) (engine.Forward, bool) {
	for _, f := range eng.Forwards() {
		if f.Spec.RemotePort == port {
			return f, true
		}
	}
	return engine.Forward{}, false
}

// waitState runs the driver until the forward of port is in state, or
// fails after timeout.
func waitState(t *testing.T, d *driver, eng *engine.Engine, port uint16, state engine.ForwardState, timeout time.Duration) engine.Forward {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f, ok := forwardOf(eng, port); ok && f.State == state {
			return f
		}
		d.runFor(100 * time.Millisecond)
	}
	f, _ := forwardOf(eng, port)
	t.Fatalf("forward of :%d is %v, want %v after %v:\n%s", port, f.State, state, timeout, d.view())
	return engine.Forward{}
}

// TestE2EReconnect: the connection drops for a few seconds; the forward
// pauses, keeps its local port, and serves again after the reconnect.
func TestE2EReconnect(t *testing.T) {
	proxy, dial := proxiedHost(t, "ss")
	eng := manualEngine(dial)
	defer eng.Close()
	host := engine.HostID("ss")
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)
	d.waitFor(t, "ignored hidden", 20*time.Second)

	startServer(t, "ss", "8124")
	d.waitFor(t, ":8124", 5*time.Second)
	d.selectPort(t, "8124")
	d.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	d.waitFor(t, "Forwarded :8124 to localhost:8124", 5*time.Second)
	get(t, "8124")

	proxy.drop(3 * time.Second)
	d.waitFor(t, "reconnecting", 10*time.Second)
	if f := waitState(t, d, eng, 8124, engine.Paused, 5*time.Second); f.Pause != engine.Disconnected {
		t.Errorf("Pause = %v, want Disconnected", f.Pause)
	}
	took := d.waitFor(t, "Reconnected to ss", 30*time.Second)
	t.Logf("reconnected %v after the outage began", took.Round(time.Millisecond))
	waitState(t, d, eng, 8124, engine.Active, 10*time.Second)
	get(t, "8124")
}

// TestE2EPausedWhenServerStops: the server behind a forward stops; the
// forward pauses until it starts again.
func TestE2EPausedWhenServerStops(t *testing.T) {
	eng := manualEngine(dialer(newHost(t, "ss")))
	defer eng.Close()
	host := engine.HostID("ss")
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)
	d.waitFor(t, "ignored hidden", 20*time.Second)

	startServer(t, "ss", "8125")
	d.waitFor(t, ":8125", 5*time.Second)
	d.selectPort(t, "8125")
	d.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	d.waitFor(t, "Forwarded :8125 to localhost:8125", 5*time.Second)

	if out, err := compose("exec", "ss", "pkill", "-f", "http.server 8125"); err != nil {
		t.Fatalf("stop the server: %v\n%s", err, out)
	}
	if f := waitState(t, d, eng, 8125, engine.Paused, 10*time.Second); f.Pause != engine.PortGone {
		t.Errorf("Pause = %v, want PortGone", f.Pause)
	}
	d.waitFor(t, "port closed", 5*time.Second)

	startServer(t, "ss", "8125")
	waitState(t, d, eng, 8125, engine.Active, 10*time.Second)
	get(t, "8125")
}
