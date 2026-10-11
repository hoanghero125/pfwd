package detect

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ssUser matches the first process in an ss users:(...) list, in the
// current format ("node",pid=42,fd=3) and the old one ("sshd",1158,3).
var ssUser = regexp.MustCompile(`\("([^"]*)",(?:pid=)?(\d+)`)

// ParseSS parses `ss -tlnp` output, with or without the header line. It
// returns one entry per listening socket and skips lines it can't parse.
func ParseSS(r io.Reader) ([]RemotePort, error) {
	var out []RemotePort
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if p, ok := parseSSLine(sc.Text()); ok {
			out = append(out, p)
		}
	}
	return out, sc.Err()
}

func parseSSLine(line string) (RemotePort, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return RemotePort{}, false
	}
	switch fields[0] {
	case "State", "Recv-Q", "Netid":
		return RemotePort{}, false
	}
	// The local address is the first host:port field; the columns before
	// it (state, queues) contain no colon.
	var p RemotePort
	found := false
	for _, f := range fields[1:] {
		i := strings.LastIndexByte(f, ':')
		if i < 0 {
			continue
		}
		port, err := strconv.ParseUint(f[i+1:], 10, 16)
		if err != nil || port == 0 {
			return RemotePort{}, false
		}
		addr, ok := normalizeAddr(f[:i])
		if !ok {
			return RemotePort{}, false
		}
		p = RemotePort{Port: uint16(port), Addrs: []string{addr}}
		found = true
		break
	}
	if !found {
		return RemotePort{}, false
	}
	if i := strings.Index(line, "users:("); i >= 0 {
		if m := ssUser.FindStringSubmatch(line[i:]); m != nil {
			p.Process = m[1]
			p.PID, _ = strconv.Atoi(m[2])
		}
	}
	return p, true
}
