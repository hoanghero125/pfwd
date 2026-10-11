package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hoanghero125/pfwd/internal/config"
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

func TestErrorLineListsConfigProblems(t *testing.T) {
	err := &config.Problems{Path: "/c.toml", List: []config.Problem{
		{Line: 2, Col: 1, Key: "detect.intervl", Msg: "unknown setting"},
		{Line: 5, Col: 15, Key: "forward.on_conflict", Msg: "bad \x1b[2J value"},
	}}
	got := errorLine(fmt.Errorf("load: %w", err))
	want := "Error: fix the config file, then check it with `pfwd config validate`:\n" +
		"  /c.toml:2:1: detect.intervl: unknown setting\n" +
		"  /c.toml:5:15: forward.on_conflict: bad \ufffd[2J value\n"
	if got != want {
		t.Errorf("errorLine =\n%q\nwant\n%q", got, want)
	}
}
