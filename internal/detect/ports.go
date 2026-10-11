package detect

import (
	"net/netip"
	"slices"
	"strings"
)

// RemotePort is a TCP port listening on the server. Parsers return one per
// socket; Merge folds the IPv4 and IPv6 sockets of a port into one.
type RemotePort struct {
	Port uint16
	// Addrs are the bind addresses: "0.0.0.0", "::", "*" (dual-stack
	// wildcard, as ss prints it), "127.0.0.1", "::1" or a specific IP.
	Addrs []string
	// PID and Process are 0 and "" when the server doesn't show them,
	// which is the case for other users' processes unless we are root.
	PID     int
	Process string
}

// Merge returns one entry per port, sorted by port, with sorted, deduplicated
// addresses. The process is taken from the first socket that has one.
func Merge(ports []RemotePort) []RemotePort {
	byPort := make(map[uint16]int)
	var out []RemotePort
	for _, p := range ports {
		i, ok := byPort[p.Port]
		if !ok {
			i = len(out)
			byPort[p.Port] = i
			out = append(out, RemotePort{Port: p.Port})
		}
		m := &out[i]
		m.Addrs = append(m.Addrs, p.Addrs...)
		if m.Process == "" && p.Process != "" {
			m.Process, m.PID = p.Process, p.PID
		}
	}
	for i := range out {
		slices.Sort(out[i].Addrs)
		out[i].Addrs = slices.Compact(out[i].Addrs)
	}
	slices.SortFunc(out, func(a, b RemotePort) int { return int(a.Port) - int(b.Port) })
	return out
}

// Target returns the host to dial on the server for a port with these bind
// addresses: 127.0.0.1 for IPv4 and dual-stack listeners, ::1 for IPv6-only
// ones (ss prints a dual-stack wildcard as "*" and an IPv6-only one as
// "::"), and otherwise the specific address it listens on.
func Target(addrs []string) string {
	for _, a := range addrs {
		switch a {
		case "0.0.0.0", "*", "127.0.0.1":
			return "127.0.0.1"
		}
	}
	if slices.Contains(addrs, "::1") || slices.Contains(addrs, "::") {
		return "::1"
	}
	if len(addrs) == 0 {
		return "127.0.0.1"
	}
	return addrs[0]
}

// normalizeAddr turns a bind address as ss prints it into its canonical
// form: brackets and %zone suffixes are dropped and v4-mapped IPv6 becomes
// IPv4. It reports false for anything that is not an IP or "*".
func normalizeAddr(host string) (string, bool) {
	if host == "*" {
		return "*", true
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	return ip.Unmap().String(), true
}
