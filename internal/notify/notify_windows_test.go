package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestNotifyRunsPowerShell(t *testing.T) {
	n, calls := fakeNotifier(nil)
	if err := n.Notify(t.Context(), "Forwarded :3000", "vite\x1b[2J: localhost:3000"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("%d helper runs, want 1", len(*calls))
	}
	c := (*calls)[0]
	if !strings.HasSuffix(strings.ToLower(c.name), `\windowspowershell\v1.0\powershell.exe`) {
		t.Errorf("ran %q; want powershell.exe from the system directory", c.name)
	}
	if strings.Contains(strings.Join(c.env, "\n"), "\x1b") || !strings.Contains(strings.Join(c.env, "\n"), "PFWD_NOTIFY_BODY=vite") {
		t.Errorf("env = %q; want the cleaned body", c.env)
	}
}

func TestNotifyReportsPowerShellFailure(t *testing.T) {
	n, _ := fakeNotifier(errors.New("exit status 1"))
	if err := n.Notify(t.Context(), "t", "b"); err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("Notify = %v; want the helper's error", err)
	}
}
