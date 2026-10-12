//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// DefaultPath is where the daemon listens: $PFWD_SOCKET, else
// $XDG_RUNTIME_DIR/pfwd/pfwd.sock, else <temp dir>/pfwd-<uid>/pfwd.sock.
func DefaultPath() string {
	if p := os.Getenv("PFWD_SOCKET"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "pfwd", "pfwd.sock")
	}
	return filepath.Join(os.TempDir(), "pfwd-"+strconv.Itoa(os.Getuid()), "pfwd.sock")
}

// Listen listens on the socket at path. Its directory is created with mode
// 0700 and must belong to this user with no access for anyone else. A
// socket left by a daemon that died is removed; a live daemon is
// ErrRunning.
func Listen(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
			return nil, ErrRunning
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove the stale socket %s: %w", path, err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("make %s private: %w", path, err)
	}
	return ln, nil
}

// privateDir checks that dir is a real directory of this user that no one
// else can enter.
func privateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("check %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory; remove it, or set PFWD_SOCKET to another path", dir)
	case ok && int(st.Uid) != os.Getuid():
		return fmt.Errorf("%s belongs to another user; remove it, or set PFWD_SOCKET to another path", dir)
	case info.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("%s can be opened by other users (mode %o); run `chmod 700 %s`", dir, info.Mode().Perm(), dir)
	}
	return nil
}

// dial connects to the daemon at path, after checking that the socket and
// its directory are this user's: in a shared temp directory another user
// could make them first, to read the passwords pfwd passes on. Where the
// OS says who the server is, it must be this user too.
func dial(ctx context.Context, path string) (net.Conn, error) {
	if err := privateDir(filepath.Dir(path)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotRunning
		}
		return nil, fmt.Errorf("refusing the daemon socket %s: %w", path, err)
	}
	if info, err := os.Lstat(path); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
			return nil, fmt.Errorf("refusing the daemon socket %s: it belongs to user %d; remove it", path, st.Uid)
		}
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, fmt.Errorf("connect to the pfwd daemon at %s: %w", path, err)
	}
	if err := checkPeer(c); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("refusing the daemon at %s: %w", path, err)
	}
	return c, nil
}
