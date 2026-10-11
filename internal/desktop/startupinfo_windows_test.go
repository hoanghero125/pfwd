package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// startupInfo is what a helper started by runHelper was told about its
// window: "flags show".
func startupInfo() string {
	var si windows.StartupInfo
	if err := windows.GetStartupInfo(&si); err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%d %d", si.Flags, si.ShowWindow)
}

// TestHelpersMayShowWindows: rundll32 hands its show-window flag on to the
// browser it starts, so a hidden rundll32 can mean a hidden browser.
// Helpers must start without STARTF_USESHOWWINDOW.
func TestHelpersMayShowWindows(t *testing.T) {
	out := filepath.Join(t.TempDir(), "si")
	t.Setenv(helperOutEnv, out)
	if err := runHelper(t.Context(), selfHelper(t, "startupinfo"), ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var flags, show uint32
	if _, err := fmt.Sscan(strings.TrimSpace(string(data)), &flags, &show); err != nil {
		t.Fatalf("startup info %q: %v", data, err)
	}
	if flags&windows.STARTF_USESHOWWINDOW != 0 && show == windows.SW_HIDE {
		t.Errorf("the helper was started hidden (flags %#x, show %d)", flags, show)
	}
}
