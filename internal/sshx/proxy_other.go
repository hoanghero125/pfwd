//go:build !unix && !windows

package sshx

import (
	"errors"
	"os/exec"
)

// proxyCommand reports that ProxyCommand isn't supported on this system.
func proxyCommand(string) (*exec.Cmd, error) {
	return nil, errors.New("ProxyCommand is not supported on this system")
}
