package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/safetext"
)

// Engine is the part of *engine.Engine the TUI uses.
type Engine interface {
	Events() <-chan engine.Event
	Forward(host engine.HostID, spec engine.ForwardSpec) (engine.Forward, error)
	Stop(id string) error
	Forwards() []engine.Forward
	SetAuto(host engine.HostID, on bool)
}

// Options configure the TUI. The zero value draws Unicode glyphs, asks
// the terminal whether its background is dark, leaves the mouse alone and
// asks before quitting while forwards are active.
type Options struct {
	// Open opens a URL in the browser (o).
	Open func(ctx context.Context, url string) error
	// Copy puts text on the system clipboard (y). y also sends the text
	// to the terminal's clipboard with OSC 52, which works over SSH.
	Copy func(ctx context.Context, text string) error
	// Theme is "dark" or "light"; anything else asks the terminal.
	Theme string
	// ASCII draws ASCII glyphs, for fonts without the Unicode ones.
	ASCII bool
	// Mouse selects rows on click and moves the cursor with the wheel.
	Mouse bool
	// SkipQuitConfirm quits on q even while forwards are active.
	SkipQuitConfirm bool
	// ShowHidden shows ignored ports from the start (h toggles).
	ShowHidden bool
	// BindAddr is the local address forwards listen on; one that isn't
	// loopback gets a badge in the host bar.
	BindAddr string
	// OnConflict is what enter does when the local port is taken: "next"
	// uses the free port nearby, "random" any free port, "fail" reports
	// the error, and anything else ("ask") opens the conflict modal.
	OnConflict string
}

// statusFor is how long the status line shows the last event.
const statusFor = 5 * time.Second

// maxLog is how many events the log keeps.
const maxLog = 500

// maxFilter caps the filter's length.
const maxFilter = 40

type statusKind int

const (
	statusInfo statusKind = iota
	statusOK
	statusErr
)

type status struct {
	text string
	kind statusKind
	at   time.Time
}

// logEntry is one line of the event log.
type logEntry struct {
	at   time.Time
	kind statusKind
	text string
}

type sample struct {
	in, out int64
	at      time.Time
}

type rate struct{ in, out float64 }

// row is one table line: a forward (with the detected port it forwards,
// if the server still lists it) or a detected port that is not forwarded.
type row struct {
	key  string
	fwd  *engine.Forward
	port *engine.Port
}

// Model is the TUI state. Create it with New.
type Model struct {
	eng   Engine
	host  engine.HostID
	opts  Options
	th    theme
	goos  string
	now   func() time.Time
	every func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	width, height int

	session      engine.SessionState
	sessErr      error
	wasConnected bool
	autoKnown    bool
	auto         bool

	scanned   bool
	strategy  detect.Strategy
	remoteOS  string
	detectErr error
	ports     []engine.Port

	forwards map[string]engine.Forward
	samples  map[string]sample
	rates    map[string]rate

	rows     []row
	cursor   int
	selected string
	// selectedPort is the selected row's remote port, so the cursor can
	// follow it between the forwards and the detected ports.
	selectedPort uint16
	// offset is the first table line on screen.
	offset int

	// filter narrows the rows; filtering is true while it is typed.
	filter     string
	filtering  bool
	showHidden bool
	// total counts the rows before the filter; hidden counts the ignored
	// ports left out.
	total, hidden int

	log     []logEntry
	logOpen bool
	// logScroll is how many lines the log is scrolled up from the newest.
	logScroll int

	modal  modalState
	status status
}

// New returns the model for one host. Connect the engine to the host
// separately; the model reacts to its events.
func New(e Engine, host engine.HostID, opts Options) Model {
	return Model{
		eng:        e,
		host:       host,
		opts:       opts,
		th:         newTheme(opts.Theme != "light", opts.ASCII),
		goos:       runtime.GOOS,
		now:        time.Now,
		every:      tea.Tick,
		width:      80,
		height:     24,
		showHidden: opts.ShowHidden,
		forwards:   make(map[string]engine.Forward),
		samples:    make(map[string]sample),
		rates:      make(map[string]rate),
	}
}

