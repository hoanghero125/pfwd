package forward

import (
	"fmt"
	"net"
	"strconv"
)

// suggestRange is how far above a busy port Listen looks for a free one.
const suggestRange = 100

// PortInUseError means the local port is taken. Suggested is a free port
// close to it, or 0 when none of the next 100 ports is free.
type PortInUseError struct {
	Addr      string
	Port      uint16
	Suggested uint16
}

// Error describes the conflict and the way out.
func (e *PortInUseError) Error() string {
	if e.Suggested == 0 {
		return fmt.Sprintf("local port %d is already in use on %s, and so are the next %d; free it or choose another port", e.Port, e.Addr, suggestRange)
	}
	return fmt.Sprintf("local port %d is already in use on %s; forward to port %d instead, or free it", e.Port, e.Addr, e.Suggested)
}

// Listen binds a local TCP port; bind defaults to 127.0.0.1. A busy port
// returns a *PortInUseError with a free port nearby. On Windows that
// includes ports reserved for Hyper-V, which fail with WSAEACCES.
func Listen(bind string, port uint16) (net.Listener, error) {
	if bind == "" {
		bind = "127.0.0.1"
	}
	addr := net.JoinHostPort(bind, strconv.Itoa(int(port)))
	ln, err := net.Listen("tcp", addr)
	switch {
	case err == nil:
		return ln, nil
	case isAddrInUse(err):
		return nil, &PortInUseError{Addr: bind, Port: port, Suggested: suggestPort(bind, port)}
	case isPermissionDenied(err):
		return nil, fmt.Errorf("local port %d needs root on this system; forward to a port above 1023 instead: %w", port, err)
	default:
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
}

// suggestPort returns the first free port above port, or 0.
func suggestPort(bind string, port uint16) uint16 {
	for p := int(port) + 1; p <= int(port)+suggestRange && p <= 65535; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(p)))
		if err == nil {
			_ = ln.Close()
			return uint16(p)
		}
	}
	return 0
}
