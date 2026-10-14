package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tokens "github.com/hoanghero125/pfwd/internal/theme"
)

// glyphs are the symbols the TUI draws, in Unicode or ASCII.
type glyphs struct {
	active, paused, failed, starting, connecting, closed string
	cursor, ok, none, rule, ellipsis, down, up           string
	topLeft, topRight, bottomLeft, bottomRight, side     string
	upDown, dots, block, mask                            string
}

var unicodeGlyphs = glyphs{
	active: tokens.Active().Glyph, paused: tokens.Paused().Glyph, failed: tokens.Failed().Glyph,
	starting: tokens.Starting().Glyph, connecting: tokens.Connecting().Glyph, closed: tokens.Closed().Glyph,
	cursor: "▶", ok: "✓", none: "—", rule: "─", ellipsis: "…", down: "↓", up: "↑",
	topLeft: "╭", topRight: "╮", bottomLeft: "╰", bottomRight: "╯", side: "│",
	upDown: "↑ ↓", dots: "…", block: "█", mask: "•",
}

// asciiGlyphs are for consoles whose fonts lack the Unicode ones, such as
// the legacy Windows console.
var asciiGlyphs = glyphs{
	active: tokens.Active().ASCII, paused: tokens.Paused().ASCII, failed: tokens.Failed().ASCII,
	starting: tokens.Starting().ASCII, connecting: tokens.Connecting().ASCII, closed: tokens.Closed().ASCII,
	cursor: ">", ok: "+", none: "-", rule: "-", ellipsis: "~", down: "v", up: "^",
	topLeft: "+", topRight: "+", bottomLeft: "+", bottomRight: "+", side: "|",
	upDown: "up down", dots: "...", block: "_", mask: "*",
}

// theme holds the TUI's styles and glyphs. Colors are the terminal's own
// ANSI palette, so pfwd follows the user's theme; only the accent and the
// selection depend on whether the background is dark. The state glyphs and
// colors come from internal/theme, which the web UI shares.
type theme struct {
	faint, bold, accent, ok, warn, bad lipgloss.Style
	// selectBg starts the selected row's background.
	selectBg string
	g        glyphs
}

func newTheme(dark, ascii bool) theme {
	s := lipgloss.NewStyle()
	color := func(st tokens.State) lipgloss.Style {
		if dark {
			return s.Foreground(lipgloss.Color(st.ANSI))
		}
		// Cyan is hard to read on white in the stock palettes, so the
		// accent's light color is blue.
		return s.Foreground(lipgloss.Color(st.ANSILight))
	}
	th := theme{
		faint:    s.Faint(true),
		bold:     s.Bold(true),
		accent:   color(tokens.Connecting()),
		ok:       color(tokens.Active()),
		warn:     color(tokens.Paused()),
		bad:      color(tokens.Failed()),
		selectBg: "\x1b[48;5;236m",
		g:        unicodeGlyphs,
	}
	if !dark {
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
