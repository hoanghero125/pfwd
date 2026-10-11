package tui

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// fakeEngine records calls and returns forwards with sequential IDs.
type fakeEngine struct {
	events chan engine.Event

	mu         sync.Mutex
	forwardErr error
	forwards   []engine.Forward
	specs      []engine.ForwardSpec
	stopped    []string
	nextID     int
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{events: make(chan engine.Event, 16)}
}

func (f *fakeEngine) Events() <-chan engine.Event { return f.events }

func (f *fakeEngine) Forward(host engine.HostID, spec engine.ForwardSpec) (engine.Forward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	if err := f.forwardErr; err != nil {
		f.forwardErr = nil
		return engine.Forward{}, err
	}
	if spec.LocalPort == 0 {
		spec.LocalPort = spec.RemotePort
	}
	if spec.BindAddr == "" {
		spec.BindAddr = "127.0.0.1"
	}
	f.nextID++
	fw := engine.Forward{ID: itoa(f.nextID), Host: host, Spec: spec, State: engine.Active}
	f.forwards = append(f.forwards, fw)
	return fw, nil
}

func (f *fakeEngine) Stop(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	for i, fw := range f.forwards {
		if fw.ID == id {
			f.forwards = append(f.forwards[:i], f.forwards[i+1:]...)
			return nil
		}
	}
	return errors.New("no such forward")
}

func (f *fakeEngine) Forwards() []engine.Forward {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]engine.Forward(nil), f.forwards...)
}

var t0 = time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

func newModel(e Engine) Model {
	m := New(e, "devbox")
	m.now = func() time.Time { return t0 }
	// Tests send tickMsg themselves; a real tick would loop forever.
	m.every = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	return m
}

// key builds a key press the way Bubble Tea reports it.
func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	k := tea.KeyPressMsg{Code: r, Text: s}
	if r >= 'A' && r <= 'Z' {
		k.Code = r + ('a' - 'A')
		k.Mod = tea.ModShift
	}
	return k
}

// update feeds msg to the model and runs the commands it returns, feeding
// their results back, except blocking event waits. It reports whether
// the model asked to quit.
func update(t *testing.T, m Model, msg tea.Msg) (Model, bool) {
	t.Helper()
	next, cmd := m.Update(msg)
	m = mustModel(next)
	return run(t, m, cmd)
}

func run(t *testing.T, m Model, cmd tea.Cmd) (Model, bool) {
	t.Helper()
	if cmd == nil {
		return m, false
	}
	switch msg := cmd().(type) {
	case nil:
		return m, false
	case tea.QuitMsg:
		return m, true
	case tea.BatchMsg:
		quit := false
		for _, c := range msg {
			if isBlocking(c) {
				continue
			}
			var q bool
			m, q = run(t, m, c)
			quit = quit || q
		}
		return m, quit
	default:
		return update(t, m, msg)
	}
}

// isBlocking is never true for the commands keys produce; event waits are
// only returned from event handling, which tests drive with apply.
func isBlocking(tea.Cmd) bool { return false }

// mustModel unwraps the tea.Model that Update returns.
func mustModel(tm tea.Model) Model {
	m, ok := tm.(Model)
	if !ok {
		panic("Update returned a different model type")
	}
	return m
}

func press(t *testing.T, m Model, keys ...string) (Model, bool) {
	t.Helper()
	quit := false
	for _, k := range keys {
		var q bool
		m, q = update(t, m, key(k))
		quit = quit || q
	}
	return m, quit
}

// apply feeds engine events the way the event loop would, without
// running the next blocking wait.
func apply(m Model, evs ...engine.Event) Model {
	for _, ev := range evs {
		next, _ := m.Update(eventMsg{ev})
		m = mustModel(next)
	}
	return m
}

func connectedWith(m Model, ports ...detect.RemotePort) Model {
	return apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, OS: "Linux", Ports: ports},
	)
}

func rp(port uint16, process string, addrs ...string) detect.RemotePort {
	if len(addrs) == 0 {
		addrs = []string{"0.0.0.0"}
	}
	return detect.RemotePort{Port: port, Addrs: addrs, Process: process}
}

