package detect

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestParseLsof(t *testing.T) {
	got, err := ParseLsof(open(t, "lsof-macos.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := []RemotePort{
		rp(9001, "127.0.0.1", 1052, "nc"),
		rp(9002, "::1", 1053, "Python"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLsof =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseLsofTypes(t *testing.T) {
	in := "p10\ncnode\nf21\ntIPv4\nn*:3000\nf22\ntIPv6\nn*:3000\nf23\ntIPv6\nn[::]:4000\nf24\ntIPv4\nn10.0.0.5:5432->10.0.0.9:1234\n"
	got, err := ParseLsof(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []RemotePort{rp(3000, "0.0.0.0", 10, "node"), rp(3000, "::", 10, "node"), rp(4000, "::", 10, "node")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLsof =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseNetstat(t *testing.T) {
	cases := map[string][]RemotePort{
		"netstat-macos.txt": {
			rp(9002, "::1", 1053, "Python"),
			rp(9001, "127.0.0.1", 1052, "nc"),
			rp(22, "0.0.0.0", 1, "launchd"),
			rp(22, "::", 1, "launchd"),
		},
		"netstat-gnu.txt": {
			rp(53, "127.0.0.53", 395, "systemd-resolve"),
			rp(22, "0.0.0.0", 1, "init"),
			rp(8123, "0.0.0.0", 1974, "python3"),
			rp(53, "127.0.0.54", 395, "systemd-resolve"),
			rp(22, "::", 1, "init"),
		},
		"netstat-busybox.txt": {
			rp(8125, "127.0.0.1", 0, ""),
			rp(8124, "::", 0, ""),
		},
		"netstat-busybox-p.txt": {
			rp(8125, "127.0.0.1", 8, "nc"),
			rp(8124, "::", 7, "nc"),
		},
		"netstat-openbsd.txt": {
			rp(3000, "127.0.0.1", 0, ""),
			rp(22, "0.0.0.0", 0, ""),
			rp(8888, "::1", 0, ""),
			rp(22, "::", 0, ""),
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseNetstat(open(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ParseNetstat =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestParseSockstat(t *testing.T) {
	got, err := ParseSockstat(open(t, "sockstat-freebsd.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := []RemotePort{
		rp(80, "0.0.0.0", 1204, "nginx"),
		rp(80, "::", 1204, "nginx"),
		rp(3000, "127.0.0.1", 2210, "node"),
		rp(8888, "::1", 2301, "python3.11"),
		rp(22, "::", 812, "sshd"),
		rp(22, "0.0.0.0", 812, "sshd"),
		rp(5432, "10.0.0.5", 0, ""),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSockstat =\n%+v\nwant\n%+v", got, want)
	}
}

// windowsFrame is what the Windows loop prints between frame markers.
func windowsFrame(t testing.TB) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, name := range []string{"netstat-windows.txt", "netstat6-windows.txt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	b.WriteString(windowsTasklistMarker + "\n")
	data, err := os.ReadFile(filepath.Join("testdata", "tasklist-windows.txt"))
	if err != nil {
		t.Fatal(err)
	}
	b.Write(data)
	return b.Bytes()
}

func TestParseWindows(t *testing.T) {
	got, err := ParseWindows(bytes.NewReader(windowsFrame(t)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []RemotePort{
		rp(22, "0.0.0.0", 4012, "sshd"),
		rp(8123, "0.0.0.0", 6420, "python"),
		rp(139, "10.1.0.76", 4, "System"),
		rp(22, "::", 4012, "sshd"),
	} {
		if !slices.ContainsFunc(got, func(p RemotePort) bool { return reflect.DeepEqual(p, want) }) {
			t.Errorf("ParseWindows lacks %+v", want)
		}
	}
	for _, p := range got {
		if p.Port == 49673 || p.Port == 49917 {
			t.Errorf("a TIME_WAIT connection was taken for a listener: %+v", p)
		}
	}
}

func TestParseWindowsLocalizedState(t *testing.T) {
	in := "  Proto  Lokale Adresse         Remoteadresse          Status           PID\n" +
		"  TCP    0.0.0.0:8080           0.0.0.0:0              ABHÖREN          77\n" +
		"  TCP    10.0.0.2:51000         20.0.0.1:443           HERGESTELLT      78\n"
	got, err := ParseWindows(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := []RemotePort{rp(8080, "0.0.0.0", 77, "")}; !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWindows = %+v, want %+v", got, want)
	}
}

// TestParseWindowsStateWithSpace: Italian Windows says IN ASCOLTO, two
// words, so the PID is the last field, not the fifth.
func TestParseWindowsStateWithSpace(t *testing.T) {
	in := "  Proto  Indirizzo locale       Indirizzo esterno      Stato           PID\n" +
		"  TCP    0.0.0.0:8080           0.0.0.0:0              IN ASCOLTO      77\n"
	got, err := ParseWindows(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := []RemotePort{rp(8080, "0.0.0.0", 77, "")}; !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWindows = %+v, want %+v", got, want)
	}
}

func TestProbeStrategies(t *testing.T) {
	cases := []struct {
		out  string
		want Strategy
	}{
		{"PFWD_OS Linux\nHAVE_SS\nHAVE_PROC\n", SS},
		{"PFWD_OS Linux\nHAVE_NETSTAT\n", Netstat},
		{"PFWD_OS Darwin\nHAVE_LSOF\nHAVE_NETSTAT\n", Lsof},
		{"PFWD_OS Darwin\nHAVE_NETSTAT\n", Netstat},
		{"PFWD_OS FreeBSD\nHAVE_SOCKSTAT\nHAVE_NETSTAT\n", Sockstat},
		{"PFWD_OS OpenBSD\nHAVE_NETSTAT\n", Netstat},
		{"\r\nMicrosoft Windows [Version 10.0.26100.33438]\r\n", Windows},
		{"PFWD_OS SunOS\nHAVE_NETSTAT\n", None},
	}
	for _, tc := range cases {
		p := ParseProbe(tc.out)
		if got := p.Strategy(); got != tc.want {
			t.Errorf("Strategy(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}

func TestLoopCommands(t *testing.T) {
	for _, tc := range []struct {
		s    Strategy
		os   string
		want string
	}{
		{Lsof, "Darwin", "lsof -nP -iTCP -sTCP:LISTEN -F pctn"},
		{Netstat, "Linux", "netstat -tln"},
		{Netstat, "Darwin", "netstat -anv -p tcp"},
		{Netstat, "OpenBSD", "netstat -an -p tcp"},
		{Sockstat, "FreeBSD", "sockstat -46 -l -P tcp"},
	} {
		cmd := LoopCommand(tc.s, tc.os, 0)
		if !strings.HasPrefix(cmd, "sh -c '") || !strings.Contains(cmd, tc.want) {
			t.Errorf("LoopCommand(%v, %s) = %q, want an sh loop running %q", tc.s, tc.os, cmd, tc.want)
		}
	}
	win := LoopCommand(Windows, "Windows", 2500e6)
	if !strings.HasPrefix(win, "powershell -NoProfile -NonInteractive -EncodedCommand ") {
		t.Fatalf("Windows loop = %q", win)
	}
	script := decodePowerShell(t, strings.TrimPrefix(win, "powershell -NoProfile -NonInteractive -EncodedCommand "))
	for _, want := range []string{"netstat -ano -p TCP", "netstat -ano -p TCPv6", windowsTasklistMarker, "tasklist /fo csv /nh", "Start-Sleep -Milliseconds 2500"} {
		if !strings.Contains(script, want) {
			t.Errorf("the Windows loop script lacks %q:\n%s", want, script)
		}
	}
}

func FuzzParseLsof(f *testing.F) { fuzzParser(f, ParseLsof, "lsof-macos.txt") }
func FuzzParseNetstat(f *testing.F) {
	fuzzParser(f, ParseNetstat, "netstat-macos.txt", "netstat-gnu.txt", "netstat-busybox-p.txt")
}
func FuzzParseSockstat(f *testing.F) { fuzzParser(f, ParseSockstat, "sockstat-freebsd.txt") }
func FuzzParseWindows(f *testing.F) {
	f.Add(windowsFrame(f))
	fuzzParser(f, ParseWindows)
}

// fuzzParser checks parse never panics and only returns usable ports.
func fuzzParser(f *testing.F, parse func(io.Reader) ([]RemotePort, error), seeds ...string) {
	for _, s := range seeds {
		data, err := os.ReadFile(filepath.Join("testdata", s))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte("tcp 0 0 *.:LISTEN\n:\n[::\nn*:\np\x00\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		ports, _ := parse(bytes.NewReader(data))
		for _, p := range ports {
			if p.Port == 0 || len(p.Addrs) != 1 {
				t.Fatalf("unusable port %+v from %q", p, data)
			}
		}
	})
}

// decodePowerShell undoes -EncodedCommand: base64 of UTF-16LE.
func decodePowerShell(t *testing.T, enc string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("bad encoded command %q: %v", enc, err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return string(utf16.Decode(u))
}

// windowsRunner is a Windows OpenSSH server: sh doesn't exist, cmd /c ver
// answers, and the loop prints one frame.
type windowsRunner struct {
	fakeRunner
	frame []byte
}

func (w *windowsRunner) Output(ctx context.Context, cmd string) ([]byte, error) {
	w.mu.Lock()
	w.cmds = append(w.cmds, cmd)
	w.mu.Unlock()
	if cmd == WindowsProbe {
		return []byte("\r\nMicrosoft Windows [Version 10.0.26100.33438]\r\n"), nil
	}
	return nil, errors.New("exit status 1")
}

func (w *windowsRunner) Start(ctx context.Context, cmd string) (io.ReadCloser, error) {
	w.mu.Lock()
	w.cmds = append(w.cmds, cmd)
	w.mu.Unlock()
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(frameBegin + "\r\n"))
		_, _ = pw.Write(w.frame)
		_, _ = pw.Write([]byte(frameEnd + "\r\n"))
	}()
	stop := context.AfterFunc(ctx, func() { pw.Close() })
	return readCloser{pr, func() error { stop(); return pw.Close() }}, nil
}

func TestWatchWindowsServer(t *testing.T) {
	r := &windowsRunner{frame: windowsFrame(t)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	got := make(chan Update, 1)
	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, r, WatchOptions{Interval: time.Second}, func(u Update) {
			select {
			case got <- u:
			default:
			}
		})
	}()
	select {
	case u := <-got:
		if u.Strategy != Windows || u.OS != "Windows" || len(u.Ports) == 0 {
			t.Errorf("update = strategy %v, OS %q, %d ports", u.Strategy, u.OS, len(u.Ports))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no update from a Windows server")
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Watch = %v", err)
	}
}
