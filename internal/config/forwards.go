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
