package tui

import (
	"cmp"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalPort
	modalConflict
	modalHostKey
	modalQuit
	modalCredential
	modalPicker
)

// modalState is the open modal, if any, and what it needs.
type modalState struct {
	kind modalKind
	// host is the host the port and conflict modals forward on.
	host engine.HostID
	// spec is the forward being made (port and conflict modals).
	spec engine.ForwardSpec
	// input and inputErr belong to the port modal's field.
	input, inputErr string
	// busy, bind and suggested describe a port conflict.
	busy, suggested uint16
	bind            string
	prompt          engine.HostKeyPrompt
	// active is the number of active forwards (quit modal).
	active int
	// scroll is the first key the help shows, when they don't all fit.
	scroll int
	// cred and secret belong to the credential modal. secret has a fixed
	// capacity and is zeroed when the modal closes.
	cred   engine.CredentialPrompt
	secret []byte
}

// modalInner is the text width inside a modal box.
const modalInner = 52

// hint renders "key description" pairs, keys in the accent color.
func (m Model) hint(pairs ...string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, m.th.accent.Render(pairs[i])+" "+m.th.faint.Render(pairs[i+1]))
	}
	return strings.Join(parts, "   ")
}

func (m Model) modalLines() []string {
	th := m.th
	var title string
	var body []string
	switch md := m.modal; md.kind {
	case modalHelp:
		title = "Keys"
		keys := helpKeys(th)
		// The box needs a border line above and below, plus the title and
		// a blank line; on a short window the keys scroll.
		if room := m.height - 4; room < len(keys) {
			shown := max(1, room-1)
			start := min(max(0, md.scroll), len(keys)-shown)
			more := th.faint.Render(th.g.upDown + " more keys, " + itoa(start+1) + "-" + itoa(start+shown) + " of " + itoa(len(keys)))
			keys = append(keys[start:start+shown:start+shown], [2]string{"", more})
		}
		for _, k := range keys {
			if k[0] == "" {
				body = append(body, k[1])
				continue
			}
			body = append(body, th.accent.Render(pad(k[0], 13))+k[1])
		}
	case modalHostKey:
		q := md.prompt.Question
		// A jump host's key is asked about under the jump host's name.
		title = "First connection to " + safetext.Clean(cmp.Or(q.Host, string(md.prompt.Host)))
		trust := "trust for this connection"
		if q.Saves {
			trust = "trust and save to known_hosts"
		}
		body = append(body,
			"pfwd has not seen this server's key before. Check the fingerprint with whoever runs the server before you trust it.",
			"",
			"  "+safetext.Clean(q.KeyType),
			"  "+safetext.Clean(q.Fingerprint),
			"",
			m.hint("y", trust, "n", "don't connect"),
		)
	case modalPort:
		title = "Forward :" + itoa(int(md.spec.RemotePort)) + " to another local port"
		label := "Local port"
		if md.spec.Kind == engine.Remote {
			title = "Expose localhost:" + itoa(int(md.spec.LocalPort)) + " on another server port"
			label = "Server port"
		}
		field := label + "  " + md.input + th.accent.Render(th.g.block)
		errLine := ""
		if md.inputErr != "" {
			errLine = th.bad.Render(md.inputErr)
		}
		body = append(body, field, errLine, m.hint("enter", "forward", "esc", "cancel"))
	case modalConflict:
		port := itoa(int(md.busy))
		title = "Port " + port + " is busy on this machine"
		addr := md.bind + ":" + port
		where := "Something here already uses " + addr + "."
		if m.goos == "windows" {
			// Windows reserves port ranges that nothing visibly listens on.
			where = "Something here already uses " + addr + ", or Windows reserved it. Hyper-V, WSL and Docker reserve port ranges; `netsh int ipv4 show excludedportrange protocol=tcp` lists them."
		}
		if md.suggested == 0 {
			where = strings.TrimSuffix(where, ".") + ". The next 100 ports are taken too."
			body = append(body, where, "", m.hint("F", "choose a port", "esc", "cancel"))
		} else {
			body = append(body, where, "", m.hint("enter", "use localhost:"+itoa(int(md.suggested)), "F", "other port", "esc", "cancel"))
		}
	case modalCredential:
		title, body = m.credentialLines(md)
	case modalPicker:
		title, body = m.pickerLines()
	case modalQuit:
		title = "Quit pfwd?"
		active := "1 forward is active. Quitting stops it."
		if md.active != 1 {
			active = itoa(md.active) + " forwards are active. Quitting stops them."
		}
		body = append(body, active, "", m.hint("y", "quit", "n", "stay"))
	default:
		return nil
	}
	lines := []string{th.bold.Render(title), ""}
	for _, b := range body {
		if b == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.Split(ansi.Wordwrap(b, modalInner, ""), "\n")...)
	}
	return box(th, lines, modalInner)
}

