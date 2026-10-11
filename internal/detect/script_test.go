package detect

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseProbe(t *testing.T) {
	cases := []struct {
		out      string
		want     Probe
		strategy Strategy
	}{
		{"PFWD_OS Linux\nHAVE_SS\nHAVE_NETSTAT\nHAVE_PROC\n", Probe{OS: "Linux", SS: true, Netstat: true, Proc: true}, SS},
		{"PFWD_OS Linux\nHAVE_NETSTAT\nHAVE_PROC\n", Probe{OS: "Linux", Netstat: true, Proc: true}, Proc},
		{"PFWD_OS Linux\r\nHAVE_PROC\r\n", Probe{OS: "Linux", Proc: true}, Proc},
		{"PFWD_OS Linux\n", Probe{OS: "Linux"}, None},
		{"PFWD_OS Darwin\nHAVE_NETSTAT\nHAVE_LSOF\n", Probe{OS: "Darwin", Netstat: true, Lsof: true}, None},
		{"PFWD_OS FreeBSD\nHAVE_SOCKSTAT\n", Probe{OS: "FreeBSD", Sockstat: true}, None},
		{"HAVE_SS\n", Probe{SS: true}, None},
		// Output from shell startup files must not become the OS name.
		{"Welcome to devbox!\nPFWD_OS Linux\nHAVE_SS\n", Probe{OS: "Linux", SS: true}, SS},
		{"Last login: today\nHAVE_PROC\nPFWD_OS \n", Probe{Proc: true}, None},
		{"", Probe{}, None},
	}
	for _, c := range cases {
		got := ParseProbe(c.out)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseProbe(%q) = %+v, want %+v", c.out, got, c.want)
		}
		if s := got.Strategy(); s != c.strategy {
			t.Errorf("ParseProbe(%q).Strategy() = %v, want %v", c.out, s, c.strategy)
		}
	}
}

func TestStrategyString(t *testing.T) {
	for s, want := range map[Strategy]string{None: "none", SS: "ss", Proc: "/proc/net/tcp"} {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}

func TestProbeScript(t *testing.T) {
	for _, want := range []string{"PFWD_OS", "uname -s", "ss -tln", "HAVE_SS", "command -v netstat", "command -v lsof", "command -v sockstat", "test -r /proc/net/tcp", "HAVE_PROC"} {
		if !strings.Contains(ProbeScript, want) {
			t.Errorf("ProbeScript lacks %q", want)
		}
	}
	if !strings.HasSuffix(ProbeScript, "true") {
		t.Error("ProbeScript must exit 0 even when the last test fails")
	}
}

func TestLoopScript(t *testing.T) {
	ss := LoopScript(SS, 2*time.Second)
	for _, want := range []string{"while :", "ss -tlnp", `printf "\036PFWD\n"`, `printf "\036PFWD END\n"`, "sleep 2;", "|| exit"} {
		if !strings.Contains(ss, want) {
			t.Errorf("LoopScript(SS) = %q, lacks %q", ss, want)
		}
	}
	proc := LoopScript(Proc, 1500*time.Millisecond)
	if !strings.Contains(proc, "cat /proc/net/tcp /proc/net/tcp6") || !strings.Contains(proc, "sleep 1.5;") {
		t.Errorf("LoopScript(Proc) = %q", proc)
	}
	if fast := LoopScript(SS, 100*time.Millisecond); !strings.Contains(fast, "sleep 0.5;") {
		t.Errorf("interval below 500ms not clamped: %q", fast)
	}
	if LoopScript(None, time.Second) != "" {
		t.Error("LoopScript(None) should be empty")
	}
}

func TestScriptsHaveNoSingleQuotes(t *testing.T) {
	// ShellCommand wraps scripts in single quotes; a quote inside would
	// end the quoting early.
	for _, s := range []string{ProbeScript, LoopScript(SS, 2*time.Second), LoopScript(Proc, 2*time.Second)} {
		if strings.Contains(s, "'") {
			t.Errorf("script contains a single quote: %q", s)
		}
	}
}

func TestShQuote(t *testing.T) {
	cases := map[string]string{
		"a":            "'a'",
		"":             "''",
		"it's":         `'it'\''s'`,
		"a b;$(rm -r)": "'a b;$(rm -r)'",
		"''":           `''\'''\'''`,
	}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShellCommand(t *testing.T) {
	if got, want := ShellCommand("echo hi"), "sh -c 'echo hi'"; got != want {
		t.Errorf("ShellCommand = %q, want %q", got, want)
	}
}
