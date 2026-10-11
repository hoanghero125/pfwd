//go:build windows

package forward

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isAddrInUse also treats WSAEACCES as in use: Windows returns it for ports
// in the ranges Hyper-V and WSL reserve, and for ports another process
// holds exclusively. Either way, another port is the fix.
func isAddrInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES)
}

// isPermissionDenied is always false: Windows has no privileged ports.
func isPermissionDenied(error) bool {
	return false
}