func view(m Model) string {
	return ansi.Strip(m.View().Content)
}

func TestEnterForwardsDetectedPort(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(6006, "tensorboard", "::1"), rp(8123, "python3"))
	m, _ = press(t, m, "down", "enter")
	if len(e.specs) != 1 {
		t.Fatalf("Forward calls = %+v", e.specs)
	}
	if got, want := e.specs[0], (engine.ForwardSpec{RemotePort: 8123, LocalPort: 8123, RemoteHost: "127.0.0.1"}); got != want {
		t.Errorf("spec = %+v, want %+v", got, want)
	}
	if v := view(m); !strings.Contains(v, "Forwarded :8123 to localhost:8123") || !strings.Contains(v, "localhost:8123") {
		t.Errorf("view after forwarding:\n%s", v)
	}

	// The cursor followed the new forward to the top; the IPv6-only
	// listener is now the last row. It is dialed on ::1, and f works like
	// enter.
	_, _ = press(t, m, "G", "f")
	if got := e.specs[1]; got.RemotePort != 6006 || got.RemoteHost != "::1" {
		t.Errorf("second spec = %+v", got)
	}
}

func TestEnterStopsForwardedPort(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	// The forwarded row is now first; enter on it stops it.
	m, _ = press(t, m, "g", "enter")
	if len(e.stopped) != 1 || e.stopped[0] != "1" {
		t.Fatalf("Stop calls = %v", e.stopped)
	}
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "1", Host: "devbox", Spec: engine.ForwardSpec{RemotePort: 8123, LocalPort: 8123}, State: engine.Stopped}})
	if v := view(m); !strings.Contains(v, "Stopped :8123") || strings.Contains(v, "localhost:8123") {
		t.Errorf("view after stop:\n%s", v)
	}
}

func TestXStops(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(8123, "python3"))
	m, _ = press(t, m, "x")
	if len(e.stopped) != 0 || !strings.Contains(view(m), "not forwarded") {
		t.Errorf("x on a detected port: stops=%v view:\n%s", e.stopped, view(m))
	}
	_, _ = press(t, m, "enter", "g", "x")
	if len(e.stopped) != 1 {
		t.Errorf("x on a forward did not stop it: %v", e.stopped)
	}
}

func TestCustomPortModal(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"))
	m, _ = press(t, m, "F")
	if !strings.Contains(view(m), "Forward :5173 to another local port") {
		t.Fatalf("modal not shown:\n%s", view(m))
	}
	for _, bad := range [][]string{{"0"}, {"7", "0", "0", "0", "0"}} {
		m, _ = press(t, m, bad...)
		m, _ = press(t, m, "enter")
		if len(e.specs) != 0 || !strings.Contains(view(m), "Enter a port from 1 to 65535") {
			t.Fatalf("input %v accepted:\n%s", bad, view(m))
		}
		for range bad {
			m, _ = press(t, m, "backspace")
		}
	}
	m, _ = press(t, m, "a", "b", "c")
	if strings.Contains(view(m), "abc") {
		t.Error("letters were typed into the port field")
	}
	m, _ = press(t, m, "9", "0", "0", "0", "enter")
	if len(e.specs) != 1 || e.specs[0].LocalPort != 9000 || e.specs[0].RemotePort != 5173 {
		t.Fatalf("specs = %+v, want local 9000 for remote 5173", e.specs)
	}
	if strings.Contains(view(m), "another local port") {
		t.Error("modal still open after forwarding")
	}
}

func TestCustomPortModalEscCancels(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"))
	m, _ = press(t, m, "F", "1", "esc")
	if strings.Contains(view(m), "another local port") || len(e.specs) != 0 {
		t.Errorf("esc did not cancel: specs=%v\n%s", e.specs, view(m))
	}
}

