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

func dial(ctx context.Context, path string) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, path)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, fmt.Errorf("connect to the pfwd daemon at %s: %w", path, err)
	}
	return c, nil
}

// checkPeer accepts every peer: the pipe's DACL already lets only this
// user open it.
func checkPeer(net.Conn) error { return nil }
