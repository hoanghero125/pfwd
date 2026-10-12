package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/engine"
)

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.tooSmall() {
		// Nothing else is on screen to act on.
		if k == "q" || k == "ctrl+c" {
			return m.quitNow()
		}
		return m, nil
	}
	if k == "ctrl+c" && m.modal.kind != modalQuit {
		answer := m.declinePrompt()
		next, quit := m.requestQuit()
		return next, tea.Batch(answer, quit)
	}
	switch {
	case m.modal.kind == modalCredential:
		return m.handleCredentialKey(msg)
	case m.modal.kind != modalNone:
		return m.handleModalKey(k)
	case m.filtering:
		return m.handleFilterKey(msg)
	case m.logOpen:
		return m.handleLogKey(k)
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
			return m, m.enterCmd(specFor(*r.port))
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
	case "c":
		host := m.host
		switch m.session {
		case engine.Reconnecting, engine.SessionFailed:
			m.setStatus(statusInfo, "Reconnecting to %s%s", host, m.th.g.dots)
			return m, m.reconnectCmd()
		case engine.Connected:
			m.setStatus(statusInfo, "Already connected to %s", host)
		}
	case "p":
		f, ok := m.selectedForward()
		if !ok {
			return m, nil
		}
		return m, m.pinCmd(f, !f.Spec.Pinned)
	case "x":
		if m.session == engine.Reconnecting {
			m.setStatus(statusInfo, "Stopped reconnecting; c tries again")
			return m, m.stopRetryingCmd()
		}
		r, ok := m.current()
		switch {
		case !ok:
		case r.fwd != nil:
			return m, m.stopCmd(*r.fwd)
		default:
			m.setStatus(statusInfo, ":%d is not forwarded", r.port.Port)
		}
	case "o":
		return m.openSelected()
	case "y":
		return m.copySelected()
	case "a":
		return m, m.setAutoCmd(!m.auto)
	case "/":
		m.filtering = true
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.rebuild()
		}
	case "h":
		m.showHidden = !m.showHidden
		m.rebuild()
	case "L":
		m.logOpen, m.logScroll = true, 0
	case "?":
		m.modal = modalState{kind: modalHelp}
	case "q":
		return m.requestQuit()
	}
	return m, nil
}

// selectedForward is the forward on the selected row; otherwise it sets a
// status saying why there is none.
func (m *Model) selectedForward() (engine.Forward, bool) {
	r, ok := m.current()
	switch {
	case !ok:
		return engine.Forward{}, false
	case r.fwd == nil:
		m.setStatus(statusInfo, ":%d is not forwarded; press enter first", r.port.Port)
		return engine.Forward{}, false
	}
	return *r.fwd, true
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	f, ok := m.selectedForward()
	if !ok {
		return m, nil
	}
	url, isURL := target(f)
	switch {
	case !isURL:
		m.setStatus(statusInfo, ":%d doesn't speak HTTP; y copies its address", f.Spec.RemotePort)
		return m, nil
	case m.opts.Open == nil:
		m.setStatus(statusInfo, "Open %s in your browser", url)
		return m, nil
	}
	m.setStatus(statusInfo, "Opening %s", url)
	return m, m.openCmd(url)
}

func (m Model) copySelected() (tea.Model, tea.Cmd) {
	f, ok := m.selectedForward()
	if !ok {
		return m, nil
	}
	text, _ := target(f)
	if m.opts.Copy == nil {
		m.setStatus(statusInfo, "Sent %s to the terminal's clipboard", text)
		return m, tea.SetClipboard(text)
	}
	return m, tea.Batch(tea.SetClipboard(text), m.copyCmd(text))
}

// requestQuit quits, or asks first while forwards are active.
func (m Model) requestQuit() (Model, tea.Cmd) {
	if n := m.activeForwards(); n > 0 && !m.opts.SkipQuitConfirm {
		m.modal = modalState{kind: modalQuit, active: n}
		return m, nil
	}
	return m, tea.Quit
}

// quitNow quits without asking, refusing an open prompt.
func (m Model) quitNow() (tea.Model, tea.Cmd) {
	answer := m.declinePrompt()
	return m, tea.Batch(answer, tea.Quit)
}

// declinePrompt closes the open modal; an open host-key question is
// answered no, and a credential prompt canceled, with what was typed
// zeroed. Queued credential prompts are canceled too.
func (m *Model) declinePrompt() tea.Cmd {
	var cmds []tea.Cmd
	switch m.modal.kind {
	case modalHostKey:
		cmds = append(cmds, answerCmd(m.modal.prompt, false))
	case modalCredential:
		clear(m.modal.secret[:cap(m.modal.secret)])
		cmds = append(cmds, cancelCredCmd(m.modal.cred))
	}
	for _, p := range m.creds {
		cmds = append(cmds, cancelCredCmd(p))
	}
	m.modal, m.creds = modalState{}, nil
	return tea.Batch(cmds...)
}

func answerCmd(p engine.HostKeyPrompt, ok bool) tea.Cmd {
	return func() tea.Msg {
		p.Answer(ok)
		return nil
	}
}

