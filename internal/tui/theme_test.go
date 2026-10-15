package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	tokens "github.com/hoanghero125/pfwd/internal/theme"
)

// TestTUIMatchesTheme: the TUI draws states with the shared tokens, so the
// web UI shows the same glyphs and color families.
func TestTUIMatchesTheme(t *testing.T) {
	for _, c := range []struct {
		name       string
		got, ascii string
		want       tokens.State
	}{
		{"active", unicodeGlyphs.active, asciiGlyphs.active, tokens.Active()},
		{"paused", unicodeGlyphs.paused, asciiGlyphs.paused, tokens.Paused()},
		{"failed", unicodeGlyphs.failed, asciiGlyphs.failed, tokens.Failed()},
		{"starting", unicodeGlyphs.starting, asciiGlyphs.starting, tokens.Starting()},
		{"connecting", unicodeGlyphs.connecting, asciiGlyphs.connecting, tokens.Connecting()},
		{"closed", unicodeGlyphs.closed, asciiGlyphs.closed, tokens.Closed()},
	} {
		if c.got != c.want.Glyph || c.ascii != c.want.ASCII {
			t.Errorf("%s: TUI glyphs %q/%q, theme %q/%q", c.name, c.got, c.ascii, c.want.Glyph, c.want.ASCII)
		}
	}
	for _, dark := range []bool{true, false} {
		th := newTheme(dark, false, false)
		hex := func(s tokens.State) string {
			if dark {
				return s.Dark
			}
			return s.Light
		}
		for _, c := range []struct {
			name  string
			style lipgloss.Style
			want  string
		}{
			{"ok", th.ok, hex(tokens.Active())},
			{"warn", th.warn, hex(tokens.Paused())},
			{"bad", th.bad, hex(tokens.Failed())},
			{"accent", th.accent, hex(tokens.Connecting())},
		} {
			if c.style.GetForeground() != lipgloss.Color(c.want) {
				t.Errorf("dark %v: %s color is %v, theme says %s", dark, c.name, c.style.GetForeground(), c.want)
			}
		}
	}
}

// foreground is the escape sequence that makes hex the foreground color.
func foreground(hex string) string {
	return strings.Replace(background(hex), "[48;", "[38;", 1)
}
