package cli

import (
	"errors"
	"strings"
	"testing"
)

// TestErrorLineStripsControlCharacters pins the fix for escape injection on
// exit: connection errors carry text from the server (x/crypto quotes the
// server's host key algorithm name verbatim), and Execute prints them after
// the TUI has handed the terminal back.
func TestErrorLineStripsControlCharacters(t *testing.T) {
	err := errors.New("ssh: unknown key algorithm: \x1b]52;c;ZXZpbA==\x07\x1b[2J\u009b2J\u202e")
	line := errorLine(err)
	for _, bad := range []string{"\x1b", "\x07", "\u009b", "\u202e"} {
		if strings.Contains(line, bad) {
			t.Errorf("errorLine kept %q: %q", bad, line)
		}
	}
	if !strings.HasPrefix(line, "Error: ssh: unknown key algorithm: ") || !strings.HasSuffix(line, "\n") {
		t.Errorf("errorLine = %q", line)
	}
}
