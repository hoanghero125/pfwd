package tui

import (
	"errors"
	"fmt"
	"math"
	"net"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	lines = append(lines, m.hostBar(w), "")
	if m.logOpen {
		lines = append(lines, m.logPane(w, h-5)...)
	} else if m.reverseOpen {
		lines = append(lines, m.reversePane(w, h-5)...)
	} else {
		lines = append(lines, m.body(w, h-5)...)
	}
	lines = append(lines, m.viewLine(), m.statusLine(), m.hints(w))
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
	right := strings.Join(badges, "   ") + "  "

	host := safetext.Clean(string(m.host))
	head := "  " + th.faint.Render("pfwd") + "  "
	if m.all {
		head += th.bold.Render("all hosts")
		state = ""
	}
	current := host + " " + state
	if m.all {
		current = ""
	} else {
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

// viewLine is the line above the status line. It names what is not on
// screen, the filter and the ignored ports hidden, and is blank when every
// row shows.
func (m Model) viewLine() string {
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
		return ""
	}
	return "  " + strings.Join(parts, "   ")
}

func (m Model) statusLine() string {
	s := m.status
	if s.text == "" || m.now().Sub(s.at) >= statusFor {
		return ""
	}
	return "  " + m.kindGlyph(s.kind) + safetext.Clean(s.text)
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
	if m.opts.Detach {
		quit = hintItem{"q", "detach", 0}
	}
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
		return "  " + strings.Join(parts, "  ")
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

// columns is the table layout for a width. A row reads from the server to
// this computer: the remote port and its process, the tunnel, then the
// local address. host is 0 unless every host shows.
type columns struct {
	host, remote, process, tunnel, local int
}

// The tunnel takes what the other columns leave, between minTunnel and
// maxTunnel cells; process gets its widest name, between minProcess and
// maxProcess. Room the tunnel can't use stays at the end of the line.
const (
	minTunnel  = 7
	maxTunnel  = 48
	minProcess = 8
	maxProcess = 32
)

// columns lays out the table for width w.
func (m Model) columns(w int) columns {
	c := columns{remote: 8, process: 10, local: 16}
	for _, r := range m.rows {
		c.remote = max(c.remote, ansi.StringWidth(remoteText(r))+2)
		c.process = max(c.process, ansi.StringWidth(m.processText(r))+2)
		if r.fwd != nil {
			c.local = max(c.local, ansi.StringWidth(m.localText(*r.fwd))+1)
		}
	}
	c.remote, c.process, c.local = min(c.remote, 24), min(c.process, maxProcess), min(c.local, 32)
	if m.all {
		for _, h := range m.hosts {
			c.host = max(c.host, ansi.StringWidth(safetext.Clean(string(h)))+2)
		}
		c.host = min(max(c.host, 6), 16)
	}
	// 2 for the cursor, 1 between the tunnel and the local address.
	rest := w - 2 - c.host - c.remote - c.local - 1
	if rest-c.process < minTunnel {
		c.process = max(minProcess, rest-minTunnel)
	}
	c.tunnel = min(maxTunnel, max(3, rest-c.process))
	return c
}

func (m Model) table(w, h int) []string {
	th := m.th
	c := m.columns(w)
	from := "remote"
	if !m.all {
		from = safetext.Clean(string(m.host))
	}
	header := "  " + th.cell("host", c.host) + th.cell(from, c.remote+c.process+c.tunnel+1) + "this computer"
	lines := []string{th.faint.Render(header)}

	sep := m.separatorAt()
	var body []string
	for i, r := range m.rows {
		if i == sep {
			body = append(body, "", m.separator())
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

// separator titles the detected ports under the forwards.
func (m Model) separator() string {
	text := "not forwarded"
	if !m.all {
		text = "on " + safetext.Clean(string(m.host)) + ", not forwarded"
	}
	return "  " + m.th.faint.Render(text)
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
	start := th.cell(safetext.Clean(string(r.host)), c.host) + th.cell(remoteText(r), c.remote)
	switch {
	case r.fwd == nil && r.port.Ignored:
		// Shown only while h is on, and quiet: never auto-forwarded.
		return cursor + th.faint.Render(start+th.cell(ansi.Strip(m.processText(r)), c.process)+"ignored")
	case r.fwd == nil:
		// A detected port: the start of a tunnel, for enter to finish.
		stub := g.from + strings.Repeat(g.gap, 3)
		if selected {
			stub = th.accent.Render(stub)
		} else {
			stub = th.faint.Render(stub)
		}
		return cursor + th.bold.Render(start) + th.cell(m.processText(r), c.process) + stub
	}
	return cursor + th.bold.Render(start) + th.cell(m.processText(r), c.process) + m.tunnel(*r.fwd, c.tunnel) + " " + m.localText(*r.fwd)
}

// processText is what listens on the server's side of a row: its label,
// or else the process.
func (m Model) processText(r row) string {
	th := m.th
	var proc string
	switch {
	case r.fwd != nil && r.fwd.Spec.Kind == engine.Remote:
		proc = "reverse"
	case r.fwd != nil && r.fwd.Spec.Kind == engine.Dynamic:
		proc = "proxy"
	case r.port == nil:
		// The server doesn't list the port (or isn't connected): the
		// tunnel says why.
	case r.port.Process == "":
		proc = "?"
	default:
		proc = safetext.Clean(r.port.Process)
	}
	switch label := safetext.Clean(rowLabel(r)); {
	case label != "":
		return label
	case proc == "":
		return th.faint.Render(th.g.none)
	case r.port != nil && r.port.Process != "":
		return proc
	}
	return th.faint.Render(proc)
}

// localText is a forward's address on this computer, then its tags: the
// scheme once the probe found one, and "pin" or "cfg" when it is
// remembered across runs.
func (m Model) localText(f engine.Forward) string {
	addr := localAddr(f.Spec)
	if f.State == engine.Active {
		addr = m.th.bold.Render(addr)
	}
	var tags []string
	if f.Scheme == forward.SchemeHTTP || f.Scheme == forward.SchemeHTTPS {
		tags = append(tags, f.Scheme.String())
	}
	switch {
	case f.Spec.Config:
		tags = append(tags, "cfg")
	case f.Spec.Pinned:
		tags = append(tags, "pin")
	}
	if len(tags) == 0 {
		return addr
	}
	return addr + " " + m.th.faint.Render(strings.Join(tags, " "))
}

// packetEvery is the distance between a tunnel's packets, in cells.
const packetEvery = 4

// tunnel draws forward f as a line w cells wide, from the server's side to
// this computer's: an open end where the forwarded port listens, a solid
// one where it arrives (a reverse forward runs the other way), and its
// state on the line. While traffic flows, packets move along it a cell a
// second. Working tunnels are solid lines, the others broken ones, so the
// state shows without color.
func (m Model) tunnel(f engine.Forward, w int) string {
	th, g := m.th, m.th.g
	if w < 3 {
		return strings.Repeat(" ", max(0, w))
	}
	from, to := g.from, g.to
	if f.Spec.Kind == engine.Remote {
		from, to = to, from
	}
	r := m.rates[f.ID]
	busy := f.State == engine.Active && r.in+r.out > 0
	line, ends := th.faint, th.faint
	wire := g.gap
	var labels []tunnelLabel
	switch f.State {
	case engine.Active:
		line, ends, wire = th.faint, th.ok, g.wire
		if busy {
			line = th.ok
		}
		labels = m.rateLabels(f, r)
	case engine.Paused:
		line, ends = th.warn, th.warn
		labels = []tunnelLabel{{text: "paused", style: th.warn}}
		if f.Pause != engine.NotPaused {
			labels = append([]tunnelLabel{{text: "paused, " + f.Pause.String(), style: th.warn}}, labels...)
		}
	case engine.Failed:
		line, ends = th.bad, th.bad
		labels = []tunnelLabel{{text: "failed", style: th.bad}}
		if f.Err != nil {
			labels = append([]tunnelLabel{{text: "failed: " + safetext.Clean(f.Err.Error()), style: th.bad, cut: true}}, labels...)
		}
	default:
		labels = []tunnelLabel{{text: "starting" + g.dots, style: th.faint}}
	}

	// A label keeps three cells of line on each side, so the tunnel still
	// reads as one; the last, shortest one makes do with one.
	inner := w - 2
	var label tunnelLabel
	for i, l := range labels {
		room := inner - 2 - 6
		if i == len(labels)-1 {
			room = inner - 2 - 2
		}
		if ansi.StringWidth(l.text) <= room {
			label = l
			break
		}
		// An error is cut to fit rather than dropped, while a useful part
		// of it shows.
		if l.cut && room >= 16 {
			label = l
			label.text = ansi.Truncate(l.text, room, g.ellipsis)
			break
		}
	}
	lw := 0
	if label.text != "" {
		lw = ansi.StringWidth(label.text) + 2
	}
	left := (inner - lw) / 2
	// wires draws cells [a, b) of the line, with the packets on it.
	wires := func(a, b int) string {
		var s strings.Builder
		for i := a; i < b; i++ {
			pos := i - r.moved
			if f.Spec.Kind == engine.Remote {
				pos = i + r.moved
			}
			if busy && ((pos%packetEvery)+packetEvery)%packetEvery == 0 {
				s.WriteString(g.packet)
			} else {
				s.WriteString(wire)
			}
		}
		return line.Render(s.String())
	}
	out := ends.Render(from) + wires(0, left)
	if lw > 0 {
		out += " " + label.styled() + " "
	}
	return out + wires(left+lw, inner) + ends.Render(to)
}

// tunnelLabel is the text on a tunnel; cut lets it be shortened to fit.
type tunnelLabel struct {
	text  string
	style lipgloss.Style
	// parts, when set, style the text in pieces; text is their plain sum.
	parts []string
	cut   bool
}

func (l tunnelLabel) styled() string {
	if len(l.parts) == 0 {
		return l.style.Render(l.text)
	}
	return strings.Join(l.parts, "")
}

// rateLabels are an active tunnel's labels, longest first: the rates and
// the open connections, the rates, the download rate. Idle traffic says
// so.
func (m Model) rateLabels(f engine.Forward, r rate) []tunnelLabel {
	th, g := m.th, m.th.g
	if r.in == 0 && r.out == 0 {
		return []tunnelLabel{{text: "idle", style: th.faint}}
	}
	down := g.down + " " + formatRate(r.in) + "/s"
	up := g.up + " " + formatRate(r.out) + "/s"
	labels := []tunnelLabel{
		{text: down + "  " + up, parts: []string{down, "  ", th.faint.Render(up)}},
		{text: down, parts: []string{down}},
	}
	if n := int(f.Stats.Conns); n > 0 {
		conns := itoa(n) + " " + plural(n, "conn", "conns")
		labels = append([]tunnelLabel{{text: down + "  " + up + "  " + conns, parts: []string{down, "  ", th.faint.Render(up + "  " + conns)}}}, labels...)
	}
	return labels
}
