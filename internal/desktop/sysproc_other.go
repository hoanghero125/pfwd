//go:build !unix && !windows

package desktop

import "syscall"

// sysProcAttr is nil where there is nothing to detach from.
func sysProcAttr() *syscall.SysProcAttr { return nil }
