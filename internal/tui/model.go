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
	Reconnect(host engine.HostID)
	StopRetrying(host engine.HostID)
	SetPinned(id string, pinned bool) error
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
	// Hosts lists the hosts + offers, most recently used first; nil turns
	// + off.
	Hosts func(ctx context.Context) ([]HostChoice, error)
	// Connect resolves a host picked with + and connects the engine to it,
	// returning its ID.
	Connect func(ctx context.Context, name string) (engine.HostID, error)
	// LocalPorts lists this machine's listening ports for the Reverse tab
	// (r); nil leaves the tab empty with a note.
	LocalPorts func(ctx context.Context) ([]detect.RemotePort, error)
}

// socksPort is the port d starts a SOCKS proxy on.
const socksPort = 1080

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
	host engine.HostID
	fwd  *engine.Forward
	port *engine.Port
}

// HostChoice is a host the picker (+) offers.
type HostChoice struct {
	Name string
	// LastUsed is when pfwd last used the host; zero when never.
	LastUsed time.Time
}

// hostView is what the TUI knows about one host's connection and ports.
type hostView struct {
	session      engine.SessionState
	sessErr      error
	wasConnected bool
	autoKnown    bool
	auto         bool
	// retryAt and attempt describe the next reconnect while reconnecting;
	// retryAt is zero while an attempt is under way.
	retryAt time.Time
	attempt int

	scanned   bool
	strategy  detect.Strategy
	detectErr error
	ports     []engine.Port
}

// pickerState is the host picker's list and filter.
type pickerState struct {
	loading bool
	err     error
	choices []HostChoice
	filter  string
	cursor  int
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

	// hosts are the tabs, in the order they were opened. views holds each
	// host's state; host and the embedded hostView are the current tab's.
	hosts []engine.HostID
	views map[engine.HostID]*hostView
	*hostView
	// all shows every host's rows, with a HOST column (ctrl+a).
	all    bool
	picker pickerState

	// keyPrompts and creds are host-key and credential prompts waiting for
	// the modal. One opens at a time, so a prompt from another host never
	// replaces the one the user is answering.
	keyPrompts []engine.HostKeyPrompt
	creds      []engine.CredentialPrompt

	// forwards holds every host's forwards.
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

	// reverseOpen shows the Reverse tab: this machine's listening ports
	// (local), to expose one on the server.
	reverseOpen  bool
	local        []detect.RemotePort
	localErr     error
	localLoading bool
	revCursor    int

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
	return NewHosts(e, []engine.HostID{host}, opts)
}

