//go:build windows

package sshx

import (
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
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
		return openPipe(path)
	}
	return net.DialTimeout("unix", path, 2*time.Second) //nolint:gosec // a local agent socket path from ssh config or SSH_AUTH_SOCK
}

// openPipe opens a named pipe at the SecurityIdentification level. When the
// agent service is stopped, any local user can create a pipe with the
// agent's name; with the default flags that pipe's server could impersonate
// pfwd's token.
func openPipe(path string) (io.ReadWriteCloser, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
