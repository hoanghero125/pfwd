package forward

import (
	"fmt"
	"net"
	"net/netip"
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
//
// For a loopback bind, a port that another local process holds on the
// wildcard addresses or on ::1 counts as busy too. Windows and macOS let
// 127.0.0.1:P bind next to those, and a browser that tries ::1 first for
// localhost would then reach that process instead of the forward.
func Listen(bind string, port uint16) (net.Listener, error) {
	if bind == "" {
		bind = "127.0.0.1"
	}
	if isLoopback(bind) && heldElsewhere(port) {
		return nil, &PortInUseError{Addr: bind, Port: port, Suggested: suggestPort(bind, port)}
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

func isLoopback(bind string) bool {
	ip, err := netip.ParseAddr(bind)
	return bind == "localhost" || (err == nil && ip.IsLoopback())
}

// heldElsewhere reports whether another socket holds port on 0.0.0.0, [::]
// or [::1], by binding each briefly. Probes that fail for another reason
// (no IPv6 on this machine) are ignored.
func heldElsewhere(port uint16) bool {
	probes := []struct{ network, host string }{
		{"tcp4", "0.0.0.0"},
		{"tcp6", "::"},
		{"tcp6", "::1"},
	}
	for _, pr := range probes {
		ln, err := net.Listen(pr.network, net.JoinHostPort(pr.host, strconv.Itoa(int(port))))
		if err == nil {
			_ = ln.Close()
			continue
		}
		if isAddrInUse(err) {
			return true
		}
	}
	return false
}

// suggestPort returns the first port above port that is free on bind and,
// for a loopback bind, not held on the other local addresses; 0 if none.
func suggestPort(bind string, port uint16) uint16 {
	for p := int(port) + 1; p <= int(port)+suggestRange && p <= 65535; p++ {
		if isLoopback(bind) && heldElsewhere(uint16(p)) {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(p)))
		if err == nil {
			_ = ln.Close()
			return uint16(p)
		}
	}
	return 0
}

// FreePort asks the OS for a free TCP port on bind ("" means 127.0.0.1).
// Another program can take it before it is used, so Listen still checks.
func FreePort(bind string) (uint16, error) {
	if bind == "" {
		bind = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, "0"))
	if err != nil {
		return 0, fmt.Errorf("find a free local port on %s: %w; check forward.bind", bind, err)
	}
	defer ln.Close()
	ap, err := netip.ParseAddrPort(ln.Addr().String())
	if err != nil {
		return 0, fmt.Errorf("find a free local port: %w", err)
	}
	return ap.Port(), nil
}