// NewHosts returns the model for several hosts, one tab each, the first
// one current. Connect the engine to them separately.
func NewHosts(e Engine, hosts []engine.HostID, opts Options) Model {
	views := make(map[engine.HostID]*hostView, len(hosts))
	for _, h := range hosts {
		views[h] = &hostView{}
	}
	return Model{
		eng:        e,
		host:       hosts[0],
		hosts:      slices.Clone(hosts),
		views:      views,
		hostView:   views[hosts[0]],
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
		host engine.HostID
		spec engine.ForwardSpec
		fwd  engine.Forward
		err  error
		// resolve applies Options.OnConflict to a taken port; a port the
		// user typed always asks.
		resolve bool
	}
	stopResultMsg struct {
		id     string
		host   engine.HostID
		remote uint16
		err    error
	}
	openResultMsg struct{ err error }
	copyResultMsg struct {
		text string
		err  error
	}
	pinResultMsg struct {
		port   uint16
		pinned bool
		err    error
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

// Update applies a message. A host-key or credential prompt waiting in the
// queue opens as soon as no other modal is open.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(Model); ok {
		nm.openQueuedPrompt()
		return nm, cmd
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		return m.onHost(msg.host, func(m Model) (tea.Model, tea.Cmd) {
			cmd := m.applyForwardResult(msg)
			return m, cmd
		})
	case hostsMsg:
		m.picker.loading, m.picker.choices, m.picker.err = false, msg.hosts, msg.err
	case hostAddedMsg:
		m.addHost(msg)
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
	case localPortsMsg:
		m.applyLocalPorts(msg)
	case pinResultMsg:
		switch {
		case msg.err != nil:
			m.setStatus(statusErr, "%s", msg.err.Error())
		case msg.pinned:
			m.setStatus(statusOK, "Pinned :%d: it comes back next time pfwd connects", msg.port)
		default:
			m.setStatus(statusOK, "Unpinned :%d", msg.port)
		}
	case tea.PasteMsg:
		if m.modal.kind == modalCredential {
			m.typeSecret(msg.Content)
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
		// Keep the lines on screen where they are, by the new entry's
		// wrapped lines, without scrolling past the top.
		added := len(m.entryLines(m.log[len(m.log)-1], m.width))
		m.logScroll = min(m.logScroll+added, m.maxLogScroll())
	}
}

func (m *Model) applyEvent(ev engine.Event) {
	if h, ok := eventHost(ev); ok && h != m.host {
		v := m.views[h]
		if v == nil {
			return // a host this window doesn't show
		}
		host, view := m.host, m.hostView
		m.host, m.hostView = h, v
		m.applyHostEvent(ev)
		m.host, m.hostView = host, view
		m.rebuild()
		return
	}
	m.applyHostEvent(ev)
}

// eventHost returns the host an event is about.
func eventHost(ev engine.Event) (engine.HostID, bool) {
	switch ev := ev.(type) {
	case engine.SessionChanged:
		return ev.Host, true
	case engine.AutoChanged:
		return ev.Host, true
	case engine.AutoForwardFailed:
		return ev.Host, true
	case engine.PortsChanged:
		return ev.Host, true
	case engine.ForwardChanged:
		return ev.Forward.Host, true
	case engine.HostKeyPrompt:
		return ev.Host, true
	case engine.CredentialPrompt:
		return ev.Host, true
	case engine.RestoreFailed:
		return ev.Host, true
	}
	return "", false
}

// applyHostEvent applies an event about the current host (applyEvent
// makes the event's host current while it runs).
func (m *Model) applyHostEvent(ev engine.Event) {
	host := safetext.Clean(string(m.host))
	switch ev := ev.(type) {
	case engine.SessionChanged:
		if ev.Host != m.host {
			return
		}
		prev := m.session
		m.session = ev.State
		switch ev.State {
		case engine.Connected:
			if m.wasConnected {
				m.event(statusOK, "Reconnected to %s", host)
			} else {
				m.addLog(statusOK, "Connected to "+host)
			}
			m.wasConnected, m.sessErr = true, nil
			m.retryAt, m.attempt = time.Time{}, 0
		case engine.Reconnecting:
			m.retryAt, m.attempt = ev.RetryAt, ev.Attempt
			switch {
			case prev != engine.Reconnecting:
				// The ports listed before the drop are stale now.
				m.ports, m.scanned = nil, false
				m.event(statusErr, "Lost the connection to %s; reconnecting", host)
				if ev.Err != nil {
					m.addLog(statusErr, ev.Err.Error())
				}
			case !ev.RetryAt.IsZero() && ev.Err != nil:
				m.addLog(statusErr, fmt.Sprintf("Reconnect attempt %d failed: %s", ev.Attempt-1, ev.Err.Error()))
			}
		case engine.SessionFailed:
			m.sessErr = ev.Err
			m.closePrompts(ev.Host)
			if m.wasConnected {
				m.setStatus(statusErr, "Disconnected from %s; c reconnects", host)
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
		m.strategy, m.detectErr, m.ports = ev.Strategy, ev.Err, ev.Ports
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
			m.keyPrompts = append(m.keyPrompts, ev)
		}
	case engine.CredentialPrompt:
		if ev.Host == m.host {
			m.creds = append(m.creds, ev)
		}
	case engine.RestoreFailed:
		switch {
		case ev.Host != m.host:
		case ev.Spec == (engine.ForwardSpec{}):
			m.event(statusErr, "Couldn't read the remembered forwards: %s", ev.Err.Error())
		case ev.Spec.Kind == engine.Dynamic:
			m.event(statusErr, "Couldn't start the SOCKS proxy on %s: %s", localAddr(ev.Spec), ev.Err.Error())
		case ev.Spec.Kind == engine.Remote:
			m.event(statusErr, "Couldn't expose %s on :%d: %s", localAddr(ev.Spec), ev.Spec.RemotePort, ev.Err.Error())
		default:
			m.event(statusErr, "Couldn't restore :%d: %s", ev.Spec.RemotePort, ev.Err.Error())
		}
	}
	m.rebuild()
}

// maxSecret caps the credential field. Its buffer is allocated at this
// size and never grows, so zeroing it removes every byte typed.
const maxSecret = 1024

// openQueuedPrompt opens the next prompt when no modal is open: host keys
// first, then credentials.
func (m *Model) openQueuedPrompt() {
	if m.modal.kind != modalNone {
		return
	}
	if len(m.keyPrompts) > 0 {
		p := m.keyPrompts[0]
		m.keyPrompts = m.keyPrompts[1:]
		m.modal = modalState{kind: modalHostKey, prompt: p}
		return
	}
	if len(m.creds) == 0 {
		return
	}
	p := m.creds[0]
	m.creds = m.creds[1:]
	m.modal = modalState{kind: modalCredential, cred: p, secret: make([]byte, 0, maxSecret)}
}

// closePrompts drops the host-key and credential prompts of host's
// session, which ended, zeroing anything typed.
func (m *Model) closePrompts(host engine.HostID) {
	m.keyPrompts = slices.DeleteFunc(m.keyPrompts, func(p engine.HostKeyPrompt) bool { return p.Host == host })
	m.creds = slices.DeleteFunc(m.creds, func(p engine.CredentialPrompt) bool { return p.Host == host })
	open := (m.modal.kind == modalHostKey && m.modal.prompt.Host == host) ||
		(m.modal.kind == modalCredential && m.modal.cred.Host == host)
	if open {
		clear(m.modal.secret[:cap(m.modal.secret)])
		m.modal = modalState{}
	}
	m.creds = nil
}

func (m *Model) applyForwardChanged(f engine.Forward) {
	if f.Host != m.host {
		return
	}
	prev, had := m.forwards[f.ID]
	port := int(f.Spec.RemotePort)
	if f.State == engine.Stopped {
		if had {
			m.dropForward(f.ID)
			if prev.State == engine.Paused && prev.Pause == engine.PortGone && prev.Spec.Auto && !prev.Spec.Pinned {
				m.addLog(statusInfo, fmt.Sprintf("Removed :%d: its port stayed closed", port))
			} else {
				m.addLog(statusOK, "Stopped :"+itoa(port))
			}
		}
		return
	}
	m.forwards[f.ID] = f
	if !had && f.State != engine.Failed {
		m.forwardStarted(f)
	}
	if had && (prev.State != f.State || prev.Pause != f.Pause) {
		switch {
		case f.State == engine.Paused && f.Pause == engine.PortGone:
			m.addLog(statusInfo, fmt.Sprintf("Paused :%d: the port closed on %s", port, safetext.Clean(string(m.host))))
		case f.State == engine.Paused && f.Pause == engine.ServerPortBusy:
			m.addLog(statusInfo, fmt.Sprintf("Waiting for :%d on %s: the old connection still holds it", port, safetext.Clean(string(m.host))))
		case f.State == engine.Active && prev.State == engine.Paused && (prev.Pause == engine.PortGone || prev.Pause == engine.ServerPortBusy):
			m.addLog(statusOK, fmt.Sprintf("Resumed :%d", port))
		}
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

// describe says what a forward does, in the words the status line uses.
func describe(f engine.Forward, host string) string {
	switch f.Spec.Kind {
	case engine.Remote:
		return fmt.Sprintf("Exposed %s on :%d of %s", localAddr(f.Spec), f.Spec.RemotePort, host)
	case engine.Dynamic:
		return "SOCKS proxy on " + localAddr(f.Spec)
	}
	text := fmt.Sprintf("Forwarded :%d to %s", f.Spec.RemotePort, localAddr(f.Spec))
	if f.MappedFrom != 0 {
		text += fmt.Sprintf(" (%d needs privileges here; using %d)", f.MappedFrom, f.Spec.LocalPort)
	}
	return text
}

// forwardStarted logs a forward the first time the model sees it. An
// auto-forward also goes to the status line; a manual one gets its status
// from the forward's result.
func (m *Model) forwardStarted(f engine.Forward) {
	text := describe(f, safetext.Clean(string(f.Host)))
	if !f.Spec.Auto {
		m.addLog(statusOK, text)
		return
	}
	if f.Spec.LocalPort != f.Spec.RemotePort && f.MappedFrom == 0 {
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
// brings back a stopped forward, and only their counters: states and
// errors arrive as events, and a snapshot taken before one must not undo
// it.
func (m *Model) applyStats(msg statsMsg) {
	for _, f := range msg.forwards {
		cur, ok := m.forwards[f.ID]
		if !ok {
			continue
		}
		cur.Stats = f.Stats
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
	m.setStatus(statusOK, "%s", describe(msg.fwd, safetext.Clean(string(msg.fwd.Host))))
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
	m.modal = modalState{kind: modalConflict, host: m.host, spec: spec, busy: inUse.Port, bind: inUse.Addr, suggested: inUse.Suggested}
	return nil
}

func (m *Model) applyStopResult(msg stopResultMsg) {
	if msg.err != nil {
		m.event(statusErr, "%s", msg.err.Error())
		return
	}
	what := ":" + itoa(int(msg.remote))
	if msg.remote == 0 {
		what = "the SOCKS proxy"
	}
	if _, had := m.forwards[msg.id]; had {
		m.dropForward(msg.id)
		m.addLog(statusOK, "Stopped "+what)
	}
	m.selected = portKey(msg.host, msg.remote)
	m.setStatus(statusOK, "Stopped %s", what)
	m.rebuild()
}

func portKey(host engine.HostID, p uint16) string { return "p:" + string(host) + ":" + itoa(int(p)) }

// rebuild recomputes the rows: forwards first, then detected ports that
// are not forwarded, each sorted by port, minus hidden ports and rows the
// filter doesn't match. The cursor stays on the selected row's key, or on
// the same index when that row is gone.
func (m *Model) rebuild() {
	m.total, m.hidden = 0, 0
	var fwdRows, portRows []row
	hosts := []engine.HostID{m.host}
	if m.all {
		hosts = m.hosts
	}
	for _, h := range hosts {
		f, p := m.hostRows(h, m.views[h])
		fwdRows, portRows = append(fwdRows, f...), append(portRows, p...)
	}
	rows := append(fwdRows, portRows...)
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

// hostRows returns host's forward rows and its detected-port rows that
// pass the filter, and counts them in total and hidden.
func (m *Model) hostRows(host engine.HostID, v *hostView) (fwdRows, portRows []row) {
	byPort := make(map[uint16]*engine.Port, len(v.ports))
	for i := range v.ports {
		byPort[v.ports[i].Port] = &v.ports[i]
	}
	var fwds []engine.Forward
	for _, f := range m.forwards {
		if f.Host == host {
			fwds = append(fwds, f)
		}
	}
	slices.SortFunc(fwds, func(a, b engine.Forward) int {
		if a.Spec.RemotePort != b.Spec.RemotePort {
			return int(a.Spec.RemotePort) - int(b.Spec.RemotePort)
		}
		return compareIDs(a.ID, b.ID)
	})

	// forwarded are server ports a local forward covers; own are the
	// server-side listeners of remote forwards, which are pfwd's.
	forwarded, own := make(map[uint16]bool), make(map[uint16]bool)
	for i := range fwds {
		f := &fwds[i]
		r := row{key: "f:" + f.ID, host: host, fwd: f}
		switch f.Spec.Kind {
		case engine.Local:
			forwarded[f.Spec.RemotePort] = true
			r.port = byPort[f.Spec.RemotePort]
		case engine.Remote:
			own[f.Spec.RemotePort] = true
		}
		m.total++
		if m.matches(r) {
			fwdRows = append(fwdRows, r)
		}
	}
	for i := range v.ports {
		p := &v.ports[i]
		switch {
		case forwarded[p.Port], own[p.Port]:
			continue
		case p.Ignored && !m.showHidden:
			m.hidden++
			continue
		}
		m.total++
		if r := (row{key: portKey(host, p.Port), host: host, port: p}); m.matches(r) {
			portRows = append(portRows, r)
		}
	}
	return fwdRows, portRows
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
	if r.fwd != nil && r.fwd.Spec.Kind == engine.Dynamic {
		return "SOCKS"
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

// activeForwards counts the forwards quitting would stop: active ones, and
// paused ones whose listeners still wait.
func (m Model) activeForwards() int {
	n := 0
	for _, f := range m.forwards {
		if f.State == engine.Active || f.State == engine.Paused {
			n++
		}
	}
	return n
}

func (m Model) forwardCmd(spec engine.ForwardSpec) tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		f, err := eng.Forward(host, spec)
		return forwardResultMsg{host: host, spec: spec, fwd: f, err: err}
	}
}

// enterCmd forwards a detected port with enter: a taken local port then
// follows Options.OnConflict.
func (m Model) enterCmd(spec engine.ForwardSpec) tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		f, err := eng.Forward(host, spec)
		return forwardResultMsg{host: host, spec: spec, fwd: f, err: err, resolve: true}
	}
}

// randomPortCmd forwards spec to any free local port.
func (m Model) randomPortCmd(spec engine.ForwardSpec) tea.Cmd {
	eng, host, bind := m.eng, m.host, m.opts.BindAddr
	return func() tea.Msg {
		port, err := forward.FreePort(bind)
		if err != nil {
			return forwardResultMsg{host: host, spec: spec, err: err}
		}
		spec.LocalPort = port
		f, err := eng.Forward(host, spec)
		return forwardResultMsg{host: host, spec: spec, fwd: f, err: err}
	}
}

func (m Model) stopCmd(f engine.Forward) tea.Cmd {
	eng, id, host, remote := m.eng, f.ID, f.Host, f.Spec.RemotePort
	return func() tea.Msg {
		return stopResultMsg{id: id, host: host, remote: remote, err: eng.Stop(id)}
	}
}

func (m Model) setAutoCmd(on bool) tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		eng.SetAuto(host, on)
		return nil
	}
}

func (m Model) reconnectCmd() tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		eng.Reconnect(host)
		return nil
	}
}

func (m Model) stopRetryingCmd() tea.Cmd {
	eng, host := m.eng, m.host
	return func() tea.Msg {
		eng.StopRetrying(host)
		return nil
	}
}

func (m Model) pinCmd(f engine.Forward, pinned bool) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		err := eng.SetPinned(f.ID, pinned)
		return pinResultMsg{port: f.Spec.RemotePort, pinned: pinned, err: err}
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

// localAddr is a forward's address on this machine: where a local or SOCKS
// forward listens, or where a remote forward delivers.
func localAddr(s engine.ForwardSpec) string {
	host := s.BindAddr
	if s.Kind == engine.Remote {
		host = s.RemoteHost
	}
	if host == "" || isLoopback(host) {
		return "localhost:" + itoa(int(s.LocalPort))
	}
	return net.JoinHostPort(host, itoa(int(s.LocalPort)))
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
	switch f.Spec.Kind {
	case engine.Dynamic:
		return "socks5h://" + addr, false
	case engine.Remote:
		return localAddr(f.Spec), false
	}
	switch f.Scheme {
	case forward.SchemeNone:
		return addr, false
	case forward.SchemeHTTPS:
		return "https://" + addr, true
	default:
		return "http://" + addr, true
	}
}
