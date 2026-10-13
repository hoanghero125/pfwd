package tui

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/safetext"
)

// localPortsMsg carries this machine's listening ports for the Reverse
// tab.
type localPortsMsg struct {
	ports []detect.RemotePort
	err   error
}

// openReverse opens the Reverse tab and lists this machine's ports.
func (m Model) openReverse() (Model, tea.Cmd) {
	m.reverseOpen, m.localLoading, m.revCursor = true, true, 0
	m.reverseHost = m.host // the selected row's host in the all view
	load := m.opts.LocalPorts
	if load == nil {
		return m, func() tea.Msg {
			return localPortsMsg{err: errors.New("pfwd can't list this machine's ports here")}
		}
	}
	return m, func() tea.Msg {
		ports, err := load(context.Background())
		return localPortsMsg{ports: ports, err: err}
	}
}

func (m *Model) applyLocalPorts(msg localPortsMsg) {
	m.localLoading = false
	m.local, m.localErr = msg.ports, msg.err
	m.revCursor = max(0, min(m.revCursor, len(m.local)-1))
}

// handleReverseKey drives the Reverse tab: enter exposes the selected port
// on the server under the same number, F under another, and esc, q or r
// go back to the table.
func (m Model) handleReverseKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "up", "k":
		m.revCursor = max(0, m.revCursor-1)
	case "down", "j":
		m.revCursor = max(0, min(m.revCursor+1, len(m.local)-1))
	case "enter":
		if p, ok := m.localSelected(); ok {
			m.reverseOpen = false
			return m, m.forwardCmd(engine.ForwardSpec{Kind: engine.Remote, RemotePort: p.Port, LocalPort: p.Port, RemoteHost: detect.Target(p.Addrs)})
		}
	case "F":
		if p, ok := m.localSelected(); ok {
			m.modal = modalState{kind: modalPort, host: m.host, spec: engine.ForwardSpec{Kind: engine.Remote, LocalPort: p.Port, RemoteHost: detect.Target(p.Addrs)}}
		}
	case "esc", "q", "r":
		m.reverseOpen = false
	case "?":
		m.modal = modalState{kind: modalHelp}
	}
	return m, nil
}

func (m Model) localSelected() (detect.RemotePort, bool) {
	if m.localLoading || m.revCursor < 0 || m.revCursor >= len(m.local) {
		return detect.RemotePort{}, false
	}
	return m.local[m.revCursor], true
}

// reversePane replaces the table with this machine's listening ports,
// exactly h lines.
func (m Model) reversePane(w, h int) []string {
	th := m.th
	host := safetext.Clean(string(m.reverseHost))
	lines := []string{th.faint.Render("  This machine's listening ports. enter exposes one on " + host + ".")}
	switch {
	case m.localLoading:
		lines = append(lines, "", "  Looking for listening ports on this machine"+th.g.dots)
	case m.localErr != nil:
		lines = append(lines, "")
		for _, l := range strings.Split(ansi.Wordwrap(safetext.Clean(m.localErr.Error()), w-4, ""), "\n") {
			lines = append(lines, "  "+th.bad.Render(l))
		}
	case len(m.local) == 0:
		lines = append(lines, "", "  Nothing listens on this machine yet. Start a server here, then press r again.")
	default:
		lines = append(lines, th.faint.Render("  "+th.cell("PORT", 9)+th.cell("ADDRESS", 18)+"PROCESS"))
		start := max(0, m.revCursor-(h-4))
		for i := start; i < len(m.local) && len(lines) < h; i++ {
			p := m.local[i]
			proc := safetext.Clean(p.Process)
			if proc == "" {
				proc = th.faint.Render("?")
			}
			line := "  " + th.cell(":"+itoa(int(p.Port)), 9) + th.cell(strings.Join(p.Addrs, " "), 18) + proc
			if i == m.revCursor {
				line = th.selected(th.accent.Render(th.g.cursor)+line[1:], w)
			}
			lines = append(lines, line)
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}