// Err returns the connection error, if the connection failed or was lost.
func (m Model) Err() error {
	return m.sessErr
}

type (
	eventMsg        struct{ ev engine.Event }
	eventsClosedMsg struct{}
	tickMsg         time.Time
	statsMsg        struct {
		forwards []engine.Forward
		at       time.Time
	}
	forwardResultMsg struct {
		spec engine.ForwardSpec
		fwd  engine.Forward
		err  error
		// resolve applies Options.OnConflict to a taken port; a port the
		// user typed always asks.
		resolve bool
	}
	stopResultMsg struct {
		id     string
		remote uint16
		err    error
	}
	openResultMsg struct{ err error }
	copyResultMsg struct {
		text string
		err  error
	}
)

func waitEvent(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventsClosedMsg{}
		}
		return eventMsg{ev}
	}
}

func (m Model) tick() tea.Cmd {
	return m.every(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// themeForced reports whether Options.Theme overrides the terminal.
func (m Model) themeForced() bool {
	return m.opts.Theme == "dark" || m.opts.Theme == "light"
}

// Init starts listening for engine events and the 1 s stats tick, and
// asks the terminal for its background color unless the theme is forced.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitEvent(m.eng.Events()), m.tick()}
	if !m.themeForced() {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

// Update applies a message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scrollToCursor()
	case tea.BackgroundColorMsg:
		if !m.themeForced() {
			m.th = newTheme(msg.IsDark(), m.opts.ASCII)
		}
	case eventMsg:
		m.applyEvent(msg.ev)
		return m, waitEvent(m.eng.Events())
	case eventsClosedMsg:
		return m, tea.Quit
	case tickMsg:
		eng, at := m.eng, time.Time(msg)
		return m, func() tea.Msg { return statsMsg{forwards: eng.Forwards(), at: at} }
	case statsMsg:
		m.applyStats(msg)
		return m, m.tick()
	case forwardResultMsg:
		return m, m.applyForwardResult(msg)
	case stopResultMsg:
		m.applyStopResult(msg)
	case openResultMsg:
		if msg.err != nil {
			m.event(statusErr, "%s", msg.err.Error())
		}
	case copyResultMsg:
		if msg.err == nil {
			m.setStatus(statusOK, "Copied %s", msg.text)
		} else {
			m.setStatus(statusInfo, "Sent %s to the terminal's clipboard", msg.text)
			m.addLog(statusInfo, "The system clipboard didn't take it: "+msg.err.Error())
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		m.handleClick(msg.Mouse())
	case tea.MouseWheelMsg:
		m.handleWheel(msg.Mouse())
	}
	return m, nil
}

// setStatus shows a message in the status line.
func (m *Model) setStatus(kind statusKind, format string, args ...any) {
	m.status = status{text: fmt.Sprintf(format, args...), kind: kind, at: m.now()}
}

// event shows a message in the status line and keeps it in the log.
func (m *Model) event(kind statusKind, format string, args ...any) {
	m.setStatus(kind, format, args...)
	m.addLog(kind, m.status.text)
}

func (m *Model) addLog(kind statusKind, text string) {
	m.log = append(m.log, logEntry{at: m.now(), kind: kind, text: text})
	if n := len(m.log) - maxLog; n > 0 {
		m.log = slices.Delete(m.log, 0, n)
	}
	if m.logScroll > 0 {
		m.logScroll++ // keep the lines on screen where they are
	}
}

func (m *Model) applyEvent(ev engine.Event) {
	host := safetext.Clean(string(m.host))
	switch ev := ev.(type) {
	case engine.SessionChanged:
		if ev.Host != m.host {
			return
		}
		m.session = ev.State
		switch ev.State {
		case engine.Connected:
			m.wasConnected = true
			m.addLog(statusOK, "Connected to "+host)
		case engine.SessionFailed:
			m.sessErr = ev.Err
			if m.modal.kind == modalHostKey {
				m.modal = modalState{}
			}
			if ev.Err != nil {
				m.addLog(statusErr, "Connection to "+host+" ended: "+ev.Err.Error())
			}
		}
	case engine.AutoChanged:
		if ev.Host != m.host {
			return
		}
		changed := m.autoKnown && m.auto != ev.On
		m.autoKnown, m.auto = true, ev.On
		switch {
		case changed && ev.On:
			m.event(statusInfo, "Auto-forward on for %s: new ports are forwarded", host)
		case changed:
			m.event(statusInfo, "Auto-forward off for %s", host)
		}
	case engine.AutoForwardFailed:
		if ev.Host == m.host {
			m.event(statusErr, "Couldn't auto-forward :%d: %s", ev.Port, ev.Err.Error())
		}
	case engine.PortsChanged:
		if ev.Host != m.host {
			return
		}
		m.scanned = true
		m.strategy, m.remoteOS, m.detectErr, m.ports = ev.Strategy, ev.OS, ev.Err, ev.Ports
		for _, port := range ev.Added {
			text := "New port :" + itoa(int(port))
			if i := slices.IndexFunc(ev.Ports, func(p engine.Port) bool { return p.Port == port }); i >= 0 && ev.Ports[i].Process != "" {
				text += " (" + ev.Ports[i].Process + ")"
			}
			m.addLog(statusInfo, text)
		}
		for _, port := range ev.Removed {
			m.addLog(statusInfo, "Port :"+itoa(int(port))+" closed")
		}
	case engine.ForwardChanged:
		m.applyForwardChanged(ev.Forward)
	case engine.HostKeyPrompt:
		if ev.Host == m.host {
			m.modal = modalState{kind: modalHostKey, prompt: ev}
		}
	}
	m.rebuild()
}

func (m *Model) applyForwardChanged(f engine.Forward) {
	if f.Host != m.host {
		return
	}
	prev, had := m.forwards[f.ID]
	if f.State == engine.Stopped {
		if had {
			m.dropForward(f.ID)
			m.addLog(statusOK, "Stopped :"+itoa(int(f.Spec.RemotePort)))
		}
		return
	}
	m.forwards[f.ID] = f
	if !had && f.State != engine.Failed {
		m.forwardStarted(f)
	}
	if f.Scheme != prev.Scheme && (f.Scheme == forward.SchemeHTTP || f.Scheme == forward.SchemeHTTPS) {
		m.addLog(statusInfo, fmt.Sprintf(":%d speaks %s", f.Spec.RemotePort, f.Scheme))
	}
	// A new error on a forward (the server refuses forwarding, or nothing
	// listens on the target any more) goes to the status line.
	if f.Err != nil && (!had || prev.Err == nil || prev.Err.Error() != f.Err.Error()) {
		m.event(statusErr, ":%d: %s", f.Spec.RemotePort, f.Err.Error())
	}
}

// forwardStarted logs a forward the first time the model sees it. An
// auto-forward also goes to the status line; a manual one gets its status
// from the forward's result.
func (m *Model) forwardStarted(f engine.Forward) {
	text := fmt.Sprintf("Forwarded :%d to %s", f.Spec.RemotePort, localAddr(f.Spec))
	if !f.Spec.Auto {
		m.addLog(statusOK, text)
		return
	}
	if f.Spec.LocalPort != f.Spec.RemotePort {
		m.event(statusOK, "%s (auto; %d was busy)", text, f.Spec.RemotePort)
		return
	}
	m.event(statusOK, "%s (auto)", text)
}

func (m *Model) dropForward(id string) {
	delete(m.forwards, id)
	delete(m.samples, id)
	delete(m.rates, id)
}

// applyStats refreshes counters and computes traffic rates. It only
// updates forwards the model already knows, so a stale snapshot never
// brings back a stopped forward.
func (m *Model) applyStats(msg statsMsg) {
	for _, f := range msg.forwards {
		cur, ok := m.forwards[f.ID]
		if !ok {
			continue
		}
		cur.Stats, cur.State, cur.Err = f.Stats, f.State, f.Err
		m.forwards[f.ID] = cur
		if prev, ok := m.samples[f.ID]; ok && msg.at.After(prev.at) {
			dt := msg.at.Sub(prev.at).Seconds()
			m.rates[f.ID] = rate{
				in:  max(0, float64(f.Stats.BytesIn-prev.in)/dt),
				out: max(0, float64(f.Stats.BytesOut-prev.out)/dt),
			}
		}
		m.samples[f.ID] = sample{in: f.Stats.BytesIn, out: f.Stats.BytesOut, at: msg.at}
	}
	m.rebuild()
}

// applyForwardResult records a forward, or handles why it failed. A taken
// local port follows Options.OnConflict, which may retry.
func (m *Model) applyForwardResult(msg forwardResultMsg) tea.Cmd {
	if msg.err != nil {
		var inUse *forward.PortInUseError
		if errors.As(msg.err, &inUse) {
			return m.portTaken(msg, inUse)
		}
		m.event(statusErr, "%s", msg.err.Error())
		return nil
	}
	if _, ok := m.forwards[msg.fwd.ID]; !ok {
		m.forwards[msg.fwd.ID] = msg.fwd
		m.forwardStarted(msg.fwd)
	}
	m.selected = "f:" + msg.fwd.ID
	m.setStatus(statusOK, "Forwarded :%d to %s", msg.fwd.Spec.RemotePort, localAddr(msg.fwd.Spec))
	m.rebuild()
	return nil
}

// portTaken follows forward.on_conflict for a forward made with enter;
// "ask", and any port the user typed, opens the conflict modal.
func (m *Model) portTaken(msg forwardResultMsg, inUse *forward.PortInUseError) tea.Cmd {
	spec := msg.spec
	switch {
	case !msg.resolve:
	case m.opts.OnConflict == "next" && inUse.Suggested != 0:
		spec.LocalPort = inUse.Suggested
		return m.forwardCmd(spec)
	case m.opts.OnConflict == "random":
		return m.randomPortCmd(spec)
	case m.opts.OnConflict == "fail" || m.opts.OnConflict == "next":
		m.event(statusErr, "%s", inUse.Error())
		return nil
	}
	m.modal = modalState{kind: modalConflict, spec: spec, busy: inUse.Port, bind: inUse.Addr, suggested: inUse.Suggested}
	return nil
}

func (m *Model) applyStopResult(msg stopResultMsg) {
	if msg.err != nil {
		m.event(statusErr, "%s", msg.err.Error())
		return
	}
	if _, had := m.forwards[msg.id]; had {
		m.dropForward(msg.id)
		m.addLog(statusOK, "Stopped :"+itoa(int(msg.remote)))
	}
	m.selected = portKey(msg.remote)
	m.setStatus(statusOK, "Stopped :%d", msg.remote)
	m.rebuild()
}

func portKey(p uint16) string { return "p:" + itoa(int(p)) }

// rebuild recomputes the rows: forwards first, then detected ports that
// are not forwarded, each sorted by port, minus hidden ports and rows the
// filter doesn't match. The cursor stays on the selected row's key, or on
// the same index when that row is gone.
func (m *Model) rebuild() {
	byPort := make(map[uint16]*engine.Port, len(m.ports))
	for i := range m.ports {
		byPort[m.ports[i].Port] = &m.ports[i]
	}
	var fwds []engine.Forward
	for _, f := range m.forwards {
		fwds = append(fwds, f)
	}
	slices.SortFunc(fwds, func(a, b engine.Forward) int {
		if a.Spec.RemotePort != b.Spec.RemotePort {
			return int(a.Spec.RemotePort) - int(b.Spec.RemotePort)
		}
		return compareIDs(a.ID, b.ID)
	})

	m.total, m.hidden = 0, 0
	rows := make([]row, 0, len(fwds)+len(m.ports))
	forwarded := make(map[uint16]bool)
	for i := range fwds {
		f := &fwds[i]
		forwarded[f.Spec.RemotePort] = true
		m.total++
		if r := (row{key: "f:" + f.ID, fwd: f, port: byPort[f.Spec.RemotePort]}); m.matches(r) {
			rows = append(rows, r)
		}
	}
	for i := range m.ports {
		p := &m.ports[i]
		switch {
		case forwarded[p.Port]:
			continue
		case p.Ignored && !m.showHidden:
			m.hidden++
			continue
		}
		m.total++
		if r := (row{key: portKey(p.Port), port: p}); m.matches(r) {
			rows = append(rows, r)
		}
	}
	m.rows = rows

	idx := slices.IndexFunc(rows, func(r row) bool { return r.key == m.selected })
	if idx < 0 && m.selectedPort != 0 {
		// The port was forwarded or stopped, so its row moved group.
		idx = slices.IndexFunc(rows, func(r row) bool { return r.remotePort() == m.selectedPort })
	}
	if idx < 0 {
		idx = min(m.cursor, len(rows)-1)
	}
	m.setCursor(idx)
}

// remotePort is the server port a row stands for.
func (r row) remotePort() uint16 {
	if r.fwd != nil {
		return r.fwd.Spec.RemotePort
	}
	return r.port.Port
}

// matches reports whether a row contains the filter in its port, local
// port, process or label, ignoring case.
func (m Model) matches(r row) bool {
	if m.filter == "" {
		return true
	}
	f := strings.ToLower(m.filter)
	fields := []string{rowLabel(r)}
	if r.fwd != nil {
		fields = append(fields, itoa(int(r.fwd.Spec.RemotePort)), itoa(int(r.fwd.Spec.LocalPort)))
	}
	if r.port != nil {
		fields = append(fields, itoa(int(r.port.Port)), r.port.Process)
	}
	return slices.ContainsFunc(fields, func(s string) bool { return strings.Contains(strings.ToLower(s), f) })
}

// rowLabel is the forward's label, else the detected port's.
func rowLabel(r row) string {
	if r.fwd != nil && r.fwd.Spec.Label != "" {
		return r.fwd.Spec.Label
	}
	if r.port != nil {
		return r.port.Label
	}
	return ""
}

func compareIDs(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (m *Model) setCursor(i int) {
	if len(m.rows) == 0 {
		m.cursor, m.selected, m.selectedPort, m.offset = 0, "", 0, 0
		return
	}
	m.cursor = max(0, min(i, len(m.rows)-1))
	m.selected = m.rows[m.cursor].key
	m.selectedPort = m.rows[m.cursor].remotePort()
	m.scrollToCursor()
}

// separatorAt is the table line of the "detected, not forwarded"
// separator, or -1 when there is none: it sits between the forwards and
// the detected ports when there are both.
func (m Model) separatorAt() int {
	nf := 0
	for nf < len(m.rows) && m.rows[nf].fwd != nil {
		nf++
	}
	if nf == 0 || nf == len(m.rows) {
		return -1
	}
	return nf
}

// lineOf is the table line of row i.
func (m Model) lineOf(i int) int {
	if sep := m.separatorAt(); sep >= 0 && i >= sep {
		return i + 1
	}
	return i
}

// visibleRows is how many table lines fit under the header.
func (m Model) visibleRows() int {
	return max(1, m.height-6)
}

// scrollToCursor moves the table only as far as needed to show the
// cursor, so moving inside the visible rows never drags the view.
func (m *Model) scrollToCursor() {
	vis := m.visibleRows()
	lines := len(m.rows)
	if m.separatorAt() >= 0 {
		lines++
	}
	cl := m.lineOf(m.cursor)
	if cl < m.offset {
		m.offset = cl
	}
	if cl >= m.offset+vis {
		m.offset = cl - vis + 1
	}
	m.offset = max(0, min(m.offset, lines-vis))
}

func (m Model) current() (row, bool) {
	if m.cursor < len(m.rows) {
		return m.rows[m.cursor], true
	}
	return row{}, false
}

func (m Model) activeForwards() int {
	n := 0
	for _, f := range m.forwards {
		if f.State == engine.Active {
			n++
		}
	}
	return n
}

func (m Model) forwardCmd(spec engine.ForwardSpec) tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		f, err := eng.Forward(host, spec)
		return forwardResultMsg{spec: spec, fwd: f, err: err}
	}
}

// enterCmd forwards a detected port with enter: a taken local port then
// follows Options.OnConflict.
func (m Model) enterCmd(spec engine.ForwardSpec) tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		f, err := eng.Forward(host, spec)
		return forwardResultMsg{spec: spec, fwd: f, err: err, resolve: true}
	}
}

