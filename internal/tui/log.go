package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/hoanghero125/pfwd/internal/safetext"
)

// logIndent lines wrapped log messages up under the message column:
// two spaces, the time and two more spaces.
const logIndent = 12

// logLines renders the event log for width w, oldest first, wrapping long
// messages under the message column.
func (m Model) logLines(w int) []string {
	var out []string
	for _, e := range m.log {
		out = append(out, m.entryLines(e, w)...)
	}
	return out
}

// entryLines renders one log entry for width w.
func (m Model) entryLines(e logEntry, w int) []string {
	glyph := m.kindGlyph(e.kind)
	width := max(10, w-logIndent-ansi.StringWidth(glyph))
	wrapped := strings.Split(ansi.Wordwrap(safetext.Clean(e.text), width, ""), "\n")
	out := []string{"  " + e.at.Format("15:04:05") + "  " + glyph + wrapped[0]}
	for _, l := range wrapped[1:] {
		out = append(out, strings.Repeat(" ", logIndent+ansi.StringWidth(glyph))+l)
	}
	return out
}

// logVisible is how many log lines fit under the pane's title.
func (m Model) logVisible() int {
	return max(1, m.height-6)
}

// maxLogScroll is how far the log scrolls up: to its first line.
func (m Model) maxLogScroll() int {
	return max(0, len(m.logLines(m.width))-m.logVisible())
}

// logPane replaces the table with the event log, exactly h lines.
func (m Model) logPane(w, h int) []string {
	lines := []string{m.th.faint.Render("  Event log, newest last")}
	all := m.logLines(w)
	end := len(all) - min(m.logScroll, m.maxLogScroll())
	start := max(0, end-(h-1))
	lines = append(lines, all[start:end]...)
	if len(m.log) == 0 {
		lines = append(lines, "  Nothing yet.")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}
