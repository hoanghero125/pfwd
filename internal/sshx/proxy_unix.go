//go:build unix

package sshx

import (
	"os"
	"os/exec"
	"syscall"
)

// proxyCommand runs line the way OpenSSH runs a ProxyCommand: through the
// user's shell with exec, so the shell is replaced by the command. The
// command gets its own session, so it can't read the terminal pfwd draws in.
func proxyCommand(line string) (*exec.Cmd, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", "exec "+line) //nolint:gosec // the user's own ProxyCommand from the ssh config, run as OpenSSH runs it
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}
