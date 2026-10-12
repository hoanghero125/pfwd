package tui

import (
	"errors"
	"fmt"
	"math"
	"net"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
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
	if m.all {
		v.WindowTitle = "pfwd: all hosts"
	}
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
	} else if m.reverseOpen {
		lines = append(lines, m.reversePane(w, h-5)...)
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
	case engine.Reconnecting:
		state = th.warn.Render(g.connecting) + " " + m.reconnectText()
	case engine.SessionFailed:
		word := "failed"
		if m.wasConnected {
			word = "disconnected"
		}
		state = th.bad.Render(g.failed) + " " + word
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
	if m.all {
		head += th.bold.Render("all hosts")
		state = ""
	}
	current := host + " " + state
	if m.all {
		current = ""
	} else if len(m.hosts) > 1 {
		host = th.bold.Render(host)
	}
	// A long host name is cut before any badge goes.
	room := w - ansi.StringWidth(head) - 1 - ansi.StringWidth(state) - 1 - ansi.StringWidth(right)
	if !m.all && room < ansi.StringWidth(host) {
		host = ansi.Truncate(host, max(1, room), g.ellipsis)
	}
	if !m.all {
		current = host + " " + state
	}
	left := head + current
	// The other hosts follow as far as they fit, each with its state glyph.
	for _, h := range m.hosts {
		if h == m.host && !m.all {
			continue
		}
		tab := th.faint.Render(safetext.Clean(string(h))) + " " + m.tabGlyph(m.views[h])
		if strings.TrimSpace(ansi.Strip(left)) != "pfwd" {
			tab = "  " + tab
		}
		if ansi.StringWidth(left)+ansi.StringWidth(tab)+1+ansi.StringWidth(right) > w {
			break
		}
		left += tab
	}
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// tabGlyph is a host tab's state glyph.
func (m Model) tabGlyph(v *hostView) string {
	th, g := m.th, m.th.g
	switch v.session {
	case engine.Connected:
		return th.ok.Render(g.active)
	case engine.SessionFailed:
		return th.bad.Render(g.failed)
	case engine.Closed:
		return th.faint.Render(g.closed)
	}
	return th.warn.Render(g.connecting)
}

// reconnectText counts down to the next reconnect attempt, in whole
// seconds, or says one is under way.
func (m Model) reconnectText() string {
	if left := m.retryAt.Sub(m.now()); !m.retryAt.IsZero() && left > 0 {
		return fmt.Sprintf("reconnecting in %ds", int(math.Ceil(left.Seconds())))
	}
	return "reconnecting" + m.th.g.dots
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
	auto, filter, log := hintItem{"a", "auto", 2}, hintItem{"/", "filter", 4}, hintItem{"L", "log", 1}
	switch {
	case m.filtering:
		return []hintItem{{"enter", "keep", 0}, {"esc", "clear", 0}, {m.th.g.upDown, "move", 1}}
	case m.logOpen:
		return []hintItem{{m.th.g.upDown, "scroll", 0}, {"g G", "first, last", 1}, {"L", "close", 0}}
	case m.reverseOpen:
		return []hintItem{{"enter", "expose", 0}, {"F", "other server port", 2}, {m.th.g.upDown, "move", 1}, {"esc", "back", 0}}
	case m.session == engine.Reconnecting:
		return []hintItem{{"c", "reconnect now", 0}, {"x", "stop retrying", 3}, log, help, quit}
	case m.session == engine.SessionFailed:
		return []hintItem{{"c", "reconnect", 0}, log, quit}
	case m.session != engine.Connected:
		return []hintItem{quit}
	}
	r, ok := m.current()
	switch {
	case !ok:
		return []hintItem{auto, log, help, quit}
	case r.fwd != nil:
		pin := hintItem{"p", "pin", 3}
		if r.fwd.Spec.Pinned {
			pin.desc = "unpin"
		}
		return []hintItem{{"enter", "stop", 0}, {"o", "open", 6}, {"y", "copy", 5}, pin, auto, filter, log, help, quit}
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
	// After a loss the table stays, unless the server refused the
	// reconnect: a changed host key or a rejected login needs reading.
	showTable := len(m.rows) > 0 && (m.session == engine.Connected || m.session == engine.Reconnecting ||
		(m.session == engine.SessionFailed && m.wasConnected && !refused(m.sessErr)))
	switch {
	case showTable:
		return m.table(w, h)
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
		lines = append(lines, "  Press c to try again, or q to quit.")
	case m.session == engine.Reconnecting:
		lines = append(lines, "", "  Reconnecting to "+host+dots)
	case m.session != engine.Connected:
		lines = append(lines, "", "  Connecting to "+host+dots)
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

// refused reports whether a connection failed because the server, or its
// host key, refused it, rather than the network.
func refused(err error) bool {
	return errors.Is(err, sshx.ErrHostKey) || errors.Is(err, sshx.ErrAuth) || errors.Is(err, sshx.ErrUnsupported)
}

func (m Model) unavailable() []string {
	host := safetext.Clean(string(m.host))
	switch {
	case m.detectErr != nil:
		return []string{"Can't list ports on " + host + ": " + safetext.Clean(m.detectErr.Error())}
	default:
		return []string{"Can't list ports on " + host + ": it has none of ss, /proc/net/tcp, netstat, lsof or sockstat (or PowerShell, on Windows). You can still forward ports by hand."}
	}
}

// columns is the table layout for a width; 0 means the column is not
// shown.
type columns struct {
	host, state, remote, local, process, label, conn, traffic int
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
	if m.all {
		for _, h := range m.hosts {
			c.host = max(c.host, ansi.StringWidth(safetext.Clean(string(h)))+2)
		}
		c.host = min(max(c.host, 6), 16)
	}
	fixed := 2 + c.host + c.state + c.remote + c.local
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
	header := "  " + th.cell("HOST", c.host) + th.cell("STATE", c.state) + th.cell("REMOTE", c.remote) + th.cell("LOCAL", c.local) + th.cell("PROCESS", c.process) +
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
	switch {
	case r.fwd != nil && r.fwd.Spec.Kind == engine.Dynamic:
		return "any"
	case r.fwd != nil && r.fwd.Spec.Kind == engine.Remote:
		host, port = r.fwd.Spec.BindAddr, r.fwd.Spec.RemotePort
	case r.fwd != nil:
		host, port = r.fwd.Spec.RemoteHost, r.fwd.Spec.RemotePort
	default:
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
	cursor += th.cell(safetext.Clean(string(r.host)), c.host)

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
		word := "fwd"
		switch f.Spec.Kind {
		case engine.Remote:
			word = "rev"
		case engine.Dynamic:
			word = "socks"
		}
		switch f.State {
		case engine.Active:
			state = th.ok.Render(g.active) + " " + word
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
		case f.State == engine.Paused && f.Pause != engine.NotPaused:
			process = th.faint.Render(f.Pause.String())
		case f.Spec.Kind == engine.Remote:
			process = th.faint.Render("this machine")
		case f.Spec.Kind == engine.Dynamic:
			process = th.faint.Render("SOCKS proxy")
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

// labelCell is the LABEL column: the label, then tags: the scheme once the
// probe found one, and "pin" or "cfg" for forwards remembered across runs.
// When they don't all fit, the scheme goes first, then the label is cut,
// so the tags show.
func (m Model) labelCell(r row, w int) string {
	th := m.th
	label := safetext.Clean(rowLabel(r))
	var scheme, keep string
	if f := r.fwd; f != nil {
		if f.Scheme == forward.SchemeHTTP || f.Scheme == forward.SchemeHTTPS {
			scheme = f.Scheme.String()
		}
		switch {
		case f.Spec.Config:
			keep = "cfg"
		case f.Spec.Pinned:
			keep = "pin"
		}
	}
	suffix := strings.TrimSpace(scheme + " " + keep)
	if keep != "" && scheme != "" && ansi.StringWidth(label)+1+len(suffix) > w-1 {
		suffix = keep
	}
	if suffix == "" || w == 0 {
		return th.cell(label, w)
	}
	if room := w - 2 - len(suffix); label != "" && room > 1 {
		return th.cell(ansi.Truncate(label, room, th.g.ellipsis)+" "+th.faint.Render(suffix), w)
	}
	return th.cell(th.faint.Render(suffix), w)
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
