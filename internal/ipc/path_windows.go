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
	// This user owns the pipe, and only this user may open it.
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "O:" + sid + "D:P(A;;GA;;;" + sid + ")"})
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

// verifyServer checks that the pipe belongs to this user. The daemon makes
// itself the owner of its pipe, and another user can't make a pipe owned
// by this one; the owner is a property of the pipe itself, so unlike the
// server's process ID it can't change between the check and the use.
func verifyServer(c net.Conn) error {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("can't tell who owns it")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("can't tell who owns it: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("can't tell who owns it: %w", err)
	}
	if owner == nil {
		return errors.New("it has no owner")
	}
	mine, err := userSID()
	if err != nil {
		return err
	}
	if owner.String() != mine {
		return fmt.Errorf("another user (%s) owns it; end the process that made it, or set PFWD_SOCKET to another pipe name", owner)
	}
	return nil
}

// checkPeer accepts every peer: the pipe's DACL already lets only this
// user open it.
func checkPeer(net.Conn) error { return nil }