// helpKeys are the help modal's keys and what they do.
func helpKeys(th theme) [][2]string {
	return [][2]string{
		{th.g.upDown + "  j k", "move"},
		{"g G", "first, last"},
		{"enter  f", "forward the port, or stop it"},
		{"F", "forward to another local port"},
		{"x", "stop the forward"},
		{"o", "open in the browser"},
		{"y", "copy the URL"},
		{"p", "pin: remember the forward next time"},
		{"a", "turn auto-forward on or off"},
		{"/", "filter by port, process or label"},
		{"h", "show or hide ignored ports"},
		{"L", "event log"},
		{"tab", "next host; shift+tab the one before"},
		{"ctrl+a", "all hosts in one table, or back"},
		{"+", "connect another host"},
		{"r", "expose a local port on the server"},
		{"d", "start or stop a SOCKS proxy (1080)"},
		{"c", "reconnect now"},
		{"x", "stop retrying, while reconnecting"},
		{"?  esc", "close this help"},
		{"q  ctrl+c", "quit"},
	}
}

// credentialLines are the title and body of the credential modal. Text
// from the server is cleaned; a secret shows as one mask glyph per
// character unless the server asked to echo it.
func (m Model) credentialLines(md modalState) (string, []string) {
	th := m.th
	q := md.cred.Question
	host := safetext.Clean(cmp.Or(q.Host, string(md.cred.Host)))
	var title string
	var body []string
	action := "log in"
	switch q.Kind {
	case sshx.Passphrase:
		title, action = "Unlock your key", "unlock"
		body = append(body, "Passphrase for "+safetext.Clean(q.Prompt))
	case sshx.Password:
		title = "Password for " + safetext.Clean(q.Prompt) + "@" + host
	default:
		title = host + " asks"
		if q.Instruction != "" {
			body = append(body, safetext.Clean(q.Instruction), "")
		}
		body = append(body, safetext.Clean(q.Prompt))
	}
	typed := strings.Repeat(th.g.mask, utf8.RuneCount(md.secret))
	if q.Echo {
		typed = safetext.Clean(string(md.secret))
	}
	if q.Retry {
		body = append(body, th.bad.Render("That didn't work. Try again."))
	}
	body = append(body, "  "+typed+th.accent.Render(th.g.block), "", m.hint("enter", action, "esc", "cancel"))
	return title, body
}

// box draws a border around lines padded to inner cells.
func box(th theme, lines []string, inner int) []string {
	edge, g := th.accent, th.g
	out := make([]string, 0, len(lines)+2)
	out = append(out, edge.Render(g.topLeft+strings.Repeat(g.rule, inner+2)+g.topRight))
	for _, l := range lines {
		out = append(out, edge.Render(g.side)+" "+pad(ansi.Truncate(l, inner, ""), inner)+" "+edge.Render(g.side))
	}
	out = append(out, edge.Render(g.bottomLeft+strings.Repeat(g.rule, inner+2)+g.bottomRight))
	return out
}

// overlay draws the box lines centered over the base lines.
func overlay(base, boxLines []string, width int) []string {
	if len(boxLines) == 0 {
		return base
	}
	bw := ansi.StringWidth(boxLines[0])
	x := max(0, (width-bw)/2)
	y := max(0, (len(base)-len(boxLines))/2)
	out := append([]string(nil), base...)
	for i, bl := range boxLines {
		if y+i >= len(out) {
			break
		}
		line := out[y+i]
		left := pad(ansi.Truncate(line, x, ""), x)
		right := ansi.TruncateLeft(line, x+bw, "")
		out[y+i] = left + "\x1b[m" + bl + right
	}
	return out
}