// randomPortCmd forwards spec to any free local port.
func (m Model) randomPortCmd(spec engine.ForwardSpec) tea.Cmd {
	eng, host, bind := m.eng, m.host, m.opts.BindAddr
	return func() tea.Msg {
		port, err := forward.FreePort(bind)
		if err != nil {
			return forwardResultMsg{spec: spec, err: err}
		}
		spec.LocalPort = port
		f, err := eng.Forward(host, spec)
		return forwardResultMsg{spec: spec, fwd: f, err: err}
	}
}

func (m Model) stopCmd(f engine.Forward) tea.Cmd {
	eng, id, remote := m.eng, f.ID, f.Spec.RemotePort
	return func() tea.Msg {
		return stopResultMsg{id: id, remote: remote, err: eng.Stop(id)}
	}
}

func (m Model) setAutoCmd(on bool) tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		eng.SetAuto(host, on)
		return nil
	}
}

func (m Model) openCmd(url string) tea.Cmd {
	open := m.opts.Open
	return func() tea.Msg { return openResultMsg{err: open(context.Background(), url)} }
}

func (m Model) copyCmd(text string) tea.Cmd {
	cp := m.opts.Copy
	return func() tea.Msg { return copyResultMsg{text: text, err: cp(context.Background(), text)} }
}

