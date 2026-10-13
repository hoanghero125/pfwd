package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// TestBackgroundEventKeepsCursor: an event from a background host leaves
// the current tab's cursor, and so what enter does, alone.
func TestBackgroundEventKeepsCursor(t *testing.T) {
	e := newFakeEngine()
	m := twoHosts(e, Options{})
	m = apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{rp(5173, "node"), rp(6379, "redis")}},
		engine.SessionChanged{Host: "gpu", State: engine.Connected},
	)
	// forward 5173 on devbox, then select 6379
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "down")
	before := selectedLine(m)
	m = apply(m, engine.PortsChanged{Host: "gpu", Strategy: detect.SS, Ports: []engine.Port{rp(8888, "jupyter")}})
	after := selectedLine(m)
	if !strings.Contains(after, ":6379") {
		t.Errorf("gpu's event moved devbox's cursor: before %q after %q", before, after)
	}
	// What enter now does
	n := len(e.stopped)
	_, _ = press(t, m, "enter")
	if len(e.stopped) > n {
		t.Errorf("enter after gpu's event stopped forward %v instead of forwarding :6379", e.stopped)
	}
}

// TestBackgroundSessionKeepsCursor: a background host's session event leaves the cursor alone.
func TestBackgroundSessionKeepsCursor(t *testing.T) {
	m := twoHosts(newFakeEngine(), Options{})
	m = apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{rp(5173, "node"), rp(6379, "redis"), rp(8080, "x")}},
	)
	m, _ = press(t, m, "down", "down")
	before := selectedLine(m)
	m = apply(m, engine.SessionChanged{Host: "gpu", State: engine.Connected})
	after := selectedLine(m)
	if before != after {
		t.Errorf("gpu SessionChanged moved devbox cursor: %q -> %q", before, after)
	}
}

// TestFailedHostKeepsOtherHostsPrompts: one host's failed session drops
// only its own queued prompts.
func TestFailedHostKeepsOtherHostsPrompts(t *testing.T) {
	m := NewHosts(newFakeEngine(), []engine.HostID{"a", "b", "c"}, Options{})
	m.now = func() time.Time { return t0 }
	ra, rb, rc := make(chan engine.CredentialAnswer, 1), make(chan engine.CredentialAnswer, 1), make(chan engine.CredentialAnswer, 1)
	m = apply(m,
		engine.NewCredentialPrompt("a", sshx.CredentialQuestion{Host: "a", Kind: sshx.Password, Prompt: "u"}, ra),
		engine.NewCredentialPrompt("b", sshx.CredentialQuestion{Host: "b", Kind: sshx.Password, Prompt: "u"}, rb),
		engine.NewCredentialPrompt("c", sshx.CredentialQuestion{Host: "c", Kind: sshx.Password, Prompt: "u"}, rc),
	)
	// a's modal open; the server drops a (LoginGraceTime) -> SessionFailed for a
	m = apply(m, engine.SessionChanged{Host: "a", State: engine.SessionFailed})
	// answer b
	m, _ = press(t, m, "x", "enter")
	select {
	case <-rb:
	default:
		t.Log("b got no answer")
	}
	v := view(m)
	if !strings.Contains(v, "u@c") {
		select {
		case <-rc:
		default:
			t.Errorf("c's queued credential prompt vanished without an answer; c waits forever:\n%s", v)
		}
	}
}

// TestAllViewReverseUsesRowHost: r on gpu's row in the all view exposes
// on gpu.
func TestAllViewReverseUsesRowHost(t *testing.T) {
	e := newFakeEngine()
	opts := Options{LocalPorts: func(context.Context) ([]detect.RemotePort, error) {
		return []detect.RemotePort{{Port: 3000, Addrs: []string{"127.0.0.1"}, Process: "web"}}, nil
	}}
	m := bothConnected(twoHosts(e, opts))
	m, _ = press(t, m, "ctrl+a")
	for range 5 {
		if strings.Contains(selectedLine(m), ":8888") {
			break
		}
		m, _ = press(t, m, "down")
	}
	m, _ = press(t, m, "r")
	_, _ = press(t, m, "enter")
	if len(e.hosts) != 1 || e.hosts[0] != "gpu" {
		t.Errorf("r on gpu's row exposed on %v", e.hosts)
	}
}

