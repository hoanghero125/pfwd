package detect

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// sysProcAttr runs local tools without a console window, so they can't
// touch the terminal pfwd draws in.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
