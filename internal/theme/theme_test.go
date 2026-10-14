package theme

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestGeneratedFilesUpToDate: the web UI's copy matches the tokens. Run
// `go generate ./internal/theme` after changing them.
func TestGeneratedFilesUpToDate(t *testing.T) {
	for name, want := range map[string]string{"theme.gen.css": CSS(), "theme.gen.ts": TS()} {
		got, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("web/src/lib/%s is stale; run `go generate ./internal/theme`", name)
		}
	}
}

// luminance is the WCAG relative luminance of a #RRGGBB color.
func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	n, err := strconv.ParseUint(hex[1:], 16, 32)
	if len(hex) != 7 || err != nil {
		t.Fatalf("bad color %q", hex)
	}
	ch := func(v uint64) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(n>>16&0xff) + 0.7152*ch(n>>8&0xff) + 0.0722*ch(n&0xff)
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestContrast: every text color reads at WCAG AA (4.5:1) on every
// background it is drawn on, in both themes.
func TestContrast(t *testing.T) {
	colors := map[string]Color{}
	for _, c := range Palette() {
		colors[c.Name] = c
	}
	for _, s := range States() {
		colors[s.Name] = Color{Name: s.Name, Dark: s.Dark, Light: s.Light}
	}
	texts := []string{"text", "muted", "active", "paused", "failed", "starting", "connecting", "closed", "accent"}
	backgrounds := []string{"bg", "surface", "hover", "select"}
	type pair struct{ fg, bg string }
	pairs := []pair{{"surface", "accent"}, {"surface", "paused"}} // primary buttons, the daemon banner
	for _, fg := range texts {
		for _, bg := range backgrounds {
			pairs = append(pairs, pair{fg, bg})
		}
	}
	for _, p := range pairs {
		for _, dark := range []bool{false, true} {
			fg, bg := colors[p.fg].Light, colors[p.bg].Light
			if dark {
				fg, bg = colors[p.fg].Dark, colors[p.bg].Dark
			}
			if r := contrast(t, fg, bg); r < 4.5 {
				t.Errorf("%s on %s (dark %v): %s on %s is %.2f:1, want 4.5:1", p.fg, p.bg, dark, fg, bg, r)
			}
		}
	}
}

func TestStatesComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range States() {
		if s.Name == "" || s.Word == "" || s.Glyph == "" || s.ASCII == "" || s.Dark == "" || s.Light == "" || seen[s.Name] {
			t.Errorf("incomplete or duplicate state %+v", s)
		}
		seen[s.Name] = true
	}
}
