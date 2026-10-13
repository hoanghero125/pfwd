package cli

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestHelpListsEveryCommand(t *testing.T) {
	code, out, _ := run(t, "--help")
	if code != 0 {
		t.Fatalf("pfwd --help: code %d", code)
	}
	for _, name := range []string{"attach", "completion", "config", "daemon", "doctor", "fwd", "hosts", "ls", "stop", "ui", "version"} {
		if !regexp.MustCompile(`(?m)^  ` + name + `\s`).MatchString(out) {
			t.Errorf("pfwd --help does not list %q:\n%s", name, out)
		}
	}
}

func TestStubsAreNotImplementedYet(t *testing.T) {
	for _, tc := range []struct {
		args []string
		path string // the command that must report itself, not the root TUI
	}{
		{[]string{"ui"}, "pfwd ui"},
		{[]string{"ui", "devbox", "gpu-node"}, "pfwd ui"},
	} {
		code, out, errOut := run(t, tc.args...)
		want := `"` + tc.path + `" is not implemented yet`
		if code != 1 || out != "" || !strings.Contains(errOut, want) {
			t.Errorf("pfwd %v: code %d, stdout %q, stderr %q; want 1, empty stdout, %s", tc.args, code, out, errOut, want)
		}
	}
}

func TestStubUsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{"fwd"},
		{"fwd", "devbox"},
		{"ls", "a", "b"},
		{"stop"},
		{"hosts", "extra"},
		{"daemon", "bogus"},
		{"daemon", "start", "extra"},
		{"attach", "extra"},
		{"doctor", "a", "b"},
		{"config", "bogus"},
		{"config", "path", "extra"},
		{"ls", "--no-such-flag"},
	} {
		code, _, errOut := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "--help' for usage") {
			t.Errorf("pfwd %v: code %d, stderr %q; want 2 with a --help hint", args, code, errOut)
		}
	}
}

// badArgs lists, for every command in the tree, positional arguments that
// must be rejected with exit code 2, or nil when the command accepts any.
// TestEveryCommandRejectsBadUsage fails when a command is missing here, so
// each new command has to declare its usage errors.
var badArgs = map[string][]string{
	"pfwd":                       {"@devbox"}, // an empty user
	"pfwd attach":                {"extra"},
	"pfwd completion":            {"bsh"},
	"pfwd completion bash":       {"extra"},
	"pfwd completion fish":       {"extra"},
	"pfwd completion powershell": {"extra"},
	"pfwd completion zsh":        {"extra"},
	"pfwd config":                {"bogus"},
	"pfwd config edit":           {"extra"},
	"pfwd config path":           {"extra"},
	"pfwd config validate":       {"extra"},
	"pfwd daemon":                {"bogus"},
	"pfwd daemon start":          {"extra"},
	"pfwd daemon run":            {"extra"},
	"pfwd daemon status":         {"extra"},
	"pfwd daemon stop":           {"extra"},
	"pfwd doctor":                {"a", "b"},
	"pfwd fwd":                   {"devbox"},
	"pfwd hosts":                 {"extra"},
	"pfwd ls":                    {"a", "b"},
	"pfwd stop":                  {},
	"pfwd ui":                    nil, // [host...]
	"pfwd version":               {"extra"},
}

func TestEveryCommandRejectsBadUsage(t *testing.T) {
	seen := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		path := c.CommandPath()
		seen[path] = true
		words := strings.Fields(path)[1:]
		args, ok := badArgs[path]
		if !ok {
			t.Errorf("%q has no badArgs entry; declare which arguments it rejects", path)
		}
		invocations := [][]string{append(append([]string{}, words...), "--no-such-flag")}
		if args != nil {
			invocations = append(invocations, append(append([]string{}, words...), args...))
		}
		for _, inv := range invocations {
			if code, out, errOut := run(t, inv...); code != 2 || out != "" {
				t.Errorf("pfwd %v: code %d, stdout %q, stderr %q; want 2 and empty stdout", inv, code, out, errOut)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd(io.Discard, io.Discard))
	for path := range badArgs {
		if !seen[path] {
			t.Errorf("badArgs lists %q, which is not a command", path)
		}
	}
}

func TestGroupWithoutSubcommandShowsHelp(t *testing.T) {
	for _, group := range []string{"daemon", "config"} {
		code, out, _ := run(t, group)
		if code != 0 || !strings.Contains(out, "Available Commands:") {
			t.Errorf("pfwd %s: code %d, stdout %q; want help and 0", group, code, out)
		}
	}
}
