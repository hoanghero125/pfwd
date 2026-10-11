package tui

import (
	"context"
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
)

// withLabel is a detected port with a label, as the engine reports it.
func withLabel(port uint16, process, label string) engine.Port {
	p := rp(port, process)
	p.Label = label
	return p
}

func ignored(port uint16, process string) engine.Port {
	p := rp(port, process)
	p.Ignored = true
	return p
}

// sized applies a window size.
func sized(m Model, w, h int) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return mustModel(next)
}

// lineWith returns the first view line containing s.
func lineWith(t *testing.T, m Model, s string) string {
	t.Helper()
	for _, l := range strings.Split(view(m), "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	t.Fatalf("no line contains %q:\n%s", s, view(m))
	return ""
}

func TestLabelAndSchemeColumn(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), withLabel(5173, "node", "Vite"), withLabel(6379, "redis-server", "Redis"))
	m, _ = press(t, m, "enter")
	fw := e.Forwards()[0]
	fw.Scheme = forward.SchemeHTTPS
	fw.Spec.Label = "Vite"
	m = apply(m, engine.ForwardChanged{Forward: fw})
	if l := lineWith(t, m, ":5173"); !strings.Contains(l, "Vite https") {
		t.Errorf("forward row = %q; want the label and the scheme", l)
	}
	if l := lineWith(t, m, ":6379"); !strings.Contains(l, "Redis") {
		t.Errorf("detected row = %q; want its label", l)
	}
	if h := lineWith(t, m, "STATE"); !strings.Contains(h, "LABEL") {
		t.Errorf("header = %q; want LABEL", h)
	}
}

// TestLongLabelKeepsScheme: a label too long for the column is cut before
// the scheme, so the scheme always shows.
func TestLongLabelKeepsScheme(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), withLabel(6006, "tensorboard", "TensorBoard on the GPU node"))
	m, _ = press(t, m, "enter")
	fw := e.Forwards()[0]
	fw.Scheme = forward.SchemeHTTP
	m = apply(m, engine.ForwardChanged{Forward: fw})
	if l := lineWith(t, m, ":6006"); !strings.Contains(l, "… http") {
		t.Errorf("row = %q; want the label cut before \"http\"", l)
	}
}

func TestColumnsDropInOrder(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), withLabel(5173, "node", "Vite"))
	for _, tc := range []struct {
		width     int
		want, not []string
	}{
		{140, []string{"PROCESS", "LABEL", "CONN", "TRAFFIC"}, nil},
		{80, []string{"PROCESS", "LABEL", "CONN", "TRAFFIC"}, nil},
		{76, []string{"PROCESS", "LABEL", "CONN"}, []string{"TRAFFIC"}},
		{61, []string{"PROCESS", "LABEL"}, []string{"CONN", "TRAFFIC"}},
	} {
		h := lineWith(t, sized(m, tc.width, 24), "STATE")
		for _, w := range tc.want {
			if !strings.Contains(h, w) {
				t.Errorf("%d columns: header %q lacks %s", tc.width, h, w)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(h, n) {
				t.Errorf("%d columns: header %q still has %s", tc.width, h, n)
			}
		}
	}
}

// TestCellsNeverTouch pins ux-4: a cut value keeps a space before the
// next column.
func TestCellsNeverTouch(t *testing.T) {
	e := newFakeEngine()
	m := sized(connectedWith(newModel(e), rp(5173, "a-very-long-process-name")), 80, 24)
	m, _ = press(t, m, "enter")
	l := lineWith(t, m, ":5173")
	if !strings.Contains(l, "… ") {
		t.Errorf("row = %q; want the cut process name followed by a space", l)
	}
}

func TestAutoKey(t *testing.T) {
	e := newFakeEngine()
	m := apply(connectedWith(newModel(e), rp(5173, "node")), engine.AutoChanged{Host: "devbox", On: true})
	if bar := lineWith(t, m, "pfwd"); !strings.Contains(bar, "auto: on") {
		t.Errorf("host bar = %q; want auto: on", bar)
	}
	m, _ = press(t, m, "a")
	if got := e.autoCalls(); len(got) != 1 || got[0] {
		t.Fatalf("SetAuto calls = %v; want one call turning it off", got)
	}
	m = apply(m, engine.AutoChanged{Host: "devbox", On: false})
	if bar := lineWith(t, m, "pfwd"); !strings.Contains(bar, "auto: off") {
		t.Errorf("host bar = %q; want auto: off", bar)
	}
	if !strings.Contains(view(m), "Auto-forward off for devbox") {
		t.Errorf("no status for turning auto-forward off:\n%s", view(m))
	}
}

