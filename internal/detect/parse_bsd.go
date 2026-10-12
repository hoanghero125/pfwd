package detect

import (
	"bufio"
	"encoding/csv"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// newScanner returns a line scanner that allows long lines.
func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return sc
}

// splitHostPort splits a local address at its last ':' or '.', which is how
// the BSD tools write host and port (127.0.0.1.631, *.22, ::1.9002).
func splitHostPort(s string) (string, uint16, bool) {
	i := strings.LastIndexAny(s, ":.")
	if i <= 0 {
		return "", 0, false
	}
	port, err := strconv.ParseUint(s[i+1:], 10, 16)
	if err != nil || port == 0 {
		return "", 0, false
	}
	return s[:i], uint16(port), true
}

// wildcard turns "*" into the wildcard of the socket's family: 0.0.0.0 for
// IPv4, :: for IPv6, and "*" (dual-stack) when the family is unknown.
func wildcard(family string) string {
	switch {
	case strings.HasSuffix(family, "46"):
		return "*"
	case strings.HasSuffix(family, "6"):
		return "::"
	case strings.HasSuffix(family, "4"), family == "tcp":
		return "0.0.0.0"
	}
	return "*"
}

// listenAddr normalizes a listening address; "*" takes the family's
// wildcard.
func listenAddr(host, family string) (string, bool) {
	if host == "*" {
		return wildcard(family), true
	}
	return normalizeAddr(host)
}

// ParseLsof parses `lsof -nP -iTCP -sTCP:LISTEN -F pctn` output: p (PID),
// c (command), t (IPv4 or IPv6) and n (address) lines. Connections (a name
// with "->") are skipped.
func ParseLsof(r io.Reader) ([]RemotePort, error) {
	var out []RemotePort
	var pid int
	var command, family string
	sc := newScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		val := line[1:]
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(val)
			command, family = "", ""
		case 'c':
			command = val
		case 'f':
			family = ""
		case 't':
			family = strings.ToLower(strings.TrimPrefix(val, "IP"))
		case 'n':
			if strings.Contains(val, "->") {
				continue
			}
			host, port, ok := splitHostPort(val)
			if !ok {
				continue
			}
			addr, ok := listenAddr(host, map[string]string{"v4": "tcp4", "v6": "tcp6"}[family])
			if !ok {
				continue
			}
			out = append(out, RemotePort{Port: port, Addrs: []string{addr}, PID: pid, Process: command})
		}
	}
	return out, sc.Err()
}

// netstatPID matches GNU and busybox "PID/Program" and macOS
// "process:pid" columns.
var (
	netstatSlash = regexp.MustCompile(`^(\d+)/(.+)$`)
	netstatColon = regexp.MustCompile(`^(.+):(\d+)$`)
)

// ParseNetstat parses netstat's TCP listening sockets in the GNU and busybox
// format (`netstat -tln[p]`: 0.0.0.0:22, :::22, PID/Program) and the BSD
// and macOS one (`netstat -an[v] -p tcp`: *.22, 127.0.0.1.631, and macOS's
// process:pid). Lines that aren't listening TCP sockets are skipped.
func ParseNetstat(r io.Reader) ([]RemotePort, error) {
	var out []RemotePort
	sc := newScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(strings.ToLower(f[0]), "tcp") {
			continue
		}
		listen := -1
		for i, x := range f {
			if x == "LISTEN" {
				listen = i
				break
			}
		}
		if listen < 0 {
			continue
		}
		host, port, ok := splitHostPort(f[3])
		if !ok {
			continue
		}
		family := strings.ToLower(f[0])
		if family == "tcp" && strings.Contains(host, ":") {
			family = "tcp6"
		}
		addr, ok := listenAddr(host, family)
		if !ok {
			continue
		}
		p := RemotePort{Port: port, Addrs: []string{addr}}
		for _, x := range f[listen+1:] {
			if m := netstatSlash.FindStringSubmatch(x); m != nil {
				p.PID, _ = strconv.Atoi(m[1])
				p.Process = m[2]
				break
			}
			if m := netstatColon.FindStringSubmatch(x); m != nil {
				p.PID, _ = strconv.Atoi(m[2])
				p.Process = m[1]
				break
			}
		}
		out = append(out, p)
	}
	return out, sc.Err()
}

// ParseSockstat parses `sockstat -46 -l -P tcp` output (FreeBSD): USER,
// COMMAND, PID, FD, PROTO, LOCAL ADDRESS, FOREIGN ADDRESS. Other users'
// sockets show "?" for the command and PID.
func ParseSockstat(r io.Reader) ([]RemotePort, error) {
	var out []RemotePort
	sc := newScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 7 || !strings.HasPrefix(f[4], "tcp") {
			continue
		}
		host, port, ok := splitHostPort(f[5])
		if !ok {
			continue
		}
		addr, ok := listenAddr(host, f[4])
		if !ok {
			continue
		}
		p := RemotePort{Port: port, Addrs: []string{addr}}
		if pid, err := strconv.Atoi(f[2]); err == nil {
			p.PID = pid
		}
		if f[1] != "?" {
			p.Process = f[1]
		}
		out = append(out, p)
	}
	return out, sc.Err()
}

// windowsTasklistMarker separates netstat's output from tasklist's in a
// Windows frame.
const windowsTasklistMarker = "PFWD-TASKLIST"

// ParseWindows parses a Windows frame: `netstat -ano -p TCP` and
// `-p TCPv6` output, then windowsTasklistMarker, then `tasklist /fo csv
// /nh` to name the PIDs. A listening socket is one whose foreign address is
// 0.0.0.0:0 or [::]:0, which holds whatever language Windows prints its
// states in. ".exe" is dropped from process names.
func ParseWindows(r io.Reader) ([]RemotePort, error) {
	var out []RemotePort
	names := map[int]string{}
	tasklist := false
	sc := newScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == windowsTasklistMarker:
			tasklist = true
		case tasklist:
			rec, err := csv.NewReader(strings.NewReader(line)).Read()
			if err != nil || len(rec) < 2 {
				continue
			}
			if pid, err := strconv.Atoi(rec[1]); err == nil {
				name := rec[0]
				if strings.HasSuffix(strings.ToLower(name), ".exe") {
					name = name[:len(name)-4]
				}
				names[pid] = name
			}
		default:
			f := strings.Fields(line)
			if len(f) != 5 || f[0] != "TCP" || (f[2] != "0.0.0.0:0" && f[2] != "[::]:0") {
				continue
			}
			i := strings.LastIndexByte(f[1], ':')
			if i <= 0 {
				continue
			}
			port, err := strconv.ParseUint(f[1][i+1:], 10, 16)
			if err != nil || port == 0 {
				continue
			}
			addr, ok := normalizeAddr(f[1][:i])
			if !ok {
				continue
			}
			p := RemotePort{Port: uint16(port), Addrs: []string{addr}}
			p.PID, _ = strconv.Atoi(f[4])
			out = append(out, p)
		}
	}
	for i := range out {
		out[i].Process = names[out[i].PID]
	}
	return out, sc.Err()
}
