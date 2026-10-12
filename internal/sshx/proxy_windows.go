package sshx

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// proxyCommand runs line the way Windows OpenSSH runs a ProxyCommand: as a
// command line given to CreateProcess, without a shell. Killing it then
// kills the command itself, not a cmd.exe that leaves it running. It gets
// no console window.
func proxyCommand(line string) (*exec.Cmd, error) {
	name := firstToken(line)
	if name == "" {
		return nil, errors.New("the ProxyCommand is empty")
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd, nil
}

// firstToken is the program CreateProcess takes from a command line: the
// quoted text at its start, or the text up to the first space or tab.
func firstToken(line string) string {
	line = strings.TrimLeft(line, " \t")
	if rest, ok := strings.CutPrefix(line, `"`); ok {
		name, _, _ := strings.Cut(rest, `"`)
		return name
	}
	name, _, _ := strings.Cut(strings.ReplaceAll(line, "\t", " "), " ")
	return name
}
