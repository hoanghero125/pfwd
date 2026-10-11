//go:build windows

package sshx

import (
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// openSSHAgentPipe is the named pipe of the Windows OpenSSH agent service.
const openSSHAgentPipe = `\\.\pipe\openssh-ssh-agent`

// openAgent connects to IdentityAgent, then SSH_AUTH_SOCK, then the Windows
// OpenSSH agent pipe. SSH_AUTH_SOCK set by Git Bash points at an MSYS socket
// that native programs can't open, so a failure there falls through to the
// pipe.
func openAgent(hc *HostConfig) (io.ReadWriteCloser, error) {
	if hc.IdentityAgent != "" {
		return dialAgentPath(hc.IdentityAgent)
	}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if c, err := dialAgentPath(sock); err == nil {
			return c, nil
		}
	}
	return dialAgentPath(openSSHAgentPipe)
}

func dialAgentPath(path string) (io.ReadWriteCloser, error) {
	if strings.HasPrefix(path, `\\.\pipe\`) {
		return os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // an agent pipe path from ssh config or the environment
	}
	return net.DialTimeout("unix", path, 2*time.Second) //nolint:gosec // a local agent socket path from ssh config or SSH_AUTH_SOCK
}
