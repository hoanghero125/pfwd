package detect

import (
	"strconv"
	"strings"
	"time"
)

// Strategy is how the ports of a server are listed.
type Strategy int

// Detection strategies, in the order they are preferred on Linux.
const (
	// None means no supported tool was found; manual forwards still work.
	None Strategy = iota
	// SS runs `ss -tlnp`, which also names the owning processes.
	SS
	// Proc reads /proc/net/tcp and /proc/net/tcp6, for busybox systems.
	Proc
)

// String returns the strategy as shown to the user.
func (s Strategy) String() string {
	switch s {
	case SS:
		return "ss"
	case Proc:
		return "/proc/net/tcp"
	default:
		return "none"
	}
}

// ProbeScript finds out what the server is and which tools it has. It runs
// once per connection. The OS line is tagged, because shell startup files
// can print text before it. `ss -tln` is run rather than looked up, because ss
// can exist and still fail where netlink is blocked. It ends with `true` so
// the exit status is 0 whatever the last test says.
const ProbeScript = "echo \"PFWD_OS $(uname -s 2>/dev/null)\"; " +
	"ss -tln >/dev/null 2>&1 && echo HAVE_SS; " +
	"command -v netstat >/dev/null 2>&1 && echo HAVE_NETSTAT; " +
	"command -v lsof >/dev/null 2>&1 && echo HAVE_LSOF; " +
	"command -v sockstat >/dev/null 2>&1 && echo HAVE_SOCKSTAT; " +
	"test -r /proc/net/tcp && echo HAVE_PROC; " +
	"true"

// Probe is the parsed output of ProbeScript.
type Probe struct {
	// OS is the output of uname -s, like "Linux"; empty when unknown.
	OS                                string
	SS, Netstat, Lsof, Sockstat, Proc bool
}

// ParseProbe parses the output of ProbeScript.
func ParseProbe(out string) Probe {
	var p Probe
	for _, line := range strings.Split(out, "\n") {
		switch line = strings.TrimSpace(line); line {
		case "":
		case "HAVE_SS":
			p.SS = true
		case "HAVE_NETSTAT":
			p.Netstat = true
		case "HAVE_LSOF":
			p.Lsof = true
		case "HAVE_SOCKSTAT":
			p.Sockstat = true
		case "HAVE_PROC":
			p.Proc = true
		default:
			if os, ok := strings.CutPrefix(line, "PFWD_OS"); ok && p.OS == "" {
				p.OS = strings.TrimSpace(os)
			}
		}
	}
	return p
}

// Strategy picks how to list ports. Only Linux is supported for now;
// macOS, BSD and Windows servers arrive in v0.4.0.
func (p Probe) Strategy() Strategy {
	if p.OS != "Linux" {
		return None
	}
	switch {
	case p.SS:
		return SS
	case p.Proc:
		return Proc
	}
	return None
}

// minInterval is the shortest polling interval allowed.
const minInterval = 500 * time.Millisecond

// LoopScript returns the shell loop that prints a framed snapshot every
// interval until the channel closes (printf then fails and the loop exits).
// Only the interval varies, and it is a number formatted here.
func LoopScript(s Strategy, interval time.Duration) string {
	var list string
	switch s {
	case SS:
		list = "ss -tlnp 2>/dev/null"
	case Proc:
		list = "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null"
	default:
		return ""
	}
	interval = max(interval, minInterval)
	secs := strconv.FormatFloat(interval.Seconds(), 'f', -1, 64)
	return `while :; do printf "\036PFWD\n" || exit; ` + list + `; printf "\036PFWD END\n" || exit; sleep ` + secs + "; done"
}

// ShellCommand wraps a script for the exec request. The server runs exec
// commands through the user's login shell, which may be fish or csh, so
// scripts always run under sh.
func ShellCommand(script string) string {
	return "sh -c " + shQuote(script)
}

// shQuote quotes s for a POSIX shell: it wraps s in single quotes and
// replaces each embedded single quote with: close quote, backslash-escaped
// quote, reopen quote.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
