package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// theme holds the TUI's styles. Colors are the terminal's own ANSI palette,
// so pfwd follows the user's light or dark theme.
type theme struct {
	faint, bold, accent, ok, warn, bad lipgloss.Style
}

func newTheme() theme {
	s := lipgloss.NewStyle()
	return theme{
		faint:  s.Faint(true),
		bold:   s.Bold(true),
		accent: s.Foreground(lipgloss.Color("6")),
		ok:     s.Foreground(lipgloss.Color("2")),
		warn:   s.Foreground(lipgloss.Color("3")),
		bad:    s.Foreground(lipgloss.Color("1")),
	}
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

// clean makes text from the server, the ssh config or an error safe to draw.
// Control characters (C0 including ESC, DEL and C1) and bidi overrides
// become U+FFFD, so a hostile process name or server message can't move
// the cursor, clear the screen, retitle the window or write the clipboard.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "\ufffd")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r),
			r >= '\u202a' && r <= '\u202e',
			r >= '\u2066' && r <= '\u2069':
			return '\ufffd'
		}
		return r
	}, s)
}
