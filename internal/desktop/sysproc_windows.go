package desktop

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// sysProcAttr starts helpers without a console, so they can't touch the
// one pfwd draws in.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}
