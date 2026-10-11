package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoanghero125/pfwd/internal/version"
)

// run executes pfwd in-process and returns its exit code, stdout and stderr.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Execute(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionCommand(t *testing.T) {
	code, out, errOut := run(t, "version")
	if code != 0 || errOut != "" {
		t.Fatalf("pfwd version: code %d, stderr %q", code, errOut)
	}
	if !strings.HasPrefix(out, "pfwd ") || !strings.Contains(out, "(commit ") {
		t.Errorf("pfwd version printed %q", out)
	}
}

func TestVersionJSON(t *testing.T) {
	for _, args := range [][]string{{"version", "--json"}, {"--json", "version"}} {
		code, out, errOut := run(t, args...)
		if code != 0 || errOut != "" {
			t.Fatalf("pfwd %v: code %d, stderr %q", args, code, errOut)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("pfwd %v: stdout is not JSON: %v\n%s", args, err, out)
		}
		for _, key := range []string{"version", "commit", "date", "go", "platform"} {
			if got[key] == "" {
				t.Errorf("pfwd %v: JSON key %q missing or empty in %s", args, key, out)
			}
		}
	}
}

func TestVersionFlag(t *testing.T) {
	code, out, _ := run(t, "--version")
	if want := version.Get().String() + "\n"; code != 0 || out != want {
		t.Errorf("pfwd --version: code %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestRootIsNotImplementedYet(t *testing.T) {
	for _, args := range [][]string{{}, {"devbox"}, {"devbox", "gpu-node"}} {
		code, out, errOut := run(t, args...)
		if code != 1 || out != "" || !strings.Contains(errOut, "not implemented yet") {
			t.Errorf("pfwd %v: code %d, stdout %q, stderr %q; want 1, empty, \"not implemented yet\"", args, code, out, errOut)
		}
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{"--no-such-flag"},
		{"version", "extra"},
		{"version", "--no-such-flag"},
		{"completion", "bash", "extra"},
		{"completion", "bsh"}, // a typo must not print help to stdout: `source <(pfwd completion bsh)`
	} {
		code, out, errOut := run(t, args...)
		if code != 2 {
			t.Errorf("pfwd %v: exit code %d, want 2 (stderr %q)", args, code, errOut)
		}
		if out != "" || !strings.HasPrefix(errOut, "Error: ") || strings.Count(errOut, "Error: ") != 1 ||
			!strings.Contains(errOut, "--help' for usage") {
			t.Errorf("pfwd %v: stdout %q, stderr %q; want empty stdout and one error line with a --help hint", args, out, errOut)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		code, out, _ := run(t, "completion", shell)
		if code != 0 || len(out) < 100 {
			t.Errorf("pfwd completion %s: code %d, %d bytes of output", shell, code, len(out))
		}
	}
}
