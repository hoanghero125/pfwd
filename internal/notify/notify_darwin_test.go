package notify

import (
	"testing"
)

func TestNotifyRunsOsascript(t *testing.T) {
	n, calls := fakeNotifier(nil)
	if err := n.Notify(t.Context(), "Forwarded :3000", "vite: localhost:3000"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].name != "/usr/bin/osascript" {
		t.Fatalf("calls = %+v; want one run of /usr/bin/osascript", *calls)
	}
	args := (*calls)[0].args
	if args[len(args)-2] != "Forwarded :3000" || args[len(args)-1] != "vite: localhost:3000" {
		t.Errorf("args = %q", args)
	}
}
