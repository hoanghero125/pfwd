package tui

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
)

// Engine is the part of *engine.Engine the TUI uses.
type Engine interface {
	Events() <-chan engine.Event
	Forward(host engine.HostID, spec engine.ForwardSpec) (engine.Forward, error)
	Stop(id string) error
	Forwards() []engine.Forward
}

// statusFor is how long the status line shows the last event.
const statusFor = 5 * time.Second

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
	port *detect.RemotePort
}

// Model is the TUI state. Create it with New.
type Model struct {
	eng   Engine
	host  engine.HostID
	th    theme
	now   func() time.Time
	every func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	width, height int

	session      engine.SessionState
	sessErr      error
	wasConnected bool

	scanned   bool
	strategy  detect.Strategy
	remoteOS  string
	detectErr error
	ports     []detect.RemotePort

	forwards map[string]engine.Forward
	samples  map[string]sample
	rates    map[string]rate

	rows     []row
	cursor   int
	selected string

	modal  modalState
	status status
}

// New returns the model for one host. Connect the engine to the host
// separately; the model reacts to its events.
func New(e Engine, host engine.HostID) Model {
	return Model{
		eng:      e,
		host:     host,
		th:       newTheme(),
		now:      time.Now,
		every:    tea.Tick,
		width:    80,
		height:   24,
		forwards: make(map[string]engine.Forward),
		samples:  make(map[string]sample),
		rates:    make(map[string]rate),
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
	}
	stopResultMsg struct {
		id     string
		remote uint16
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

// Init starts listening for engine events and the 1 s stats tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(waitEvent(m.eng.Events()), m.tick())
}

// Update applies a message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
		m.applyForwardResult(msg)
	case stopResultMsg:
		m.applyStopResult(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) setStatus(kind statusKind, format string, args ...any) {
	m.status = status{text: fmt.Sprintf(format, args...), kind: kind, at: m.now()}
}

func (m *Model) applyEvent(ev engine.Event) {
	switch ev := ev.(type) {
	case engine.SessionChanged:
		if ev.Host != m.host {
			return
		}
		m.session = ev.State
		switch ev.State {
		case engine.Connected:
			m.wasConnected = true
		case engine.SessionFailed:
			m.sessErr = ev.Err
			if m.modal.kind == modalHostKey {
				m.modal = modalState{}
			}
		}
	case engine.PortsChanged:
		if ev.Host != m.host {
			return
		}
		m.scanned = true
		m.strategy, m.remoteOS, m.detectErr, m.ports = ev.Strategy, ev.OS, ev.Err, ev.Ports
	case engine.ForwardChanged:
		f := ev.Forward
		if f.Host != m.host {
			return
		}
		if f.State == engine.Stopped {
			m.dropForward(f.ID)
		} else {
			m.forwards[f.ID] = f
		}
	case engine.HostKeyPrompt:
		if ev.Host == m.host {
			m.modal = modalState{kind: modalHostKey, prompt: ev}
		}
	}
	m.rebuild()
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

func (m *Model) applyForwardResult(msg forwardResultMsg) {
	if msg.err != nil {
		var inUse *forward.PortInUseError
		if errors.As(msg.err, &inUse) {
			m.modal = modalState{kind: modalConflict, spec: msg.spec, busy: inUse.Port, bind: inUse.Addr, suggested: inUse.Suggested}
			return
		}
		m.setStatus(statusErr, "%s", msg.err.Error())
		return
	}
	if _, ok := m.forwards[msg.fwd.ID]; !ok {
		m.forwards[msg.fwd.ID] = msg.fwd
	}
	m.selected = "f:" + msg.fwd.ID
	m.setStatus(statusOK, "Forwarded :%d to %s", msg.fwd.Spec.RemotePort, localAddr(msg.fwd.Spec))
	m.rebuild()
}

func (m *Model) applyStopResult(msg stopResultMsg) {
	if msg.err != nil {
		m.setStatus(statusErr, "%s", msg.err.Error())
		return
	}
	m.dropForward(msg.id)
	m.selected = portKey(msg.remote)
	m.setStatus(statusOK, "Stopped :%d", msg.remote)
	m.rebuild()
}

func portKey(p uint16) string { return "p:" + itoa(int(p)) }

// rebuild recomputes the rows: forwards first, then detected ports that
// are not forwarded, each sorted by port. The cursor stays on the selected
// row's key, or on the same index when that row is gone.
func (m *Model) rebuild() {
	byPort := make(map[uint16]*detect.RemotePort, len(m.ports))
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

	rows := make([]row, 0, len(fwds)+len(m.ports))
	forwarded := make(map[uint16]bool)
	for i := range fwds {
		f := &fwds[i]
		forwarded[f.Spec.RemotePort] = true
		rows = append(rows, row{key: "f:" + f.ID, fwd: f, port: byPort[f.Spec.RemotePort]})
	}
	for i := range m.ports {
		if p := &m.ports[i]; !forwarded[p.Port] {
			rows = append(rows, row{key: portKey(p.Port), port: p})
		}
	}
	m.rows = rows

	idx := slices.IndexFunc(rows, func(r row) bool { return r.key == m.selected })
	if idx < 0 {
		idx = min(m.cursor, len(rows)-1)
	}
	m.setCursor(idx)
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
		m.cursor, m.selected = 0, ""
		return
	}
	m.cursor = max(0, min(i, len(m.rows)-1))
	m.selected = m.rows[m.cursor].key
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

func (m Model) stopCmd(f engine.Forward) tea.Cmd {
	eng, id, remote := m.eng, f.ID, f.Spec.RemotePort
	return func() tea.Msg {
		return stopResultMsg{id: id, remote: remote, err: eng.Stop(id)}
	}
}

// specFor is the forward for a detected port: same local port, dialing
// the address the server listens on.
func specFor(p detect.RemotePort) engine.ForwardSpec {
	return engine.ForwardSpec{RemotePort: p.Port, LocalPort: p.Port, RemoteHost: detect.Target(p.Addrs)}
}

// localAddr is how a forward's local end is shown: localhost:PORT on
// loopback, bind:PORT otherwise.
func localAddr(s engine.ForwardSpec) string {
	switch s.BindAddr {
	case "", "127.0.0.1", "::1", "localhost":
		return "localhost:" + itoa(int(s.LocalPort))
	}
	return net.JoinHostPort(s.BindAddr, itoa(int(s.LocalPort)))
}
