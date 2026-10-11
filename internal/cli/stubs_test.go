package cli

import (
	"regexp"
	"strings"
	"testing"
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
		{[]string{"fwd", "devbox", "5432"}, "pfwd fwd"},
		{[]string{"fwd", "devbox", "5432", "15432:db.internal:5432"}, "pfwd fwd"},
		{[]string{"ls"}, "pfwd ls"},
		{[]string{"ls", "devbox"}, "pfwd ls"},
		{[]string{"stop", "devbox:5432"}, "pfwd stop"},
		{[]string{"hosts"}, "pfwd hosts"},
		{[]string{"daemon", "start"}, "pfwd daemon start"},
		{[]string{"daemon", "stop"}, "pfwd daemon stop"},
		{[]string{"daemon", "status"}, "pfwd daemon status"},
		{[]string{"attach"}, "pfwd attach"},
		{[]string{"doctor"}, "pfwd doctor"},
		{[]string{"doctor", "devbox"}, "pfwd doctor"},
		{[]string{"config", "path"}, "pfwd config path"},
		{[]string{"config", "edit"}, "pfwd config edit"},
		{[]string{"config", "validate"}, "pfwd config validate"},
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

func TestGroupWithoutSubcommandShowsHelp(t *testing.T) {
	for _, group := range []string{"daemon", "config"} {
		code, out, _ := run(t, group)
		if code != 0 || !strings.Contains(out, "Available Commands:") {
			t.Errorf("pfwd %s: code %d, stdout %q; want help and 0", group, code, out)
		}
	}
}
