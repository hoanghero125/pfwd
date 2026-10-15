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
	// cursor marks the selected table row; pointer, the selected item of a
	// list in a modal; arrow points from a remote port to its local one.
	cursor, pointer, arrow                           string
	ok, none, rule, ellipsis, down, up               string
	topLeft, topRight, bottomLeft, bottomRight, side string
	upDown, dots, block, mask                        string
	// spark is a sparkline's levels, lowest first.
	spark []string
}

var unicodeGlyphs = glyphs{
	active: tokens.Active().Glyph, paused: tokens.Paused().Glyph, failed: tokens.Failed().Glyph,
	starting: tokens.Starting().Glyph, connecting: tokens.Connecting().Glyph, closed: tokens.Closed().Glyph,
	cursor: "▌", pointer: "▶", arrow: "→",
	ok: "✓", none: "—", rule: "─", ellipsis: "…", down: "↓", up: "↑",
	topLeft: "╭", topRight: "╮", bottomLeft: "╰", bottomRight: "╯", side: "│",
	upDown: "↑ ↓", dots: "…", block: "█", mask: "•",
	spark: []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"},
}

// asciiGlyphs are for consoles whose fonts lack the Unicode ones, such as
// the legacy Windows console.
var asciiGlyphs = glyphs{
	active: tokens.Active().ASCII, paused: tokens.Paused().ASCII, failed: tokens.Failed().ASCII,
	starting: tokens.Starting().ASCII, connecting: tokens.Connecting().ASCII, closed: tokens.Closed().ASCII,
	cursor: ">", pointer: ">", arrow: "->",
	ok: "+", none: "-", rule: "-", ellipsis: "~", down: "v", up: "^",
	topLeft: "+", topRight: "+", bottomLeft: "+", bottomRight: "+", side: "|",
	upDown: "up down", dots: "...", block: "_", mask: "*",
	spark: []string{"_", ".", ",", "-", "=", "+", "*", "#"},
}

// theme holds the TUI's styles and glyphs. The colors are internal/theme's,
// the web UI's palette, for a dark or a light background; Bubble Tea
// downsamples them for terminals with fewer colors. Without colors
// (NO_COLOR), secondary text is faint instead of muted gray, and the
// selected row has no background.
type theme struct {
	faint, bold, accent, ok, warn, bad lipgloss.Style
	// selectBg starts the selected row's background.
	selectBg string
	g        glyphs
}

func newTheme(dark, ascii, noColor bool) theme {
	s := lipgloss.NewStyle()
	th := theme{bold: s.Bold(true), g: unicodeGlyphs}
	if ascii {
		th.g = asciiGlyphs
	}
	if noColor {
		th.faint, th.accent, th.ok, th.warn, th.bad = s.Faint(true), s, s, s, s
		return th
	}
	pick := func(c tokens.Color) string {
		if dark {
			return c.Dark
		}
		return c.Light
	}
	fg := func(st tokens.State) lipgloss.Style {
		return s.Foreground(lipgloss.Color(pick(tokens.Color{Dark: st.Dark, Light: st.Light})))
	}
	th.faint = s.Foreground(lipgloss.Color(pick(paletteColor("muted"))))
	th.accent = fg(tokens.Connecting())
	th.ok = fg(tokens.Active())
	th.warn = fg(tokens.Paused())
	th.bad = fg(tokens.Failed())
	th.selectBg = background(pick(paletteColor("raised")))
	return th
}

// paletteColor is internal/theme's palette color called name.
func paletteColor(name string) tokens.Color {
	for _, c := range tokens.Palette() {
		if c.Name == name {
			return c
		}
	}
	panic("tui: no palette color " + name)
}

// background is the escape sequence that makes hex, "#RRGGBB", the
// background color.
func background(hex string) string {
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		panic("tui: bad color " + hex)
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
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
	if th.selectBg == "" {
		return line
	}
	return th.selectBg + strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+th.selectBg) + "\x1b[m"
}

// sparkline draws a sparkline: missing faint baseline glyphs for the
// seconds not measured yet, then values, oldest first, one glyph each,
// scaled to the largest. Zeros are faint too, so an idle forward is a faint
// line and traffic is in the accent color.
func (th theme) sparkline(values []float64, missing int) string {
	peak := 0.0
	for _, v := range values {
		peak = max(peak, v)
	}
	top := len(th.g.spark) - 1
	var b, run strings.Builder
	runFaint := true
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if runFaint {
			b.WriteString(th.faint.Render(run.String()))
		} else {
			b.WriteString(th.accent.Render(run.String()))
		}
		run.Reset()
	}
	run.WriteString(strings.Repeat(th.g.spark[0], missing))
	for _, v := range values {
		faint := v <= 0
		if faint != runFaint {
			flush()
			runFaint = faint
		}
		level := 0
		if !faint {
			level = max(1, int(v/peak*float64(top)+0.5))
		}
		run.WriteString(th.g.spark[level])
	}
	flush()
	return b.String()
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