// specFor is the forward for a detected port: same local port, dialing
// the address the server listens on.
func specFor(p engine.Port) engine.ForwardSpec {
	return engine.ForwardSpec{RemotePort: p.Port, LocalPort: p.Port, RemoteHost: detect.Target(p.Addrs)}
}

// isLoopback reports whether a bind address only accepts this machine.
func isLoopback(bind string) bool {
	switch bind {
	case "", "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// localAddr is how a forward's local end is shown: localhost:PORT on
// loopback, bind:PORT otherwise.
func localAddr(s engine.ForwardSpec) string {
	if isLoopback(s.BindAddr) {
		return "localhost:" + itoa(int(s.LocalPort))
	}
	return net.JoinHostPort(s.BindAddr, itoa(int(s.LocalPort)))
}

// target is what o opens and y copies: an http or https URL, or just
// host:port for a forward that doesn't speak HTTP (ok is false then).
func target(f engine.Forward) (s string, ok bool) {
	host := "localhost"
	switch b := f.Spec.BindAddr; {
	case isLoopback(b), b == "0.0.0.0", b == "::":
	default:
		host = b
	}
	addr := net.JoinHostPort(host, itoa(int(f.Spec.LocalPort)))
	switch f.Scheme {
	case forward.SchemeNone:
		return addr, false
	case forward.SchemeHTTPS:
		return "https://" + addr, true
	default:
		return "http://" + addr, true
	}
}