func TestAutoForwardNotice(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), withLabel(5173, "node", "Vite"))
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{
		ID: "1", Host: "devbox", State: engine.Active,
		Spec: engine.ForwardSpec{BindAddr: "127.0.0.1", LocalPort: 5174, RemoteHost: "127.0.0.1", RemotePort: 5173, Auto: true},
	}})
	if !strings.Contains(view(m), "✓ Forwarded :5173 to localhost:5174 (auto; 5173 was busy)") {
		t.Errorf("no auto-forward notice:\n%s", view(m))
	}
	m = apply(m, engine.AutoForwardFailed{Host: "devbox", Port: 8080, Err: errors.New("port 8080 is busy on this machine")})
	if !strings.Contains(view(m), "✗ Couldn't auto-forward :8080: port 8080 is busy") {
		t.Errorf("no auto-forward failure notice:\n%s", view(m))
	}
}

// opened records what o and y hand to the desktop.
type opened struct {
	mu   sync.Mutex
	urls []string
	err  error
}

func (o *opened) open(_ context.Context, u string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.urls = append(o.urls, u)
	return o.err
}

func forwardedWith(t *testing.T, opts Options, scheme forward.Scheme) (Model, *fakeEngine) {
	t.Helper()
	e := newFakeEngine()
	m := connectedWith(newModelWith(e, opts), withLabel(5173, "node", "Vite"), withLabel(5432, "postgres", "Postgres"))
	m, _ = press(t, m, "enter") // 5173, the first row
	fw := e.Forwards()[0]
	fw.Scheme = scheme
	return apply(m, engine.ForwardChanged{Forward: fw}), e
}

func TestOpenKey(t *testing.T) {
	for _, tc := range []struct {
		scheme forward.Scheme
		want   string
	}{{forward.SchemeHTTPS, "https://localhost:5173"}, {forward.SchemeHTTP, "http://localhost:5173"}, {forward.SchemeUnknown, "http://localhost:5173"}} {
		o := &opened{}
		m, _ := forwardedWith(t, Options{Open: o.open}, tc.scheme)
		m, _ = press(t, m, "g", "o")
		if len(o.urls) != 1 || o.urls[0] != tc.want {
			t.Errorf("scheme %v: opened %q, want %q", tc.scheme, o.urls, tc.want)
		}
		if !strings.Contains(view(m), "Opening "+tc.want) {
			t.Errorf("scheme %v: no status:\n%s", tc.scheme, view(m))
		}
	}

	o := &opened{}
	m, _ := forwardedWith(t, Options{Open: o.open}, forward.SchemeNone)
	m, _ = press(t, m, "g", "o")
	if len(o.urls) != 0 || !strings.Contains(view(m), ":5173 doesn't speak HTTP") {
		t.Errorf("o on a non-HTTP forward: opened %q\n%s", o.urls, view(m))
	}
	m, _ = press(t, m, "G", "o")
	if len(o.urls) != 0 || !strings.Contains(view(m), ":5432 is not forwarded; press enter first") {
		t.Errorf("o on a detected port: opened %q\n%s", o.urls, view(m))
	}

	o = &opened{err: errors.New("xdg-open is not installed; install xdg-utils, or open http://localhost:5173 yourself")}
	m, _ = forwardedWith(t, Options{Open: o.open}, forward.SchemeHTTP)
	m, _ = press(t, m, "g", "o")
	if !strings.Contains(view(m), "✗ xdg-open is not installed") {
		t.Errorf("an opener error is not shown:\n%s", view(m))
	}
}

