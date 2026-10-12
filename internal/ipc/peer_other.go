//go:build !linux && !darwin && !freebsd && !windows

package ipc

import "net"

// checkPeer accepts every peer where the OS can't say who it is; the
// socket's mode and directory keep other users out.
func checkPeer(net.Conn) error { return nil }
