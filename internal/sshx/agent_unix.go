//go:build !windows

package sshx

import (
	"errors"
	"io"
	"net"
	"os"
	"time"
)

// openAgent connects to the ssh-agent socket from IdentityAgent or
// SSH_AUTH_SOCK.
func openAgent(hc *HostConfig) (io.ReadWriteCloser, error) {
	path := hc.IdentityAgent
	if path == "" {
		path = os.Getenv("SSH_AUTH_SOCK")
	}
	if path == "" {
		return nil, errors.New("no ssh-agent: SSH_AUTH_SOCK is not set")
	}
	return net.DialTimeout("unix", path, 2*time.Second) //nolint:gosec // a local agent socket path from ssh config or SSH_AUTH_SOCK
}
