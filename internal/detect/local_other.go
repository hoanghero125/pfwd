//go:build !windows

package detect

import "syscall"

// sysProcAttr needs nothing special off Windows.
func sysProcAttr() *syscall.SysProcAttr { return nil }