func TestConflictModal(t *testing.T) {
	e := newFakeEngine()
	e.forwardErr = &forward.PortInUseError{Addr: "127.0.0.1", Port: 8123, Suggested: 8124}
	m := connectedWith(newModel(e), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	if v := view(m); !strings.Contains(v, "Port 8123 is busy on this machine") || !strings.Contains(v, "use localhost:8124") {
		t.Fatalf("conflict modal not shown:\n%s", v)
	}
	m, _ = press(t, m, "enter")
	if len(e.specs) != 2 || e.specs[1].LocalPort != 8124 || e.specs[1].RemotePort != 8123 {
		t.Fatalf("specs = %+v, want a retry on 8124", e.specs)
	}
	if !strings.Contains(view(m), "Forwarded :8123 to localhost:8124") {
		t.Errorf("view:\n%s", view(m))
	}
}

func TestConflictModalOtherPortAndNoSuggestion(t *testing.T) {
	e := newFakeEngine()
	e.forwardErr = &forward.PortInUseError{Addr: "127.0.0.1", Port: 8123}
	m := connectedWith(newModel(e), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	if v := view(m); strings.Contains(v, "use localhost") {
		t.Errorf("offered a suggestion when there is none:\n%s", v)
	}
	m, _ = press(t, m, "enter") // no suggestion: enter does nothing
	if len(e.specs) != 1 {
		t.Fatalf("enter without a suggestion forwarded: %+v", e.specs)
	}
	_, _ = press(t, m, "F", "8", "2", "0", "0", "enter")
	if len(e.specs) != 2 || e.specs[1].LocalPort != 8200 {
		t.Errorf("specs = %+v", e.specs)
	}
}

func TestForwardErrorShownInStatus(t *testing.T) {
	e := newFakeEngine()
	e.forwardErr = errors.New("not connected to devbox")
	m := connectedWith(newModel(e), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	if !strings.Contains(view(m), "✗ not connected to devbox") {
		t.Errorf("view:\n%s", view(m))
	}
}

func TestHostKeyModalAnswers(t *testing.T) {
	for _, c := range []struct {
		key  string
		want bool
	}{{"y", true}, {"n", false}, {"esc", false}} {
		t.Run(c.key, func(t *testing.T) {
			reply := make(chan bool, 1)
			m := apply(newModel(newFakeEngine()), engine.SessionChanged{Host: "devbox", State: engine.Connecting},
				engine.NewHostKeyPrompt("devbox", sshx.HostKeyQuestion{Host: "devbox", KeyType: "ssh-ed25519", Fingerprint: "SHA256:abc"}, reply))
			if v := view(m); !strings.Contains(v, "First connection to devbox") || !strings.Contains(v, "SHA256:abc") {
				t.Fatalf("modal:\n%s", v)
			}
			m, _ = press(t, m, "x") // other keys do nothing
			select {
			case <-reply:
				t.Fatal("answered on an unrelated key")
			default:
			}
			m, _ = press(t, m, c.key)
			select {
			case got := <-reply:
				if got != c.want {
					t.Errorf("answer = %v, want %v", got, c.want)
				}
			default:
				t.Fatal("no answer sent")
			}
			if strings.Contains(view(m), "First connection") {
				t.Error("modal still open")
			}
		})
	}
}

func TestQuitConfirmWhenActive(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	m, quit := press(t, m, "q")
	if quit || !strings.Contains(view(m), "1 forward is active") {
		t.Fatalf("q with an active forward: quit=%v\n%s", quit, view(m))
	}
	m, quit = press(t, m, "n")
	if quit || strings.Contains(view(m), "Quit pfwd?") {
		t.Fatalf("n did not stay: quit=%v", quit)
	}
	_, quit = press(t, m, "ctrl+c", "y")
	if !quit {
		t.Error("ctrl+c then y did not quit")
	}
}

func TestQuitImmediatelyWhenIdle(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(8123, "python3"))
	if _, quit := press(t, m, "q"); !quit {
		t.Error("q without forwards did not quit")
	}
	if _, quit := press(t, newModel(newFakeEngine()), "ctrl+c"); !quit {
		t.Error("ctrl+c while connecting did not quit")
	}
}

func TestHelpModal(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(8123, "python3"))
	m, _ = press(t, m, "?")
	if !strings.Contains(view(m), "forward to another local port") {
		t.Fatalf("help not shown:\n%s", view(m))
	}
	m, _ = press(t, m, "?")
	if strings.Contains(view(m), "forward to another local port") {
		t.Error("? did not close help")
	}
}

func TestCursorStaysOnPort(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"), rp(8123, "python3"))
	m, _ = press(t, m, "down") // on 8123
	m = apply(m, engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []detect.RemotePort{rp(3000, "ruby"), rp(5173, "node"), rp(8123, "python3")}})
	_, _ = press(t, m, "enter")
	if len(e.specs) != 1 || e.specs[0].RemotePort != 8123 {
		t.Errorf("cursor moved off 8123: specs = %+v", e.specs)
	}
}

