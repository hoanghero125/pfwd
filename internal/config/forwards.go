package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ForwardRule is one entry of hosts.<alias>.forwards, in the forward spec
// grammar that mirrors OpenSSH.
type ForwardRule struct {
	// Kind is "local", "remote" (-R) or "dynamic" (-D).
	Kind string
	// Bind is the local address; empty means forward.bind.
	Bind string
	// Local is the local port: the listening one for local and dynamic
	// forwards, the target for remote ones.
	Local uint16
	// RemoteHost is resolved on the server; empty means the server itself.
	RemoteHost string
	// Remote is the port on the server side; 0 for dynamic forwards.
	Remote uint16
	// Raw is the entry as written.
	Raw string
}

// ParseForward parses one forward spec:
//
//	PORT                  local PORT to PORT on the server
//	LOCAL:PORT            local LOCAL to PORT on the server
//	LOCAL:HOST:PORT       local LOCAL to HOST:PORT, as the server sees it
//	BIND:LOCAL:HOST:PORT  the same, listening on BIND
//	-R PORT[:LOCAL]       PORT on the server to local LOCAL (default PORT)
//	-D PORT               a SOCKS proxy on local PORT
//
// IPv6 addresses go in brackets.
func ParseForward(spec string) (ForwardRule, error) {
	bad := fmt.Errorf("invalid forward %q: use PORT, LOCAL:PORT, LOCAL:HOST:PORT, BIND:LOCAL:HOST:PORT, \"-R PORT[:LOCAL]\" or \"-D PORT\"", spec)
	s := strings.TrimSpace(spec)
	port := func(p string) (uint16, bool) {
		n, err := strconv.ParseUint(p, 10, 16)
		return uint16(n), err == nil && n > 0
	}
	host := func(h string) (string, bool) {
		if strings.HasPrefix(h, "[") {
			if !strings.HasSuffix(h, "]") {
				return "", false
			}
			h = h[1 : len(h)-1]
		}
		return h, h != "" && !strings.ContainsAny(h, " \t[]")
	}
	r := ForwardRule{Kind: "local", Raw: spec}
	if rest, ok := strings.CutPrefix(s, "-D "); ok {
		p, ok := port(strings.TrimSpace(rest))
		if !ok {
			return ForwardRule{}, bad
		}
		r.Kind, r.Local = "dynamic", p
		return r, nil
	}
	if rest, ok := strings.CutPrefix(s, "-R "); ok {
		parts := strings.Split(strings.TrimSpace(rest), ":")
		if len(parts) > 2 {
			return ForwardRule{}, bad
		}
		remote, ok := port(parts[0])
		if !ok {
			return ForwardRule{}, bad
		}
		local := remote
		if len(parts) == 2 {
			if local, ok = port(parts[1]); !ok {
				return ForwardRule{}, bad
			}
		}
		r.Kind, r.Remote, r.Local = "remote", remote, local
		return r, nil
	}
	parts := splitOutsideBrackets(s)
	var ok1, ok2, ok3, ok4 bool
	switch len(parts) {
	case 1:
		r.Remote, ok1 = port(parts[0])
		r.Local, ok2, ok3, ok4 = r.Remote, true, true, true
	case 2:
		r.Local, ok1 = port(parts[0])
		r.Remote, ok2 = port(parts[1])
		ok3, ok4 = true, true
	case 3:
		r.Local, ok1 = port(parts[0])
		r.RemoteHost, ok2 = host(parts[1])
		r.Remote, ok3 = port(parts[2])
		ok4 = true
	case 4:
		r.Bind, ok1 = host(parts[0])
		r.Local, ok2 = port(parts[1])
		r.RemoteHost, ok3 = host(parts[2])
		r.Remote, ok4 = port(parts[3])
	}
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return ForwardRule{}, bad
	}
	return r, nil
}

// ParseSSHForward parses the value of an ssh config's LocalForward,
// RemoteForward or DynamicForward (kind, lowercase, as ssh -G prints it):
// "[bind:]port host:hostport", or "[bind:]port" for DynamicForward. A
// RemoteForward's target is on this machine. Unix-socket forwards are not
// supported.
func ParseSSHForward(kind, value string) (ForwardRule, error) {
	bad := fmt.Errorf("unsupported %s %q: pfwd takes [bind:]port host:port, TCP only", kind, value)
	f := strings.Fields(value)
	if strings.Contains(value, "/") {
		return ForwardRule{}, bad
	}
	listen := func(s string) (string, uint16, bool) {
		parts := splitOutsideBrackets(s)
		switch len(parts) {
		case 1:
			p, ok := parsePort(parts[0])
			return "", p, ok
		case 2:
			h, hok := parseHost(parts[0])
			if h == "*" {
				h = "0.0.0.0"
			}
			p, pok := parsePort(parts[1])
			return h, p, hok && pok
		}
		return "", 0, false
	}
	target := func(s string) (string, uint16, bool) {
		parts := splitOutsideBrackets(s)
		if len(parts) != 2 {
			return "", 0, false
		}
		h, hok := parseHost(parts[0])
		p, pok := parsePort(parts[1])
		return h, p, hok && pok
	}
	r := ForwardRule{Raw: value}
	switch kind {
	case "localforward", "remoteforward":
		if len(f) != 2 {
			return ForwardRule{}, bad
		}
		bind, lport, ok1 := listen(f[0])
		host, tport, ok2 := target(f[1])
		if !ok1 || !ok2 {
			return ForwardRule{}, bad
		}
		r.Bind, r.RemoteHost = bind, host
		if kind == "localforward" {
			r.Kind, r.Local, r.Remote = "local", lport, tport
		} else {
			r.Kind, r.Remote, r.Local = "remote", lport, tport
		}
	case "dynamicforward":
		if len(f) != 1 {
			return ForwardRule{}, bad
		}
		bind, port, ok := listen(f[0])
		if !ok {
			return ForwardRule{}, bad
		}
		r.Kind, r.Bind, r.Local = "dynamic", bind, port
	default:
		return ForwardRule{}, bad
	}
	return r, nil
}

// parsePort parses a port from 1 to 65535.
func parsePort(p string) (uint16, bool) {
	n, err := strconv.ParseUint(p, 10, 16)
	return uint16(n), err == nil && n > 0
}

// parseHost takes a host, removing the brackets around an IPv6 address.
func parseHost(h string) (string, bool) {
	if strings.HasPrefix(h, "[") {
		if !strings.HasSuffix(h, "]") {
			return "", false
		}
		h = h[1 : len(h)-1]
	}
	return h, h != "" && !strings.ContainsAny(h, " \t[]")
}

// splitOutsideBrackets splits on ':' except inside [...].
func splitOutsideBrackets(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ':':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}
