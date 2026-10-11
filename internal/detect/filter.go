package detect

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// PortRange is an inclusive range of ports; a single port has Lo == Hi.
type PortRange struct{ Lo, Hi uint16 }

// String formats the range as "5173" or "3000-3999".
func (r PortRange) String() string {
	if r.Lo == r.Hi {
		return strconv.Itoa(int(r.Lo))
	}
	return strconv.Itoa(int(r.Lo)) + "-" + strconv.Itoa(int(r.Hi))
}

// ParsePortRange parses "5173" or "3000-3999". Ports run from 1 to 65535.
func ParsePortRange(s string) (PortRange, error) {
	s = strings.TrimSpace(s)
	lo, hi, isRange := strings.Cut(s, "-")
	l, err := parsePort(lo)
	if err != nil {
		return PortRange{}, fmt.Errorf("invalid port range %q: %w", s, err)
	}
	if !isRange {
		return PortRange{l, l}, nil
	}
	h, err := parsePort(hi)
	if err != nil {
		return PortRange{}, fmt.Errorf("invalid port range %q: %w", s, err)
	}
	if h < l {
		return PortRange{}, fmt.Errorf("invalid port range %q: the first port must not be above the second", s)
	}
	return PortRange{l, h}, nil
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%q is not a port from 1 to 65535", s)
	}
	return uint16(n), nil
}

// PortSet is a list of port ranges.
type PortSet []PortRange

// Contains reports whether p is in any range.
func (s PortSet) Contains(p uint16) bool {
	return slices.ContainsFunc(s, func(r PortRange) bool { return r.Lo <= p && p <= r.Hi })
}

// Filter decides which ports are hidden and which are auto-forwarded.
type Filter struct {
	// IgnorePorts and IgnoreProcesses are never auto-forwarded and are
	// hidden unless the user shows hidden ports.
	IgnorePorts     PortSet
	IgnoreProcesses []string
	// AutoPorts, when not empty, limits auto-forward to these ports.
	AutoPorts PortSet
}

// DefaultFilter returns the default ignore list: port 22 and common system
// daemons. Each call returns new slices.
func DefaultFilter() Filter {
	return Filter{
		IgnorePorts:     PortSet{{22, 22}},
		IgnoreProcesses: []string{"sshd", "systemd-resolved", "cupsd", "rpcbind", "containerd", "dnsmasq", "chronyd"},
	}
}

// Ignored reports whether p is on the ignore list.
func (f Filter) Ignored(p RemotePort) bool {
	if f.IgnorePorts.Contains(p.Port) {
		return true
	}
	return p.Process != "" && slices.ContainsFunc(f.IgnoreProcesses, func(name string) bool {
		return processIs(p.Process, name)
	})
}

// AutoForward reports whether p may be auto-forwarded.
func (f Filter) AutoForward(p RemotePort) bool {
	return !f.Ignored(p) && (len(f.AutoPorts) == 0 || f.AutoPorts.Contains(p.Port))
}

// commLen is the length Linux truncates process names (comm) to.
const commLen = 15

// processIs reports whether a process name as ss shows it is name. The
// kernel keeps only the first 15 characters, so "systemd-resolve" is
// systemd-resolved.
func processIs(proc, name string) bool {
	return proc == name || (len(proc) == commLen && strings.HasPrefix(name, proc))
}