func TestCopyKey(t *testing.T) {
	c := &opened{}
	m, _ := forwardedWith(t, Options{Copy: c.open}, forward.SchemeHTTP)
	m, _ = press(t, m, "g")
	next, cmd := m.Update(key("y"))
	if cmd == nil {
		t.Fatal("y returned no command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("y did not return a batch of the OSC 52 copy and the native copy")
	}
	m = mustModel(next)
	for _, c := range batch {
		m, _ = run(t, m, c)
	}
	if len(c.urls) != 1 || c.urls[0] != "http://localhost:5173" {
		t.Errorf("copied %q", c.urls)
	}
	if !strings.Contains(view(m), "✓ Copied http://localhost:5173") {
		t.Errorf("no status:\n%s", view(m))
	}

	c = &opened{err: errors.New("no clipboard tool found")}
	m, _ = forwardedWith(t, Options{Copy: c.open}, forward.SchemeNone)
	m, _ = press(t, m, "g", "y")
	if len(c.urls) != 1 || c.urls[0] != "localhost:5173" || !strings.Contains(view(m), "Sent localhost:5173 to the terminal's clipboard") {
		t.Errorf("copy without a native clipboard: copied %q\n%s", c.urls, view(m))
	}
}

func TestFilter(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), withLabel(5173, "node", "Vite"), withLabel(5432, "postgres", "Postgres"), rp(8123, "python3"))
	m, _ = press(t, m, "/", "5", "1")
	v := view(m)
	if !strings.Contains(v, ":5173") || strings.Contains(v, ":5432") || strings.Contains(v, ":8123") {
		t.Errorf("filter 51 shows the wrong rows:\n%s", v)
	}
	if !strings.Contains(v, "/ 51") {
		t.Errorf("the filter input is not shown:\n%s", v)
	}

	// q is a letter while typing; label and process match too.
	m, quit := press(t, m, "backspace", "backspace", "q")
	if quit {
		t.Fatal("q quit while typing a filter")
	}
	m, _ = press(t, m, "backspace", "P", "o", "s", "t")
	if v := view(m); !strings.Contains(v, ":5432") || strings.Contains(v, ":5173") {
		t.Errorf("filter Post shows the wrong rows:\n%s", v)
	}

	// enter keeps the filter; keys act on the table again.
	m, _ = press(t, m, "enter")
	if v := view(m); !strings.Contains(v, "/ Post: 1 of 3 ports") {
		t.Errorf("the kept filter is not on the view line:\n%s", v)
	}
	// esc clears it.
	m, _ = press(t, m, "esc")
	if v := view(m); !strings.Contains(v, ":5173") || !strings.Contains(v, ":8123") || strings.Contains(v, "/ Post") {
		t.Errorf("esc did not clear the filter:\n%s", v)
	}

	m, _ = press(t, m, "/", "z", "z")
	if !strings.Contains(view(m), "No ports match /zz. Press esc to clear the filter.") {
		t.Errorf("no empty-filter message:\n%s", view(m))
	}
	m, _ = press(t, m, "esc")
	if strings.Contains(view(m), "zz") {
		t.Errorf("esc while typing did not clear:\n%s", view(m))
	}
}

func TestFilterMovesWhileTyping(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5000, "a"), rp(5001, "b"), rp(6000, "c"))
	_, _ = press(t, m, "/", "5", "down", "enter", "enter")
	if len(e.specs) != 1 || e.specs[0].RemotePort != 5001 {
		t.Errorf("specs = %+v; want 5001 forwarded", e.specs)
	}
}

func TestHiddenPorts(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), ignored(22, "sshd"), ignored(53, "systemd-resolved"), rp(8123, "python3"))
	v := view(m)
	if strings.Contains(v, ":22 ") || !strings.Contains(v, "2 ignored hidden, h shows") {
		t.Errorf("ignored ports not hidden, or no count:\n%s", v)
	}
	m, _ = press(t, m, "h")
	if l := lineWith(t, m, ":22"); !strings.Contains(l, "ignored") {
		t.Errorf("shown ignored row = %q; want the word ignored", l)
	}
	if strings.Contains(view(m), "hidden") {
		t.Errorf("hidden count still shown:\n%s", view(m))
	}

	m = connectedWith(newModel(newFakeEngine()), ignored(22, "sshd"))
	if !strings.Contains(view(m), "No ports to show: 1 is hidden by the ignore list. Press h to show it.") {
		t.Errorf("no everything-hidden message:\n%s", view(m))
	}
	m = connectedWith(newModelWith(newFakeEngine(), Options{ShowHidden: true}), ignored(22, "sshd"))
	if !strings.Contains(view(m), ":22") {
		t.Errorf("ShowHidden does not show ignored ports:\n%s", view(m))
	}
}

