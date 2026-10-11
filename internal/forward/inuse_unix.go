//go:build !windows

package forward

import (
	"errors"
	"syscall"
)

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// isPermissionDenied reports a privileged port (below 1024) bound without root.
func isPermissionDenied(err error) bool {
	return errors.Is(err, syscall.EACCES)
}