func answerCredCmd(p engine.CredentialPrompt, secret []byte) tea.Cmd {
	return func() tea.Msg {
		p.Answer(secret)
		return nil
	}
}

func cancelCredCmd(p engine.CredentialPrompt) tea.Cmd {
	return func() tea.Msg {
		p.Cancel()
		return nil
	}
}

// handleCredentialKey edits the credential field. Every printable key is
// text, q included; enter sends it and esc cancels.
func (m Model) handleCredentialKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		secret := append([]byte(nil), m.modal.secret...)
		clear(m.modal.secret[:cap(m.modal.secret)])
		p := m.modal.cred
		m.modal = modalState{}
		return m, answerCredCmd(p, secret)
	case "esc":
		clear(m.modal.secret[:cap(m.modal.secret)])
		p := m.modal.cred
		m.modal = modalState{}
		return m, cancelCredCmd(p)
	case "backspace":
		if _, n := utf8.DecodeLastRune(m.modal.secret); n > 0 {
			end := len(m.modal.secret) - n
			clear(m.modal.secret[end:])
			m.modal.secret = m.modal.secret[:end]
		}
	default:
		if isText(msg) {
			m.typeSecret(msg.Text)
		}
	}
	return m, nil
}

// isText reports whether a key press types text: no Ctrl or Alt, or both
// at once, which is how Windows reports AltGr (@, \, {, } on many
// layouts).
func isText(msg tea.KeyPressMsg) bool {
	mods := msg.Mod & (tea.ModCtrl | tea.ModAlt)
	return msg.Text != "" && (mods == 0 || mods == tea.ModCtrl|tea.ModAlt)
}

// typeSecret adds text to the credential field, within its fixed buffer.
// Line breaks from a paste are dropped.
func (m *Model) typeSecret(text string) {
	text = strings.NewReplacer("\r", "", "\n", "").Replace(text)
	if len(m.modal.secret)+len(text) <= cap(m.modal.secret) {
		m.modal.secret = append(m.modal.secret, text...)
	}
}

// handleModalKey drives the open modal. In every modal, esc and q back
// out; in the host-key modal that means "don't connect".
func (m Model) handleModalKey(k string) (tea.Model, tea.Cmd) {
	switch m.modal.kind {
	case modalHelp:
		switch k {
		case "?", "esc", "q":
			m.modal = modalState{}
		case "up", "k":
			m.modal.scroll = max(0, m.modal.scroll-1)
		case "down", "j":
			m.modal.scroll = min(m.modal.scroll+1, len(helpKeys(m.th)))
		}
	case modalHostKey:
		switch k {
		case "y":
			p := m.modal.prompt
			m.modal = modalState{}
			return m, answerCmd(p, true)
		case "n", "esc", "q":
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
		case "esc", "q":
			m.modal = modalState{}
		}
	case modalPort:
		return m.handlePortKey(k)
	}
	return m, nil
}

func (m Model) handlePortKey(k string) (tea.Model, tea.Cmd) {
	switch {
	case k == "esc" || k == "q":
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

// handleFilterKey edits the filter. Every printable key is text, q
// included; enter keeps the filter and esc clears it.
func (m Model) handleFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filtering = false
	case "esc":
		m.filter, m.filtering = "", false
		m.rebuild()
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
			m.rebuild()
		}
	case "up":
		m.setCursor(m.cursor - 1)
	case "down":
		m.setCursor(m.cursor + 1)
	default:
		if isText(msg) && len([]rune(m.filter)) < maxFilter {
			m.filter += msg.Text
			m.rebuild()
		}
	}
	return m, nil
}

// handleLogKey scrolls the event log; L, esc and q close it.
func (m Model) handleLogKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "up", "k":
		m.logScroll = min(m.logScroll+1, m.maxLogScroll())
	case "down", "j":
		m.logScroll = max(m.logScroll-1, 0)
	case "g", "home":
		m.logScroll = m.maxLogScroll()
	case "G", "end":
		m.logScroll = 0
	case "L", "esc", "q":
		m.logOpen = false
	case "?":
		m.modal = modalState{kind: modalHelp}
	}
	return m, nil
}

// handleClick selects the table row under the pointer.
func (m *Model) handleClick(p tea.Mouse) {
	if p.Button != tea.MouseLeft || m.modal.kind != modalNone || m.logOpen || m.tooSmall() {
		return
	}
	// The table starts under the host bar, the rule and the header.
	line := p.Y - 3
	if line < 0 || line >= m.visibleRows() {
		return
	}
	line += m.offset
	if sep := m.separatorAt(); sep >= 0 {
		if line == sep {
			return
		}
		if line > sep {
			line--
		}
	}
	if line < len(m.rows) {
		m.setCursor(line)
	}
}

// handleWheel moves the cursor, or scrolls the log, one line.
func (m *Model) handleWheel(p tea.Mouse) {
	if m.modal.kind != modalNone || m.tooSmall() {
		return
	}
	step := 0
	switch p.Button {
	case tea.MouseWheelUp:
		step = -1
	case tea.MouseWheelDown:
		step = 1
	}
	if m.logOpen {
		m.logScroll = max(0, min(m.logScroll-step, m.maxLogScroll()))
		return
	}
	m.setCursor(m.cursor + step)
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