func TestEventLog(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"))
	m = apply(m,
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{rp(5173, "node"), rp(8080, "python3")}, Added: []uint16{8080}},
		engine.AutoForwardFailed{Host: "devbox", Port: 8080, Err: errors.New("busy")},
	)
	m, _ = press(t, m, "enter")
	table := m
	m, _ = press(t, m, "L")
	v := view(m)
	for _, want := range []string{"Event log, newest last", "09:00:00", "Connected to devbox", "New port :8080 (python3)", "Couldn't auto-forward :8080: busy", "Forwarded :5173 to localhost:5173"} {
		if !strings.Contains(v, want) {
			t.Errorf("the log lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "STATE") {
		t.Errorf("the table is still drawn under the log:\n%s", v)
	}
	for _, k := range []string{"L", "esc", "q"} {
		next, quit := press(t, table, "L", k)
		if quit || strings.Contains(view(next), "Event log") {
			t.Errorf("%s did not close the log (quit=%v)", k, quit)
		}
	}
}

func TestEventLogScrolls(t *testing.T) {
	m := sized(connectedWith(newModel(newFakeEngine())), 80, 12)
	for i := range 30 {
		m = apply(m, engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{rp(uint16(6000+i), "x")}, Added: []uint16{uint16(6000 + i)}})
	}
	m, _ = press(t, m, "L")
	if v := view(m); !strings.Contains(v, ":6029") || strings.Contains(v, ":6000 ") {
		t.Errorf("the log does not show the newest entries:\n%s", v)
	}
	m, _ = press(t, m, "g")
	if v := view(m); !strings.Contains(v, "New port :6000") || strings.Contains(v, ":6029") {
		t.Errorf("g did not scroll to the oldest entries:\n%s", v)
	}
	m, _ = press(t, m, "G")
	if !strings.Contains(view(m), ":6029") {
		t.Errorf("G did not scroll back to the newest:\n%s", view(m))
	}
}

func TestMouse(t *testing.T) {
	e := newFakeEngine()
	m := sized(connectedWith(newModelWith(e, Options{Mouse: true}), rp(5000, "a"), rp(5001, "b"), rp(5002, "c")), 80, 24)
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("mouse mode is off")
	}
	// Rows start on line 3 (after the host bar, rule and header), so y=4
	// is the second row.
	next, _ := m.Update(tea.MouseClickMsg{X: 10, Y: 4, Button: tea.MouseLeft})
	m, _ = press(t, mustModel(next), "enter")
	if len(e.specs) != 1 || e.specs[0].RemotePort != 5001 {
		t.Fatalf("click did not select 5001: %+v", e.specs)
	}
	next, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if mustModel(next).cursor != m.cursor+1 {
		t.Error("the wheel did not move the cursor")
	}
	if newModel(e).View().MouseMode != tea.MouseModeNone {
		t.Error("mouse on without Options.Mouse")
	}
}

// TestTooSmallOnlyQuits pins ux-6.
func TestTooSmallOnlyQuits(t *testing.T) {
	e := newFakeEngine()
	m := sized(connectedWith(newModel(e), rp(5173, "node")), 50, 10)
	m, quit := press(t, m, "enter", "?", "F")
	if quit || len(e.specs) != 0 || m.modal.kind != modalNone {
		t.Errorf("keys acted on a hidden screen: specs %v, modal %v", e.specs, m.modal.kind)
	}
	if !strings.Contains(view(m), "q quits") {
		t.Errorf("view:\n%s", view(m))
	}
	if _, quit := press(t, m, "q"); !quit {
		t.Error("q did not quit")
	}
}

// TestBackOutKeys pins ux-7: esc and q close every modal.
func TestBackOutKeys(t *testing.T) {
	for _, k := range []string{"esc", "q"} {
		m := connectedWith(newModel(newFakeEngine()), rp(5173, "node"))
		for _, open := range []string{"?", "F"} {
			next, quit := press(t, m, open, k)
			if quit || next.modal.kind != modalNone {
				t.Errorf("%s then %s: modal %v, quit %v", open, k, next.modal.kind, quit)
			}
		}
		reply := make(chan bool, 1)
		m = apply(newModel(newFakeEngine()), engine.NewHostKeyPrompt("devbox", hostKeyQuestion(), reply))
		m, _ = press(t, m, k)
		if got := <-reply; got || m.modal.kind != modalNone {
			t.Errorf("%s in the host-key modal: answer %v", k, got)
		}
	}
}

