//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detach makes cmd a session leader, so it outlives the terminal it was
// started from. Its stdio is the null device.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
