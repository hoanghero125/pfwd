package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
)

// TestRedrawsOnlyOnEventsAndTheTick: the spec's re-render budget. The
// only timer is the 1 s tick for traffic rates, one chain of it, which
// events don't multiply; everything else redraws because an event came.
func TestRedrawsOnlyOnEventsAndTheTick(t *testing.T) {
	var ticks []time.Duration
	m := newModel(newFakeEngine())
	m.every = func(d time.Duration, _ func(time.Time) tea.Msg) tea.Cmd {
		ticks = append(ticks, d)
		return nil
	}
	m.Init()
	m = apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, OS: "Linux", Ports: []engine.Port{rp(5173, "node")}},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, OS: "Linux", Ports: []engine.Port{rp(5173, "node"), rp(8888, "jupyter-lab")}},
	)
	if len(ticks) != 1 {
		t.Fatalf("timers after start and three events: %v, want one 1 s tick", ticks)
	}
	// A tick reads the counters; their arrival schedules the next tick.
	for range 3 {
		next, read := m.Update(tickMsg(t0))
		next, _ = next.Update(read())
		m = next.(Model) //nolint:errcheck,forcetypeassert // Update returns a Model
	}
	if len(ticks) != 4 {
		t.Errorf("timers after three ticks: %v, want each tick to schedule exactly the next", ticks)
	}
	for _, d := range ticks {
		if d != time.Second {
			t.Errorf("a timer of %v; the only timer is the 1 s tick", d)
		}
	}
}