// TestScrollKeepsOffset pins ux-8: moving up inside the visible rows
// doesn't drag the view.
func TestScrollKeepsOffset(t *testing.T) {
	var ports []engine.Port
	for i := range 20 {
		ports = append(ports, rp(uint16(5000+i), "x"))
	}
	m := sized(connectedWith(newModel(newFakeEngine()), ports...), 80, 12) // 6 rows visible
	m, _ = press(t, m, "G")
	if !strings.Contains(view(m), ":5019") {
		t.Fatalf("G did not scroll to the end:\n%s", view(m))
	}
	m, _ = press(t, m, "up")
	if v := view(m); !strings.Contains(v, ":5019") || !strings.Contains(lineWith(t, m, ":5018"), "▶") {
		t.Errorf("up moved the view instead of the cursor:\n%s", v)
	}
}

func TestConflictWordingOnWindows(t *testing.T) {
	e := newFakeEngine()
	e.forwardErr = &forward.PortInUseError{Addr: "127.0.0.1", Port: 5173, Suggested: 5174}
	m := connectedWith(newModel(e), rp(5173, "node"))
	m.goos = "windows"
	m, _ = press(t, m, "enter")
	if v := view(m); !strings.Contains(v, "Windows reserved it") || !strings.Contains(v, "excludedportrange") {
		t.Errorf("the Windows conflict modal does not mention reserved ports:\n%s", v)
	}
}

func TestSkipQuitConfirm(t *testing.T) {
	m := connectedWith(newModelWith(newFakeEngine(), Options{SkipQuitConfirm: true}), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	if _, quit := press(t, m, "q"); !quit {
		t.Error("q asked to confirm although SkipQuitConfirm is set")
	}
}

func TestHintsFollowRowAndKeepQuit(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"), rp(8123, "python3"))
	if h := lineWith(t, m, "q quit"); !strings.Contains(h, "enter forward") || strings.Contains(h, "o open") {
		t.Errorf("hints on a detected row = %q", h)
	}
	m, _ = press(t, m, "enter")
	if h := lineWith(t, m, "q quit"); !strings.Contains(h, "enter stop") || !strings.Contains(h, "o open") {
		t.Errorf("hints on a forwarded row = %q", h)
	}
	m = sized(m, 60, 24)
	if h := lineWith(t, m, "enter stop"); !strings.Contains(h, "q quit") || !strings.Contains(h, "? help") || ansi.StringWidth(h) > 60 {
		t.Errorf("hints at 60 columns = %q; want ? help and q quit kept", h)
	}
}

func TestBindBadge(t *testing.T) {
	m := connectedWith(newModelWith(newFakeEngine(), Options{BindAddr: "0.0.0.0"}), rp(5173, "node"))
	if bar := lineWith(t, m, "pfwd"); !strings.Contains(bar, "bind 0.0.0.0") {
		t.Errorf("host bar = %q; want the bind badge", bar)
	}
	m = connectedWith(newModelWith(newFakeEngine(), Options{BindAddr: "127.0.0.1"}), rp(5173, "node"))
	if strings.Contains(lineWith(t, m, "pfwd"), "bind") {
		t.Error("badge shown for a loopback bind")
	}
}

func TestASCIIGlyphs(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModelWith(e, Options{ASCII: true}), rp(5173, "node"), rp(8123, "python3"))
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "?")
	for _, r := range view(m) {
		if r > 0x7e {
			t.Fatalf("ASCII mode drew %q:\n%s", r, view(m))
		}
	}
}

func TestBackgroundColorPicksTheme(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(5173, "node"))
	dark := m.View().Content
	next, _ := m.Update(tea.BackgroundColorMsg{Color: lightGray})
	light := mustModel(next).View().Content
	if !strings.Contains(dark, "\x1b[36m") || !strings.Contains(light, "\x1b[34m") || strings.Contains(light, "\x1b[36m") {
		t.Errorf("accent: dark has cyan %v, light has blue %v", strings.Contains(dark, "\x1b[36m"), strings.Contains(light, "\x1b[34m"))
	}
	forced := connectedWith(newModelWith(newFakeEngine(), Options{Theme: "light"}), rp(5173, "node"))
	next, _ = forced.Update(tea.BackgroundColorMsg{Color: black})
	if !strings.Contains(mustModel(next).View().Content, "\x1b[34m") {
		t.Error("Theme light was overridden by the terminal's answer")
	}
}

func TestStatusAndLogUseTime(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()))
	m.now = func() time.Time { return t0.Add(90 * time.Minute) }
	m = apply(m, engine.AutoChanged{Host: "devbox", On: false}, engine.AutoChanged{Host: "devbox", On: true})
	m, _ = press(t, m, "L")
	if !strings.Contains(view(m), "10:30:00  Auto-forward on for devbox") {
		t.Errorf("log:\n%s", view(m))
	}
}
