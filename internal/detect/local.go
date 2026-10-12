package detect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// RunFunc runs a local command and returns its stdout.
type RunFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// localTimeout bounds listing this machine's ports.
const localTimeout = 5 * time.Second

// LocalPorts lists the TCP ports listening on this machine, with the same
// parsers the server's ports go through: ss or /proc on Linux, lsof on
// macOS, sockstat on FreeBSD, netstat elsewhere, and netstat with tasklist
// on Windows. Process names are only as complete as the OS shows them to
// this user. The result is merged and sorted by port.
func LocalPorts(ctx context.Context, goos string, run RunFunc, readFile func(string) ([]byte, error)) ([]RemotePort, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	var ports []RemotePort
	var err error
	switch goos {
	case "linux":
		ports, err = linuxLocal(ctx, run, readFile)
	case "darwin":
		ports, err = parseRun(ctx, run, ParseLsof, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pctn")
	case "freebsd":
		ports, err = parseRun(ctx, run, ParseSockstat, "sockstat", "-46", "-l", "-P", "tcp")
	case "windows":
		ports, err = windowsLocal(ctx, run)
	default:
		ports, err = parseRun(ctx, run, ParseNetstat, "netstat", "-an", "-p", "tcp")
	}
	if err != nil {
		return nil, fmt.Errorf("list this machine's listening ports: %w", err)
	}
	return Merge(ports), nil
}

// parseRun runs a command and parses its output. A command that fails but
// prints something (lsof exits 1 when nothing listens) is still parsed.
func parseRun(ctx context.Context, run RunFunc, parse func(io.Reader) ([]RemotePort, error), name string, args ...string) ([]RemotePort, error) {
	out, err := run(ctx, name, args...)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("%s: %w; install %s or check that it runs", name, err, name)
	}
	return parse(bytes.NewReader(out))
}

// linuxLocal uses ss when it works, which names this user's processes, and
// /proc/net/tcp otherwise.
func linuxLocal(ctx context.Context, run RunFunc, readFile func(string) ([]byte, error)) ([]RemotePort, error) {
	if out, err := run(ctx, "ss", "-tlnp"); err == nil {
		return ParseSS(bytes.NewReader(out))
	}
	var all []byte
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if data, err := readFile(path); err == nil {
			all = append(all, data...)
		}
	}
	if len(all) == 0 {
		return nil, errors.New("neither ss nor /proc/net/tcp is available")
	}
	return ParseProcNetTCP(bytes.NewReader(all))
}

// windowsLocal joins netstat's IPv4 and IPv6 tables and tasklist into the
// frame ParseWindows reads. Without tasklist the ports have no names.
func windowsLocal(ctx context.Context, run RunFunc) ([]RemotePort, error) {
	var frame bytes.Buffer
	for _, family := range []string{"TCP", "TCPv6"} {
		out, err := run(ctx, "netstat", "-ano", "-p", family)
		if err != nil && family == "TCP" {
			return nil, fmt.Errorf("netstat: %w", err)
		}
		frame.Write(out)
	}
	frame.WriteString(windowsTasklistMarker + "\n")
	if out, err := run(ctx, "tasklist", "/fo", "csv", "/nh"); err == nil {
		frame.Write(out)
	}
	return ParseWindows(&frame)
}

// RunLocal runs a local command for LocalPorts, without a console window on
// Windows, and returns its stdout.
func RunLocal(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = time.Second
	return cmd.Output()
}
