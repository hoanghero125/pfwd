// Package theme holds pfwd's design tokens: the words, glyphs and colors of
// forward and connection states, and the palette, shared by the TUI and the
// web UI (CSS variables). `go generate ./internal/theme` writes the web UI's
// copy, web/src/lib/theme.gen.css and theme.gen.ts.
package theme

//go:generate go run ./gen ../../web/src/lib

// State is how one state looks in every front end: the same word, the same
// glyph and the same color family.
type State struct {
	// Name is the key in CSS (--active) and TypeScript.
	Name string
	// Word is what the state is called next to its glyph.
	Word string
	// Glyph and ASCII are the symbols for Unicode and ASCII-only terminals.
	Glyph, ASCII string
	// Dark and Light are the colors on a dark and a light background.
	Dark, Light string
}

// The states, in the order front ends list them.
var (
	active     = State{Name: "active", Word: "active", Glyph: "●", ASCII: "*", Dark: "#7DC88E", Light: "#1E7A45"}
	paused     = State{Name: "paused", Word: "paused", Glyph: "◌", ASCII: "o", Dark: "#E2B45A", Light: "#865B00"}
	failed     = State{Name: "failed", Word: "failed", Glyph: "✗", ASCII: "x", Dark: "#EC6F67", Light: "#B3261E"}
	starting   = State{Name: "starting", Word: "starting", Glyph: "…", ASCII: ".", Dark: "#6CC4D9", Light: "#0A6A82"}
	connecting = State{Name: "connecting", Word: "connecting", Glyph: "◐", ASCII: "~", Dark: "#6CC4D9", Light: "#0A6A82"}
	closed     = State{Name: "closed", Word: "closed", Glyph: "○", ASCII: "-", Dark: "#8F929A", Light: "#5A5D66"}
)

// Active is a working forward or a connected host.
func Active() State { return active }

// Paused is a forward waiting for its port or its connection.
func Paused() State { return paused }

// Failed is a forward or a connection that stopped with an error.
func Failed() State { return failed }

// Starting is a forward that isn't listening yet.
func Starting() State { return starting }

// Connecting is a host being connected or reconnected.
func Connecting() State { return connecting }

// Closed is a host that isn't connected.
func Closed() State { return closed }

// States returns every state.
func States() []State {
	return []State{active, paused, failed, starting, connecting, closed}
}

// Color is one web UI color in both themes.
type Color struct {
	Name        string
	Dark, Light string
}

// Palette returns the surface and text colors, from the page background
// out: the rail beside it, panels, and raised rows (hover, selection). The
// TUI uses muted for secondary text and raised for the selected row.
// The accent is the color of the connecting and starting states; accent-ink
// is text on an accent fill.
func Palette() []Color {
	return []Color{
		{Name: "bg", Dark: "#131416", Light: "#F7F7F8"},
		{Name: "rail", Dark: "#0F1012", Light: "#EFEFF1"},
		{Name: "surface", Dark: "#18191C", Light: "#FFFFFF"},
		{Name: "raised", Dark: "#202126", Light: "#F0F0F3"},
		{Name: "line", Dark: "#2A2C31", Light: "#E3E3E8"},
		{Name: "line-strong", Dark: "#3B3E46", Light: "#CDCED6"},
		{Name: "text", Dark: "#E9E9EC", Light: "#16171A"},
		{Name: "muted", Dark: "#8F929A", Light: "#5A5D66"},
		{Name: "accent", Dark: connecting.Dark, Light: connecting.Light},
		{Name: "accent-ink", Dark: "#0B2027", Light: "#FFFFFF"},
	}
}
