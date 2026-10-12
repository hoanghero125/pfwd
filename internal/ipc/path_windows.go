//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// DefaultPath is where the daemon listens: $PFWD_SOCKET, else the named
// pipe \\.\pipe\pfwd-<user SID>.
func DefaultPath() string {
	if p := os.Getenv("PFWD_SOCKET"); p != "" {
		return p
	}
	sid, err := userSID()
	if err != nil {
		return `\\.\pipe\pfwd-` + os.Getenv("USERNAME")
	}
	return `\\.\pipe\pfwd-` + sid
}

// Listen listens on the named pipe at path, which only the current user
// can open. A live daemon on the pipe is ErrRunning.
func Listen(path string) (net.Listener, error) {
	sid, err := userSID()
	if err != nil {
		return nil, fmt.Errorf("find the current user's SID: %w", err)
	}
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
	if err != nil {
		if c, derr := dial(context.Background(), path); derr == nil {
			_ = c.Close()
			return nil, ErrRunning
		}
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	return ln, nil
}

// userSID is the current user's SID as a string.
func userSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// dial connects to the daemon at path, and checks that the pipe's server
// runs as this user: pipe names are global, so another user could create
// ours first to read the passwords pfwd passes on.
func dial(ctx context.Context, path string) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, path)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, fmt.Errorf("connect to the pfwd daemon at %s: %w", path, err)
	}
	if err := verifyServer(c); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("refusing the pipe %s: %w", path, err)
	}
	return c, nil
}

// verifyServer checks that the process serving the pipe runs as this user.
func verifyServer(c net.Conn) error {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("can't tell who serves it")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(f.Fd()), &pid); err != nil {
		return fmt.Errorf("can't tell who serves it: %w", err)
	}
	theirs, err := processSID(pid)
	if err != nil {
		return fmt.Errorf("process %d serves it, and it can't be checked: %w", pid, err)
	}
	mine, err := userSID()
	if err != nil {
		return err
	}
	if theirs != mine {
		return fmt.Errorf("another user's process (%d) serves it; end that process, or set PFWD_SOCKET to another pipe name", pid)
	}
	return nil
}

// processSID is the SID of the user process pid runs as.
func processSID(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return "", err
	}
	defer func() { _ = tok.Close() }()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// checkPeer accepts every peer: the pipe's DACL already lets only this
// user open it.
func checkPeer(net.Conn) error { return nil }
