package tui

import (
	"fmt"
	"strconv"

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