// TestEventsBeforeTabAreKept: events for a host with no tab yet (the
// daemon's, in attach) are kept, and its tab shows them.
func TestEventsBeforeTabAreKept(t *testing.T) {
	opts := Options{
		Hosts:   func(context.Context) ([]HostChoice, error) { return []HostChoice{{Name: "web"}}, nil },
		Connect: func(context.Context, string) (engine.HostID, error) { return "web", nil },
	}
	m := bothConnected(twoHosts(newFakeEngine(), opts))
	m = apply(m,
		engine.SessionChanged{Host: "web", State: engine.Connected},
		engine.AutoChanged{Host: "web", On: true},
		engine.ForwardChanged{Forward: engine.Forward{ID: "77", Host: "web", State: engine.Active, Spec: engine.ForwardSpec{RemotePort: 9000, LocalPort: 9000}}},
	)
	m, _ = press(t, m, "tab", "tab")
	v := view(m)
	if !strings.Contains(v, ":9000") || !strings.Contains(v, "connected") {
		t.Errorf("web's tab is stale:\n%s", v)
	}
}

// TestForwardResultFromOtherTabKeepsCursor: a forward result for another
// tab leaves the cursor alone.
func TestForwardResultFromOtherTabKeepsCursor(t *testing.T) {
	e := newFakeEngine()
	m := twoHosts(e, Options{})
	m = apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{rp(5173, "node"), rp(6379, "redis")}},
		engine.SessionChanged{Host: "gpu", State: engine.Connected},
		engine.PortsChanged{Host: "gpu", Strategy: detect.SS, Ports: []engine.Port{rp(8888, "j")}},
	)
	m, _ = press(t, m, "down")
	before := selectedLine(m)
	// a forward result from gpu arrives while devbox is current
	f, _ := e.Forward("gpu", engine.ForwardSpec{RemotePort: 8888, LocalPort: 8888})
	m, _ = update(t, m, forwardResultMsg{host: "gpu", spec: f.Spec, fwd: f})
	after := selectedLine(m)
	if before != after {
		t.Errorf("gpu's forward result moved devbox's cursor: %q -> %q", before, after)
	}
}

// TestAllViewCursorFollowsItsHost: in the all view, a row that changes
// group keeps the cursor on its own host's row.
func TestAllViewCursorFollowsItsHost(t *testing.T) {
	e := newFakeEngine()
	m := twoHosts(e, Options{})
	m = apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{rp(3000, "node")}},
		engine.SessionChanged{Host: "gpu", State: engine.Connected},
		engine.PortsChanged{Host: "gpu", Strategy: detect.SS, Ports: []engine.Port{rp(3000, "node")}},
	)
	m, _ = press(t, m, "ctrl+a", "down")
	// gpu auto-forwards its 3000
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "9", Host: "gpu", State: engine.Active, Spec: engine.ForwardSpec{RemotePort: 3000, LocalPort: 3001, Auto: true}}})
	sel := selectedLine(m)
	if !strings.Contains(sel, "gpu") {
		t.Errorf("cursor left gpu's row for another host's: %q\n%s", sel, view(m))
	}
}

func TestAllViewCursorStaysOnItsHostsRow(t *testing.T) {
	e := newFakeEngine()
	m := twoHosts(e, Options{})
	m = apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{rp(3000, "node")}},
		engine.ForwardChanged{Forward: engine.Forward{ID: "1", Host: "devbox", State: engine.Active, Spec: engine.ForwardSpec{RemotePort: 3000, LocalPort: 3000}}},
		engine.SessionChanged{Host: "gpu", State: engine.Connected},
		engine.PortsChanged{Host: "gpu", Strategy: detect.SS, Ports: []engine.Port{rp(3000, "node")}},
	)
	m, _ = press(t, m, "ctrl+a", "down")
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "9", Host: "gpu", State: engine.Active, Spec: engine.ForwardSpec{RemotePort: 3000, LocalPort: 3001, Auto: true}}})
	sel := selectedLine(m)
	if !strings.Contains(sel, "gpu") {
		t.Errorf("cursor left gpu's row for another host's: %q", sel)
	}
}
