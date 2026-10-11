//go:build !windows

package notify

import "syscall"

// sysProcAttr is nil: helpers need no special start-up outside Windows.
func sysProcAttr() *syscall.SysProcAttr { return nil }
