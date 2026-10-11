package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/engine"
)

type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalPort
	modalConflict
	modalHostKey
	modalQuit
)

// modalState is the open modal, if any, and what it needs.
type modalState struct {
	kind modalKind
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
		keys := [][2]string{
			{"↑ ↓  j k", "move"},
			{"g G", "first, last"},
			{"enter  f", "forward the port, or stop it"},
			{"F", "forward to another local port"},
			{"x", "stop the forward"},
			{"?", "close this help"},
			{"q  ctrl+c", "quit"},
		}
		for _, k := range keys {
			body = append(body, th.accent.Render(pad(k[0], 13))+k[1])
		}
	case modalHostKey:
		q := md.prompt.Question
		title = "First connection to " + string(md.prompt.Host)
		body = append(body,
			"pfwd has not seen this server's key before. Check the fingerprint with whoever runs the server before you trust it.",
			"",
			"  "+q.KeyType,
			"  "+q.Fingerprint,
			"",
			m.hint("y", "trust and save to known_hosts", "n", "don't connect"),
		)
	case modalPort:
		title = "Forward :" + itoa(int(md.spec.RemotePort)) + " to another local port"
		field := "Local port  " + md.input + th.accent.Render("█")
		errLine := ""
		if md.inputErr != "" {
			errLine = th.bad.Render(md.inputErr)
		}
		body = append(body, field, errLine, m.hint("enter", "forward", "esc", "cancel"))
	case modalConflict:
		port := itoa(int(md.busy))
		title = "Port " + port + " is busy on this machine"
		where := "Something here already listens on " + md.bind + ":" + port + "."
		if md.suggested == 0 {
			where = "Something here already listens on " + md.bind + ":" + port + ", and the next 100 ports are taken too."
			body = append(body, where, "", m.hint("F", "choose a port", "esc", "cancel"))
		} else {
			body = append(body, where, "", m.hint("enter", "use localhost:"+itoa(int(md.suggested)), "F", "other port", "esc", "cancel"))
		}
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

// box draws a rounded border around lines padded to inner cells.
func box(th theme, lines []string, inner int) []string {
	edge := th.accent
	out := make([]string, 0, len(lines)+2)
	out = append(out, edge.Render("╭"+strings.Repeat("─", inner+2)+"╮"))
	for _, l := range lines {
		out = append(out, edge.Render("│")+" "+pad(ansi.Truncate(l, inner, ""), inner)+" "+edge.Render("│"))
	}
	out = append(out, edge.Render("╰"+strings.Repeat("─", inner+2)+"╯"))
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