func TestCursorClampsWhenRowsShrink(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"), rp(8123, "python3"), rp(9000, "x"))
	m, _ = press(t, m, "G")
	m = apply(m, engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []detect.RemotePort{rp(5173, "node")}})
	_, _ = press(t, m, "enter")
	if len(e.specs) != 1 || e.specs[0].RemotePort != 5173 {
		t.Errorf("specs = %+v", e.specs)
	}
}

func TestStatusExpires(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	if !strings.Contains(view(m), "Forwarded :8123") {
		t.Fatal("no status")
	}
	m.now = func() time.Time { return t0.Add(6 * time.Second) }
	if strings.Contains(view(m), "Forwarded :8123") {
		t.Error("status still shown after 5 s")
	}
}

func TestErrAfterFailedConnect(t *testing.T) {
	authErr := &sshx.Error{Kind: sshx.ErrAuth, Msg: "authentication to devbox as alice failed"}
	m := apply(newModel(newFakeEngine()), engine.SessionChanged{Host: "devbox", State: engine.SessionFailed, Err: authErr})
	if !errors.Is(m.Err(), sshx.ErrAuth) {
		t.Errorf("Err = %v", m.Err())
	}
	if v := view(m); !strings.Contains(v, "Can't connect to devbox") || !strings.Contains(v, "alice") {
		t.Errorf("view:\n%s", v)
	}
	if newModel(newFakeEngine()).Err() != nil {
		t.Error("Err before any failure")
	}
}

func TestStatsTickShowsTrafficAndConns(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"))
	m, _ = press(t, m, "enter")
	e.mu.Lock()
	e.forwards[0].Stats = forward.Stats{Conns: 2, BytesIn: 0, BytesOut: 0}
	e.mu.Unlock()
	m, _ = update(t, m, tickMsg(t0))
	e.mu.Lock()
	e.forwards[0].Stats = forward.Stats{Conns: 2, BytesIn: 12 * 1024, BytesOut: 1024}
	e.mu.Unlock()
	m, _ = update(t, m, tickMsg(t0.Add(time.Second)))
	if v := view(m); !strings.Contains(v, "↓12K/s ↑1.0K/s") {
		t.Errorf("traffic not shown:\n%s", v)
	}
}

func TestEventsClosedQuits(t *testing.T) {
	m := newModel(newFakeEngine())
	if _, quit := update(t, m, eventsClosedMsg{}); !quit {
		t.Error("the model kept running after the engine closed its events")
	}
}

func TestFormatRate(t *testing.T) {
	cases := map[float64]string{0: "0", 512: "512", 1024: "1.0K", 12 * 1024: "12K", 1536 * 1024: "1.5M", 300 * 1024 * 1024: "300M", 5 * 1024 * 1024 * 1024: "5.0G"}
	for in, want := range cases {
		if got := formatRate(in); got != want {
			t.Errorf("formatRate(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestViewFitsWindow(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 12}, {140, 40}, {50, 10}} {
		m := connectedWith(newModel(newFakeEngine()), rp(22, ""), rp(5173, "a-very-long-process-name-that-will-not-fit-anywhere"), rp(8123, "python3"))
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = mustModel(next)
		lines := strings.Split(view(m), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d wide: %q", size[0], size[1], i, w, l)
			}
		}
	}
}
