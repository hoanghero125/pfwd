package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// glyphs are the symbols the TUI draws, in Unicode or ASCII.
type glyphs struct {
	active, paused, failed, starting, connecting, closed string
	cursor, ok, none, rule, ellipsis, down, up           string
	topLeft, topRight, bottomLeft, bottomRight, side     string
	upDown, dots, block, mask                            string
}

var unicodeGlyphs = glyphs{
	active: "●", paused: "◌", failed: "✗", starting: "…", connecting: "◐", closed: "○",
	cursor: "▶", ok: "✓", none: "—", rule: "─", ellipsis: "…", down: "↓", up: "↑",
	topLeft: "╭", topRight: "╮", bottomLeft: "╰", bottomRight: "╯", side: "│",
	upDown: "↑ ↓", dots: "…", block: "█", mask: "•",
}

// asciiGlyphs are for consoles whose fonts lack the Unicode ones, such as
// the legacy Windows console.
var asciiGlyphs = glyphs{
	active: "*", paused: "o", failed: "x", starting: ".", connecting: "~", closed: "-",
	cursor: ">", ok: "+", none: "-", rule: "-", ellipsis: "~", down: "v", up: "^",
	topLeft: "+", topRight: "+", bottomLeft: "+", bottomRight: "+", side: "|",
	upDown: "up down", dots: "...", block: "_", mask: "*",
}

// theme holds the TUI's styles and glyphs. Colors are the terminal's own
// ANSI palette, so pfwd follows the user's theme; only the accent and the
// selection depend on whether the background is dark.
type theme struct {
	faint, bold, accent, ok, warn, bad lipgloss.Style
	// selectBg starts the selected row's background.
	selectBg string
	g        glyphs
}

func newTheme(dark, ascii bool) theme {
	s := lipgloss.NewStyle()
	th := theme{
		faint:    s.Faint(true),
		bold:     s.Bold(true),
		accent:   s.Foreground(lipgloss.Color("6")),
		ok:       s.Foreground(lipgloss.Color("2")),
		warn:     s.Foreground(lipgloss.Color("3")),
		bad:      s.Foreground(lipgloss.Color("1")),
		selectBg: "\x1b[48;5;236m",
		g:        unicodeGlyphs,
	}
	if !dark {
		// Cyan is hard to read on white in the stock palettes.
		th.accent = s.Foreground(lipgloss.Color("4"))
		th.selectBg = "\x1b[48;5;254m"
	}
	if ascii {
		th.g = asciiGlyphs
	}
	return th
}

// cell fits s into exactly w cells, keeping the last one blank so columns
// never touch; a cut value ends with an ellipsis.
func (th theme) cell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return pad(ansi.Truncate(s, w-1, th.g.ellipsis), w)
}

// selected paints line, w cells wide, with the selection background. The
// background is set again after every reset inside the line.
func (th theme) selected(line string, w int) string {
	line = pad(line, w)
	return th.selectBg + strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+th.selectBg) + "\x1b[m"
}

// formatRate formats bytes per second with binary units: 512, 1.0K, 12K,
// 1.5M.
func formatRate(b float64) string {
	if b < 1024 {
		return strconv.Itoa(int(b))
	}
	units := []string{"K", "M", "G", "T"}
	i := -1
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if b < 10 {
		return fmt.Sprintf("%.1f%s", b, units[i])
	}
	return fmt.Sprintf("%.0f%s", b, units[i])
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// plural picks one or many by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
