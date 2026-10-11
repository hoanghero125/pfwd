package tui

import (
	"net"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
)

// The smallest window pfwd draws in; below it shows a "too small" note.
const (
	minWidth  = 60
	minHeight = 12
)

// View renders the full screen in the alternate screen buffer.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "pfwd: " + clean(string(m.host))
	return v
}

func (m Model) render() string {
	w, h := m.width, m.height
	if w < minWidth || h < minHeight {
		lines := make([]string, h)
		lines[h/2] = "  pfwd needs a window of at least 60×12."
		return strings.Join(fit(lines, w), "\n")
	}
	lines := make([]string, 0, h)
	lines = append(lines, m.hostBar(w), m.rule(w))
	lines = append(lines, m.body(w, h-5)...)
	lines = append(lines, m.rule(w), m.statusLine(), m.hints())
	lines = fit(lines, w)
	if m.modal.kind != modalNone {
		lines = overlay(lines, m.modalLines(), w)
	}
	return strings.Join(lines, "\n")
}

// fit makes every line exactly w cells: longer ones are cut, shorter ones
// padded.
func fit(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = pad(ansi.Truncate(l, w, ""), w)
	}
	return out
}

// pad right-pads s with spaces to w cells.
func pad(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// cell fits s into exactly w cells, cutting with an ellipsis.
func cell(s string, w int) string {
	return pad(ansi.Truncate(s, w, "…"), w)
}

func (m Model) rule(w int) string {
	return m.th.faint.Render(strings.Repeat("─", w))
}

func (m Model) hostBar(w int) string {
	th := m.th
	var state string
	switch m.session {
	case engine.Connected:
		state = th.ok.Render("●") + " connected"
	case engine.SessionFailed:
		state = th.bad.Render("✗") + " failed"
	case engine.Closed:
		state = th.faint.Render("○ closed")
	default:
		state = th.warn.Render("◐") + " connecting"
	}
	left := " " + th.bold.Render("pfwd") + "  " + clean(string(m.host)) + " " + state
	right := th.accent.Render("?") + " " + th.faint.Render("help") + " "
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) statusLine() string {
	s := m.status
	if s.text == "" || m.now().Sub(s.at) >= statusFor {
		return ""
	}
	switch s.kind {
	case statusOK:
		return " " + m.th.ok.Render("✓") + " " + clean(s.text)
	case statusErr:
		return " " + m.th.bad.Render("✗") + " " + clean(s.text)
	default:
		return " " + clean(s.text)
	}
}

func (m Model) hints() string {
	if m.session != engine.Connected {
		return " " + m.hint("q", "quit")
	}
	if len(m.rows) == 0 {
		return " " + strings.ReplaceAll(m.hint("?", "help", "q", "quit"), "   ", "  ")
	}
	return " " + strings.ReplaceAll(m.hint("enter", "forward/stop", "F", "other local port", "x", "stop", "?", "help", "q", "quit"), "   ", "  ")
}

// body is the area between the top and bottom rules, exactly h lines.
func (m Model) body(w, h int) []string {
	var lines []string
	host := clean(string(m.host))
	switch {
	case m.session == engine.SessionFailed:
		title := "Can't connect to " + host
		if m.wasConnected {
			title = "Lost the connection to " + host
		}
		lines = append(lines, "", "  "+m.th.bad.Render("✗ "+title), "")
		if m.sessErr != nil {
			for _, l := range strings.Split(ansi.Wordwrap(clean(m.sessErr.Error()), w-4, ""), "\n") {
				lines = append(lines, "  "+l)
			}
			lines = append(lines, "")
		}
		lines = append(lines, "  Press q to quit.")
	case m.session != engine.Connected:
		lines = append(lines, "", "  Connecting to "+host+"…")
	case len(m.rows) > 0:
		return m.table(w, h)
	case !m.scanned:
		lines = append(lines, "", "  Connected. Looking for listening ports on "+host+"…")
	case m.strategy == detect.None:
		lines = append(lines, "")
		for _, l := range m.unavailable() {
			for _, wl := range strings.Split(ansi.Wordwrap(l, w-4, ""), "\n") {
				lines = append(lines, "  "+wl)
			}
		}
	default:
		lines = append(lines, "", "  No listening ports yet. Start a server on "+host+" and it will appear here.")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

func (m Model) unavailable() []string {
	host := clean(string(m.host))
	switch {
	case m.detectErr != nil:
		return []string{"Can't list ports on " + host + ": " + clean(m.detectErr.Error())}
	case m.remoteOS != "" && m.remoteOS != "Linux":
		return []string{
			"Can't list ports on " + host + " yet: it runs " + clean(m.remoteOS) + ", and pfwd reads Linux servers today.",
			"macOS, BSD and Windows servers come in v0.4.0.",
		}
	default:
		return []string{"Can't list ports on " + host + ": it has neither ss nor a readable /proc/net/tcp."}
	}
}

// columns is the table layout for a width. TRAFFIC goes first as the
// window narrows, then CONN.
type columns struct {
	state, remote, local, process, conn, traffic int
}

func (m Model) columns(w int) columns {
	c := columns{state: 9, remote: 8, local: 17}
	for _, r := range m.rows {
		c.remote = max(c.remote, ansi.StringWidth(remoteText(r))+2)
	}
	c.remote = min(c.remote, 24)
	if w >= 68 {
		c.conn = 6
	}
	if w >= 76 {
		c.traffic = 16
	}
	// PROCESS takes the spare width, up to 40 cells, so the columns stay
	// together on wide windows.
	c.process = min(40, max(6, w-2-c.state-c.remote-c.local-c.conn-c.traffic-1))
	return c
}

func (m Model) table(w, h int) []string {
	th := m.th
	c := m.columns(w)
	header := "  " + cell("STATE", c.state) + cell("REMOTE", c.remote) + cell("LOCAL", c.local) + cell("PROCESS", c.process)
	if c.conn > 0 {
		header += cell("CONN", c.conn)
	}
	if c.traffic > 0 {
		header += "TRAFFIC"
	}
	lines := []string{th.faint.Render(header)}

	var body []string
	cursorLine := 0
	sepDone := false
	for i, r := range m.rows {
		if r.fwd == nil && !sepDone {
			sepDone = true
			if i > 0 {
				body = append(body, m.separator(w))
			}
		}
		if i == m.cursor {
			cursorLine = len(body)
		}
		body = append(body, m.rowLine(r, i == m.cursor, c))
	}

	visible := h - 1
	offset := max(0, cursorLine-visible+1)
	end := min(len(body), offset+visible)
	lines = append(lines, body[offset:end]...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

func (m Model) separator(w int) string {
	label := " detected, not forwarded "
	n := max(0, w-4-ansi.StringWidth(label))
	return "  " + m.th.faint.Render(strings.Repeat("─", n/2)+label+strings.Repeat("─", n-n/2))
}

func remoteText(r row) string {
	var host string
	var port uint16
	if r.fwd != nil {
		host, port = r.fwd.Spec.RemoteHost, r.fwd.Spec.RemotePort
	} else {
		host, port = detect.Target(r.port.Addrs), r.port.Port
	}
	switch host {
	case "", "127.0.0.1", "::1", "localhost":
		return ":" + itoa(int(port))
	}
	return net.JoinHostPort(host, itoa(int(port)))
}

func (m Model) rowLine(r row, selected bool, c columns) string {
	th := m.th
	cursor := "  "
	if selected {
		cursor = th.accent.Render("▶") + " "
	}
	dash := th.faint.Render("—")

	var state, local, process, conn, traffic string
	if f := r.fwd; f != nil {
		switch f.State {
		case engine.Active:
			state = th.ok.Render("●") + " fwd"
			local = th.bold.Render(localAddr(f.Spec))
		case engine.Failed:
			state = th.bad.Render("✗") + " fail"
			local = localAddr(f.Spec)
		case engine.Paused:
			state = th.warn.Render("◌") + " pause"
			local = localAddr(f.Spec)
		default:
			state = "… start"
			local = localAddr(f.Spec)
		}
		switch {
		case f.State == engine.Failed && f.Err != nil:
			process = th.faint.Render(clean(f.Err.Error()))
		case r.port == nil:
			process = th.faint.Render("not listening")
		default:
			process = m.processText(*r.port)
		}
		conn = itoa(int(f.Stats.Conns))
		traffic = dash
		if f.State == engine.Active {
			traffic = m.trafficText(f.ID)
		}
	} else {
		state, local, conn, traffic = dash, dash, dash, dash
		process = m.processText(*r.port)
	}

	line := cursor + cell(state, c.state) + cell(remoteText(r), c.remote) + cell(local, c.local) + cell(process, c.process)
	if c.conn > 0 {
		line += cell(conn, c.conn)
	}
	if c.traffic > 0 {
		line += traffic
	}
	return line
}

func (m Model) processText(p detect.RemotePort) string {
	if p.Process == "" {
		return m.th.faint.Render("?")
	}
	return clean(p.Process)
}

func (m Model) trafficText(id string) string {
	r := m.rates[id]
	if r.in == 0 && r.out == 0 {
		return "↓0 ↑0"
	}
	return "↓" + formatRate(r.in) + "/s ↑" + formatRate(r.out) + "/s"
}
