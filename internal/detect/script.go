package detect

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Strategy is how the ports of a server are listed.
type Strategy int

// Detection strategies.
const (
	// None means no supported tool was found; manual forwards still work.
	None Strategy = iota
	// SS runs `ss -tlnp`, which also names the owning processes.
	SS
	// Proc reads /proc/net/tcp and /proc/net/tcp6, for busybox systems.
	Proc
	// Netstat runs netstat: GNU or busybox on Linux, the BSD one on macOS
	// and the BSDs.
	Netstat
	// Lsof runs `lsof -F` on macOS.
	Lsof
	// Sockstat runs `sockstat -l` on FreeBSD.
	Sockstat
	// Windows runs netstat -ano and tasklist through PowerShell, on a
	// Windows OpenSSH server.
	Windows
)

// String returns the strategy as shown to the user.
func (s Strategy) String() string {
	switch s {
	case SS:
		return "ss"
	case Proc:
		return "/proc/net/tcp"
	case Netstat:
		return "netstat"
	case Lsof:
		return "lsof"
	case Sockstat:
		return "sockstat"
	case Windows:
		return "netstat (Windows)"
	default:
		return "none"
	}
}

// Parser returns the parser for the strategy's output.
func (s Strategy) Parser() func(io.Reader) ([]RemotePort, error) {
	switch s {
	case SS:
		return ParseSS
	case Proc:
		return ParseProcNetTCP
	case Netstat:
		return ParseNetstat
	case Lsof:
		return ParseLsof
	case Sockstat:
		return ParseSockstat
	case Windows:
		return ParseWindows
	}
	return nil
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

// WindowsProbe identifies a Windows OpenSSH server, where ProbeScript fails
// because there is no sh. It runs under cmd.exe or PowerShell alike.
const WindowsProbe = "cmd /c ver"

// Probe is the parsed output of ProbeScript (or WindowsProbe).
type Probe struct {
	// OS is the output of uname -s, like "Linux"; empty when unknown.
	OS                                string
	SS, Netstat, Lsof, Sockstat, Proc bool
}

// ParseProbe parses the output of ProbeScript, or of WindowsProbe.
func ParseProbe(out string) Probe {
	var p Probe
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Microsoft Windows") && p.OS == "" {
			p.OS = "Windows"
			continue
		}
		switch line {
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

// Strategy picks how to list ports: on Linux ss, then /proc, then
// netstat; on macOS lsof, then netstat; on the BSDs sockstat, then netstat;
// and the Windows loop on Windows.
func (p Probe) Strategy() Strategy {
	switch p.OS {
	case "Linux":
		switch {
		case p.SS:
			return SS
		case p.Proc:
			return Proc
		case p.Netstat:
			return Netstat
		}
	case "Darwin":
		switch {
		case p.Lsof:
			return Lsof
		case p.Netstat:
			return Netstat
		}
	case "FreeBSD", "OpenBSD", "NetBSD", "DragonFly":
		switch {
		case p.Sockstat && p.OS == "FreeBSD":
			return Sockstat
		case p.Netstat:
			return Netstat
		}
	case "Windows":
		return Windows
	}
	return None
}

// minInterval is the shortest polling interval allowed.
const minInterval = 500 * time.Millisecond

// LoopScript returns the shell loop that prints a framed snapshot every
// interval until the channel closes (printf then fails and the loop exits).
// Only the interval varies, and it is a number formatted here. os is the
// probe's OS, which picks netstat's flags.
func LoopScript(s Strategy, os string, interval time.Duration) string {
	var list string
	switch s {
	case SS:
		list = "ss -tlnp 2>/dev/null"
	case Proc:
		list = "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null"
	case Lsof:
		list = "lsof -nP -iTCP -sTCP:LISTEN -F pctn 2>/dev/null"
	case Sockstat:
		list = "sockstat -46 -l -P tcp 2>/dev/null"
	case Netstat:
		switch os {
		case "Linux":
			list = "{ netstat -tlnp 2>/dev/null || netstat -tln 2>/dev/null; }"
		case "Darwin":
			list = "netstat -anv -p tcp 2>/dev/null"
		default:
			list = "netstat -an -p tcp 2>/dev/null"
		}
	default:
		return ""
	}
	interval = max(interval, minInterval)
	secs := strconv.FormatFloat(interval.Seconds(), 'f', -1, 64)
	return `while :; do printf "\036PFWD\n" || exit; ` + list + `; printf "\036PFWD END\n" || exit; sleep ` + secs + "; done"
}

// windowsLoop is the PowerShell loop for Windows servers; %d is the
// interval in milliseconds. It writes the frame markers with \n endings
// itself, and exits when the channel is gone and writing fails.
const windowsLoop = `$ErrorActionPreference = 'SilentlyContinue'
$rs = [char]30
while ($true) {
  try { [Console]::Out.Write($rs + "PFWD` + "`" + `n"); [Console]::Out.Flush() } catch { exit }
  netstat -ano -p TCP
  netstat -ano -p TCPv6
  '` + windowsTasklistMarker + `'
  tasklist /fo csv /nh
  try { [Console]::Out.Write($rs + "PFWD END` + "`" + `n"); [Console]::Out.Flush() } catch { exit }
  Start-Sleep -Milliseconds %d
}`

// LoopCommand returns the exec command for the strategy's snapshot loop:
// the sh loop of LoopScript, or PowerShell with an encoded script on
// Windows. "" means the strategy has no loop.
func LoopCommand(s Strategy, os string, interval time.Duration) string {
	if s == Windows {
		ms := max(interval, minInterval).Milliseconds()
		return "powershell -NoProfile -NonInteractive -EncodedCommand " + encodePowerShell(fmt.Sprintf(windowsLoop, ms))
	}
	script := LoopScript(s, os, interval)
	if script == "" {
		return ""
	}
	return ShellCommand(script)
}

// encodePowerShell encodes a script for -EncodedCommand: base64 of its
// UTF-16LE bytes. The script is fixed, so no quoting is involved.
func encodePowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return base64.StdEncoding.EncodeToString(b)
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
