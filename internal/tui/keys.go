package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/engine"
)

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" && m.modal.kind != modalQuit {
		var answer tea.Cmd
		if m.modal.kind == modalHostKey {
			answer = answerCmd(m.modal.prompt, false)
		}
		m.modal = modalState{}
		next, quit := m.requestQuit()
		return next, tea.Batch(answer, quit)
	}
	if m.modal.kind != modalNone {
		return m.handleModalKey(k)
	}

	switch k {
	case "up", "k":
		m.setCursor(m.cursor - 1)
	case "down", "j":
		m.setCursor(m.cursor + 1)
	case "g", "home":
		m.setCursor(0)
	case "G", "end":
		m.setCursor(len(m.rows) - 1)
	case "enter", "f":
		r, ok := m.current()
		switch {
		case !ok:
		case r.fwd != nil:
			return m, m.stopCmd(*r.fwd)
		default:
			return m, m.forwardCmd(specFor(*r.port))
		}
	case "F":
		r, ok := m.current()
		switch {
		case !ok:
		case r.fwd != nil:
			m.setStatus(statusInfo, ":%d is already forwarded; stop it with x first", r.fwd.Spec.RemotePort)
		default:
			m.modal = modalState{kind: modalPort, spec: specFor(*r.port)}
		}
	case "x":
		r, ok := m.current()
		switch {
		case !ok:
		case r.fwd != nil:
			return m, m.stopCmd(*r.fwd)
		default:
			m.setStatus(statusInfo, ":%d is not forwarded", r.port.Port)
		}
	case "?":
		m.modal = modalState{kind: modalHelp}
	case "q":
		return m.requestQuit()
	}
	return m, nil
}

// requestQuit quits, or asks first while forwards are active.
func (m Model) requestQuit() (Model, tea.Cmd) {
	if n := m.activeForwards(); n > 0 {
		m.modal = modalState{kind: modalQuit, active: n}
		return m, nil
	}
	return m, tea.Quit
}

func answerCmd(p engine.HostKeyPrompt, ok bool) tea.Cmd {
	return func() tea.Msg {
		p.Answer(ok)
		return nil
	}
}

func (m Model) handleModalKey(k string) (tea.Model, tea.Cmd) {
	switch m.modal.kind {
	case modalHelp:
		if k == "?" || k == "esc" || k == "q" {
			m.modal = modalState{}
		}
	case modalHostKey:
		switch k {
		case "y":
			p := m.modal.prompt
			m.modal = modalState{}
			return m, answerCmd(p, true)
		case "n", "esc":
			p := m.modal.prompt
			m.modal = modalState{}
			return m, answerCmd(p, false)
		}
	case modalQuit:
		switch k {
		case "y", "ctrl+c":
			return m, tea.Quit
		case "n", "esc", "q":
			m.modal = modalState{}
		}
	case modalConflict:
		switch k {
		case "enter":
			if s := m.modal.suggested; s != 0 {
				spec := m.modal.spec
				spec.LocalPort = s
				m.modal = modalState{}
				return m, m.forwardCmd(spec)
			}
		case "F":
			m.modal = modalState{kind: modalPort, spec: m.modal.spec}
		case "esc":
			m.modal = modalState{}
		}
	case modalPort:
		return m.handlePortKey(k)
	}
	return m, nil
}

func (m Model) handlePortKey(k string) (tea.Model, tea.Cmd) {
	switch {
	case k == "esc":
		m.modal = modalState{}
	case k == "enter":
		port, ok := parsePort(m.modal.input)
		if !ok {
			m.modal.inputErr = "Enter a port from 1 to 65535."
			return m, nil
		}
		spec := m.modal.spec
		spec.LocalPort = port
		m.modal = modalState{}
		return m, m.forwardCmd(spec)
	case k == "backspace":
		if n := len(m.modal.input); n > 0 {
			m.modal.input = m.modal.input[:n-1]
		}
		m.modal.inputErr = ""
	case len(k) == 1 && k[0] >= '0' && k[0] <= '9':
		if len(m.modal.input) < 5 {
			m.modal.input += k
		}
		m.modal.inputErr = ""
	}
	return m, nil
}

func parsePort(s string) (uint16, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	if n < 1 || n > 65535 {
		return 0, false
	}
	return uint16(n), true
}
