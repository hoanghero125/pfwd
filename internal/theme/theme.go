// Package theme holds pfwd's design tokens: the words, glyphs and colors of
// forward and connection states, shared by the TUI (ANSI colors) and the
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
	// ANSI and ANSILight are the terminal palette colors on a dark and a
	// light background. Empty means the terminal's faint default.
	ANSI, ANSILight string
	// Dark and Light are the web UI's colors.
	Dark, Light string
}

// The states, in the order front ends list them.
var (
	active     = State{Name: "active", Word: "active", Glyph: "●", ASCII: "*", ANSI: "2", ANSILight: "2", Dark: "#4CC38A", Light: "#1B7A4B"}
	paused     = State{Name: "paused", Word: "paused", Glyph: "◌", ASCII: "o", ANSI: "3", ANSILight: "3", Dark: "#E3B64B", Light: "#855D00"}
	failed     = State{Name: "failed", Word: "failed", Glyph: "✗", ASCII: "x", ANSI: "1", ANSILight: "1", Dark: "#F2736C", Light: "#B42318"}
	starting   = State{Name: "starting", Word: "starting", Glyph: "…", ASCII: ".", ANSI: "6", ANSILight: "4", Dark: "#52C0D8", Light: "#0B6A82"}
	connecting = State{Name: "connecting", Word: "connecting", Glyph: "◐", ASCII: "~", ANSI: "6", ANSILight: "4", Dark: "#52C0D8", Light: "#0B6A82"}
	closed     = State{Name: "closed", Word: "closed", Glyph: "○", ASCII: "-", Dark: "#9BA5B2", Light: "#545F6E"}
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

// Palette returns the web UI's surface colors. The accent is the color of
// the connecting and starting states.
func Palette() []Color {
	return []Color{
		{Name: "bg", Dark: "#1A1F26", Light: "#F6F7F9"},
		{Name: "surface", Dark: "#212731", Light: "#FFFFFF"},
		{Name: "line", Dark: "#333B46", Light: "#D8DDE4"},
		{Name: "text", Dark: "#E3E7ED", Light: "#1C222A"},
		{Name: "muted", Dark: "#9BA5B2", Light: "#545F6E"},
		{Name: "hover", Dark: "#283039", Light: "#EEF1F4"},
		{Name: "select", Dark: "#22343C", Light: "#E3EEF2"},
		{Name: "accent", Dark: connecting.Dark, Light: connecting.Light},
	}
}
