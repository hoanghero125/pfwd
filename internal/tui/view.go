package tui

import (
	"fmt"
	"net"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/safetext"
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
	v.WindowTitle = "pfwd: " + safetext.Clean(string(m.host))
	if m.opts.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func (m Model) tooSmall() bool {
	return m.width < minWidth || m.height < minHeight
}

func (m Model) render() string {
	w, h := m.width, m.height
	if h <= 0 || w <= 0 {
		return ""
	}
	if m.tooSmall() {
		lines := make([]string, h)
		size := "60×12"
		if m.opts.ASCII {
			size = "60x12"
		}
		lines[h/2] = "  pfwd needs a window of at least " + size + ". q quits."
		return strings.Join(fit(lines, w), "\n")
	}
	lines := make([]string, 0, h)
	lines = append(lines, m.hostBar(w), m.rule(w))
	if m.logOpen {
		lines = append(lines, m.logPane(w, h-5)...)
	} else {
		lines = append(lines, m.body(w, h-5)...)
	}
	lines = append(lines, m.viewLine(w), m.statusLine(), m.hints(w))
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

func (m Model) rule(w int) string {
	return m.th.faint.Render(strings.Repeat(m.th.g.rule, w))
}

func (m Model) hostBar(w int) string {
	th, g := m.th, m.th.g
	var state string
	switch m.session {
	case engine.Connected:
		state = th.ok.Render(g.active) + " connected"
	case engine.SessionFailed:
		state = th.bad.Render(g.failed) + " failed"
	case engine.Closed:
		state = th.faint.Render(g.closed + " closed")
	default:
		state = th.warn.Render(g.connecting) + " connecting"
	}
	var badges []string
	if !isLoopback(m.opts.BindAddr) {
		badges = append(badges, th.warn.Render("bind "+safetext.Clean(m.opts.BindAddr)))
	}
	if m.autoKnown {
		if m.auto {
			badges = append(badges, "auto: "+th.ok.Render("on"))
		} else {
			badges = append(badges, "auto: "+th.faint.Render("off"))
		}
	}
	badges = append(badges, th.accent.Render("?")+" "+th.faint.Render("help"))
	right := strings.Join(badges, "   ") + " "

	host := safetext.Clean(string(m.host))
	head := " " + th.bold.Render("pfwd") + "  "
	// A long host name is cut before any badge goes.
	room := w - ansi.StringWidth(head) - 1 - ansi.StringWidth(state) - 1 - ansi.StringWidth(right)
	if room < ansi.StringWidth(host) {
		host = ansi.Truncate(host, max(1, room), g.ellipsis)
	}
	left := head + host + " " + state
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// viewLine is the rule above the status line. It names what is not on
// screen: the filter and the ignored ports hidden.
func (m Model) viewLine(w int) string {
	th, g := m.th, m.th.g
	var parts []string
	filter := safetext.Clean(m.filter)
	switch {
	case m.logOpen:
	case m.filtering:
		parts = append(parts, th.faint.Render("/ "+filter)+th.accent.Render(g.block))
	case m.filter != "":
		parts = append(parts, th.faint.Render(fmt.Sprintf("/ %s: %d of %d %s", filter, len(m.rows), m.total, plural(m.total, "port", "ports"))))
	}
	if m.hidden > 0 && !m.logOpen {
		parts = append(parts, th.faint.Render(fmt.Sprintf("%d ignored hidden, h shows", m.hidden)))
	}
	if len(parts) == 0 {
		return m.rule(w)
	}
	sep := th.faint.Render(" " + g.rule + " ")
	line := th.faint.Render(g.rule+" ") + strings.Join(parts, sep) + " "
	if n := w - ansi.StringWidth(line); n > 0 {
		line += th.faint.Render(strings.Repeat(g.rule, n))
	}
	return line
}

func (m Model) statusLine() string {
	s := m.status
	if s.text == "" || m.now().Sub(s.at) >= statusFor {
		return ""
	}
	return " " + m.kindGlyph(s.kind) + safetext.Clean(s.text)
}

// kindGlyph is the glyph and space in front of a status or log message.
func (m Model) kindGlyph(k statusKind) string {
	switch k {
	case statusOK:
		return m.th.ok.Render(m.th.g.ok) + " "
	case statusErr:
		return m.th.bad.Render(m.th.g.failed) + " "
	}
	return ""
}

// hintItem is a key hint; drop orders which go first when the line is too
// narrow (lowest first; 0 never drops).
type hintItem struct {
	key, desc string
	drop      int
}

// hintItems are the hints for what is on screen, in display order.
func (m Model) hintItems() []hintItem {
	help, quit := hintItem{"?", "help", 0}, hintItem{"q", "quit", 0}
	switch {
	case m.session != engine.Connected && !m.logOpen:
		return []hintItem{quit}
	case m.filtering:
		return []hintItem{{"enter", "keep", 0}, {"esc", "clear", 0}, {m.th.g.upDown, "move", 1}}
	case m.logOpen:
		return []hintItem{{m.th.g.upDown, "scroll", 0}, {"g G", "first, last", 1}, {"L", "close", 0}}
	}
	auto, filter, log := hintItem{"a", "auto", 2}, hintItem{"/", "filter", 4}, hintItem{"L", "log", 1}
	r, ok := m.current()
	switch {
	case !ok:
		return []hintItem{auto, log, help, quit}
	case r.fwd != nil:
		return []hintItem{{"enter", "stop", 0}, {"o", "open", 6}, {"y", "copy", 5}, auto, filter, log, help, quit}
	default:
		return []hintItem{{"enter", "forward", 0}, {"F", "other port", 3}, auto, filter, log, help, quit}
	}
}

// hints renders the key hints for width w, dropping the least important
// ones until they fit.
func (m Model) hints(w int) string {
	items := m.hintItems()
	render := func() string {
		parts := make([]string, len(items))
		for i, it := range items {
			parts[i] = m.th.accent.Render(it.key) + " " + m.th.faint.Render(it.desc)
		}
		return " " + strings.Join(parts, "  ")
	}
	line := render()
	for ansi.StringWidth(line) > w {
		lowest := -1
		for i, it := range items {
			if it.drop > 0 && (lowest < 0 || it.drop < items[lowest].drop) {
				lowest = i
			}
		}
		if lowest < 0 {
			break
		}
		items = append(items[:lowest:lowest], items[lowest+1:]...)
		line = render()
	}
	return line
}

// body is the area between the top rule and the view line, exactly h
// lines.
func (m Model) body(w, h int) []string {
	var lines []string
	host := safetext.Clean(string(m.host))
	dots := m.th.g.dots
	switch {
	case m.session == engine.SessionFailed:
		title := "Can't connect to " + host
		if m.wasConnected {
			title = "Lost the connection to " + host
		}
		lines = append(lines, "", "  "+m.th.bad.Render(m.th.g.failed+" "+title), "")
		if m.sessErr != nil {
			for _, l := range strings.Split(ansi.Wordwrap(safetext.Clean(m.sessErr.Error()), w-4, ""), "\n") {
				lines = append(lines, "  "+l)
			}
			lines = append(lines, "")
		}
		lines = append(lines, "  Press q to quit.")
	case m.session != engine.Connected:
		lines = append(lines, "", "  Connecting to "+host+dots)
	case len(m.rows) > 0:
		return m.table(w, h)
	case !m.scanned:
		lines = append(lines, "", "  Connected. Looking for listening ports on "+host+dots)
	case m.strategy == detect.None:
		lines = append(lines, "")
		for _, l := range m.unavailable() {
			for _, wl := range strings.Split(ansi.Wordwrap(l, w-4, ""), "\n") {
				lines = append(lines, "  "+wl)
			}
		}
	case m.filter != "" && m.total > 0:
		lines = append(lines, "", "  No ports match /"+safetext.Clean(m.filter)+". Press esc to clear the filter.")
	case m.hidden > 0:
		lines = append(lines, "", fmt.Sprintf("  No ports to show: %d %s hidden by the ignore list. Press h to show %s.",
			m.hidden, plural(m.hidden, "is", "are"), plural(m.hidden, "it", "them")))
	default:
		lines = append(lines, "", "  No listening ports yet. Start a server on "+host+" and it will appear here.")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

func (m Model) unavailable() []string {
	host := safetext.Clean(string(m.host))
	switch {
	case m.detectErr != nil:
		return []string{"Can't list ports on " + host + ": " + safetext.Clean(m.detectErr.Error())}
	case m.remoteOS != "" && m.remoteOS != "Linux":
		return []string{
			"Can't list ports on " + host + " yet: it runs " + safetext.Clean(m.remoteOS) + ", and pfwd reads Linux servers today.",
			"macOS, BSD and Windows servers come in v0.4.0.",
		}
	default:
		return []string{"Can't list ports on " + host + ": it has neither ss nor a readable /proc/net/tcp."}
	}
}

// columns is the table layout for a width; 0 means the column is not
// shown.
type columns struct {
	state, remote, local, process, label, conn, traffic int
}

// minProcess is the narrowest PROCESS column before optional columns go.
const minProcess = 10

// columns lays out the table. PROCESS takes what is left, up to 40
// cells; when it would get fewer than minProcess, TRAFFIC goes, then
// CONN, then LABEL.
func (m Model) columns(w int) columns {
	c := columns{state: 8, remote: 8, local: 16, label: 13, conn: 5, traffic: 15}
	for _, r := range m.rows {
		c.remote = max(c.remote, ansi.StringWidth(remoteText(r))+1)
	}
	c.remote = min(c.remote, 24)
	if w >= 100 {
		c.label = 18
	}
	fixed := 2 + c.state + c.remote + c.local
	for {
		rest := w - fixed - c.label - c.conn - c.traffic
		switch {
		case rest >= minProcess:
			c.process = min(40, rest)
			return c
		case c.traffic > 0:
			c.traffic = 0
		case c.conn > 0:
			c.conn = 0
		case c.label > 0:
			c.label = 0
		default:
			c.process = max(0, rest)
			return c
		}
	}
}

func (m Model) table(w, h int) []string {
	th := m.th
	c := m.columns(w)
	header := "  " + th.cell("STATE", c.state) + th.cell("REMOTE", c.remote) + th.cell("LOCAL", c.local) + th.cell("PROCESS", c.process) +
		th.cell("LABEL", c.label) + th.cell("CONN", c.conn)
	if c.traffic > 0 {
		header += "TRAFFIC"
	}
	lines := []string{th.faint.Render(header)}

	sep := m.separatorAt()
	var body []string
	for i, r := range m.rows {
		if i == sep {
			body = append(body, m.separator(w))
		}
		line := m.rowLine(r, i == m.cursor, c)
		if i == m.cursor {
			line = th.selected(line, w)
		}
		body = append(body, line)
	}

	end := min(len(body), m.offset+h-1)
	lines = append(lines, body[min(m.offset, end):end]...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

func (m Model) separator(w int) string {
	label := " detected, not forwarded "
	n := max(0, w-4-ansi.StringWidth(label))
	rule := m.th.g.rule
	return "  " + m.th.faint.Render(strings.Repeat(rule, n/2)+label+strings.Repeat(rule, n-n/2))
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
	th, g := m.th, m.th.g
	cursor := "  "
	if selected {
		cursor = th.accent.Render(g.cursor) + " "
	}
	dash := th.faint.Render(g.none)

	if r.fwd == nil && r.port.Ignored {
		// Shown only while h is on, and quiet: never auto-forwarded.
		line := th.cell("ignored", c.state) + th.cell(remoteText(r), c.remote) + th.cell(g.none, c.local) +
			th.cell(m.processName(*r.port, "?"), c.process) + th.cell(safetext.Clean(rowLabel(r)), c.label) + th.cell(g.none, c.conn)
		if c.traffic > 0 {
			line += g.none
		}
		return cursor + th.faint.Render(line)
	}

	var state, local, process, conn, traffic string
	if f := r.fwd; f != nil {
		switch f.State {
		case engine.Active:
			state = th.ok.Render(g.active) + " fwd"
			local = th.bold.Render(localAddr(f.Spec))
		case engine.Failed:
			state = th.bad.Render(g.failed) + " fail"
			local = localAddr(f.Spec)
		case engine.Paused:
			state = th.warn.Render(g.paused) + " pause"
			local = localAddr(f.Spec)
		default:
			state = g.starting + " start"
			local = localAddr(f.Spec)
		}
		switch {
		case f.State == engine.Failed && f.Err != nil:
			process = th.faint.Render(safetext.Clean(f.Err.Error()))
		case r.port == nil:
			process = th.faint.Render("not listening")
		default:
			process = m.processName(*r.port, th.faint.Render("?"))
		}
		conn = itoa(int(f.Stats.Conns))
		traffic = dash
		if f.State == engine.Active {
			traffic = m.trafficText(f.ID)
		}
	} else {
		state, local, conn, traffic = dash, dash, dash, dash
		process = m.processName(*r.port, th.faint.Render("?"))
	}

	line := cursor + th.cell(state, c.state) + th.cell(remoteText(r), c.remote) + th.cell(local, c.local) + th.cell(process, c.process) +
		m.labelCell(r, c.label) + th.cell(conn, c.conn)
	if c.traffic > 0 {
		line += traffic
	}
	return line
}

// labelCell is the LABEL column: the label, then the scheme once the probe
// found one. A long label is cut before the scheme, so the scheme shows.
func (m Model) labelCell(r row, w int) string {
	th := m.th
	label := safetext.Clean(rowLabel(r))
	var scheme string
	if r.fwd != nil && (r.fwd.Scheme == forward.SchemeHTTP || r.fwd.Scheme == forward.SchemeHTTPS) {
		scheme = r.fwd.Scheme.String()
	}
	if scheme == "" || w == 0 {
		return th.cell(label, w)
	}
	if room := w - 2 - len(scheme); label != "" && room > 1 {
		return th.cell(ansi.Truncate(label, room, th.g.ellipsis)+" "+th.faint.Render(scheme), w)
	}
	return th.cell(th.faint.Render(scheme), w)
}

// processName is the cleaned process name, or unknown when the server
// hides it.
func (m Model) processName(p engine.Port, unknown string) string {
	if p.Process == "" {
		return unknown
	}
	return safetext.Clean(p.Process)
}

func (m Model) trafficText(id string) string {
	g := m.th.g
	r := m.rates[id]
	if r.in == 0 && r.out == 0 {
		return g.down + "0 " + g.up + "0"
	}
	return g.down + formatRate(r.in) + "/s " + g.up + formatRate(r.out) + "/s"
}
