package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/safetext"
)

// hostsMsg carries the hosts the picker offers.
type hostsMsg struct {
	hosts []HostChoice
	err   error
}

// hostAddedMsg reports a host picked with +, connected or not.
type hostAddedMsg struct {
	name string
	host engine.HostID
	err  error
}

// onHost runs fn with h as the current host, and makes the host that was
// current current again on the model fn returns. Commands fn makes act on
// h.
func (m Model) onHost(h engine.HostID, fn func(Model) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	v := m.views[h]
	if h == "" || h == m.host || v == nil {
		return fn(m)
	}
	host, view := m.host, m.hostView
	m.host, m.hostView = h, v
	next, cmd := fn(m)
	if nm, ok := next.(Model); ok {
		nm.host, nm.hostView = host, view
		nm.rebuild()
		return nm, cmd
	}
	return next, cmd
}

// switchHost makes the tab at index i current.
func (m *Model) switchHost(i int) {
	n := len(m.hosts)
	h := m.hosts[((i%n)+n)%n]
	m.host, m.hostView = h, m.views[h]
	m.all, m.selected, m.selectedPort, m.cursor, m.offset = false, "", 0, 0, 0
	m.reverseOpen = false
	m.rebuild()
}

// hostIndex is the current tab's index.
func (m Model) hostIndex() int {
	return max(0, slices.Index(m.hosts, m.host))
}

// openPicker opens the host picker and loads its list.
func (m *Model) openPicker() tea.Cmd {
	if m.opts.Hosts == nil || m.opts.Connect == nil {
		m.setStatus(statusInfo, "Start pfwd with several hosts, like pfwd devbox gpu-node")
		return nil
	}
	m.picker = pickerState{loading: true}
	m.modal = modalState{kind: modalPicker}
	list := m.opts.Hosts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hosts, err := list(ctx)
		return hostsMsg{hosts: hosts, err: err}
	}
}

// pickerChoices are the hosts the picker shows: not open yet, and
// containing the filter, ignoring case.
func (m Model) pickerChoices() []HostChoice {
	var out []HostChoice
	f := strings.ToLower(m.picker.filter)
	for _, c := range m.picker.choices {
		if !slices.Contains(m.hosts, engine.HostID(c.Name)) && strings.Contains(strings.ToLower(c.Name), f) {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	choices := m.pickerChoices()
	switch k := msg.String(); k {
	case "esc":
		m.modal, m.picker = modalState{}, pickerState{}
	case "up":
		m.picker.cursor = max(0, m.picker.cursor-1)
	case "down":
		m.picker.cursor = max(0, min(m.picker.cursor+1, len(choices)-1))
	case "backspace":
		if r := []rune(m.picker.filter); len(r) > 0 {
			m.picker.filter, m.picker.cursor = string(r[:len(r)-1]), 0
		}
	case "enter":
		if m.picker.cursor >= len(choices) {
			return m, nil
		}
		name := choices[m.picker.cursor].Name
		m.modal, m.picker = modalState{}, pickerState{}
		m.setStatus(statusInfo, "Connecting to %s%s", safetext.Clean(name), m.th.g.dots)
		connect := m.opts.Connect
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h, err := connect(ctx, name)
			return hostAddedMsg{name: name, host: h, err: err}
		}
	default:
		if isText(msg) {
			m.picker.filter += msg.Text
			m.picker.cursor = 0
		}
	}
	return m, nil
}

// addHost opens a tab for a host the picker connected, and makes it
// current.
func (m *Model) addHost(msg hostAddedMsg) {
	if msg.err != nil {
		m.event(statusErr, "%s", msg.err.Error())
		return
	}
	if _, ok := m.views[msg.host]; !ok {
		m.hosts = append(m.hosts, msg.host)
		m.views[msg.host] = &hostView{}
	}
	m.switchHost(slices.Index(m.hosts, msg.host))
	m.addLog(statusInfo, "Opened "+safetext.Clean(string(msg.host)))
}

// pickerLines are the host picker's title and body.
func (m Model) pickerLines() (string, []string) {
	th := m.th
	title := "Connect another host"
	body := []string{m.picker.filter + th.accent.Render(th.g.block)}
	choices := m.pickerChoices()
	switch {
	case m.picker.loading:
		body = append(body, th.faint.Render("Reading your ssh config"+th.g.dots))
	case m.picker.err != nil:
		body = append(body, th.bad.Render(safetext.Clean(m.picker.err.Error())))
	case len(choices) == 0:
		body = append(body, th.faint.Render("No other host matches. pfwd hosts lists the hosts in your ssh config."))
	}
	const shown = 8
	start := max(0, min(m.picker.cursor-shown+1, len(choices)-shown))
	for i := start; i < len(choices) && i < start+shown; i++ {
		c := choices[i]
		cursor := "  "
		if i == m.picker.cursor {
			cursor = th.accent.Render(th.g.cursor) + " "
		}
		last := "never"
		if !c.LastUsed.IsZero() {
			last = agoText(m.now().Sub(c.LastUsed))
		}
		body = append(body, cursor+th.cell(safetext.Clean(c.Name), 24)+th.faint.Render(last))
	}
	body = append(body, "", m.hint("enter", "connect", th.g.upDown, "move", "esc", "cancel"))
	return title, body
}

// agoText says how long ago something was, in one unit.
func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + "m ago"
	case d < 48*time.Hour:
		return itoa(int(d/time.Hour)) + "h ago"
	}
	return itoa(int(d/(24*time.Hour))) + "d ago"
}
