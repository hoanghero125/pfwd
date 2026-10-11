package desktop

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// sysProcAttr starts console helpers (clip.exe) without a console, so they
// can't touch the one pfwd draws in. It leaves the show-window flag alone:
// rundll32 passes it on to the browser it starts, and a hidden flag there
// can mean a hidden browser window.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
