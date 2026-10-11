//go:build unix

package desktop

import "syscall"

// sysProcAttr starts helpers in a new session, away from pfwd's terminal:
// they can't read it, and quitting pfwd doesn't send them SIGHUP.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
