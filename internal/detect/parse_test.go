package detect

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func open(t testing.TB, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func rp(port uint16, addr string, pid int, process string) RemotePort {
	return RemotePort{Port: port, Addrs: []string{addr}, PID: pid, Process: process}
}

func TestParseSS(t *testing.T) {
	alpine := []RemotePort{
		rp(8123, "0.0.0.0", 10, "python3"),
		rp(5173, "127.0.0.1", 11, "python3"),
		rp(8000, "*", 12, "python3"),
		rp(6006, "::1", 13, "python3"),
	}
	cases := map[string][]RemotePort{
		"ss-alpine-root.txt":     alpine,
		"ss-alpine-noheader.txt": alpine,
		"ss-alpine-user.txt": {
			rp(8123, "0.0.0.0", 0, ""),
			rp(3000, "127.0.0.1", 14, "python3"),
		},
		"ss-debian.txt": {
			rp(8123, "0.0.0.0", 3235, "python3"),
			rp(5173, "127.0.0.1", 3236, "python3"),
			rp(6006, "::1", 3238, "python3"),
			rp(8000, "*", 3237, "python3"),
		},
		"ss-ubuntu-resolved.txt": {
			rp(53, "127.0.0.53", 0, ""),
			rp(53, "127.0.0.54", 0, ""),
			rp(22, "0.0.0.0", 0, ""),
			rp(5173, "127.0.0.1", 4242, "node"),
			rp(22, "::", 0, ""),
			rp(9000, "127.0.0.1", 777, "java"),
		},
		"ss-multi-users.txt": {
			rp(80, "0.0.0.0", 1235, "nginx"),
			rp(80, "::", 1235, "nginx"),
		},
		"ss-old-format.txt": {
			rp(22, "*", 1158, "sshd"),
			rp(25, "127.0.0.1", 1390, "master"),
			rp(22, "::", 1158, "sshd"),
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseSS(open(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ParseSS =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestParseSSSkipsMalformedLines(t *testing.T) {
	in := strings.Join([]string{
		"garbage",
		"LISTEN 0 5 0.0.0.0:* 0.0.0.0:*",              // no port
		"LISTEN 0 5 0.0.0.0:0 0.0.0.0:*",              // port 0
		"LISTEN 0 5 0.0.0.0:70000 0.0.0.0:*",          // port out of range
		"LISTEN 0 5 nothost:80 0.0.0.0:*",             // not an IP
		"LISTEN 0 5 127.0.0.1:8080 0.0.0.0:* users:(", // truncated users list
		"\x00\xff\xfe",
		"",
	}, "\n")
	got, err := ParseSS(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []RemotePort{rp(8080, "127.0.0.1", 0, "")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSS = %+v, want %+v", got, want)
	}
}

func TestParseSSProcessNameWithSpace(t *testing.T) {
	got, err := ParseSS(strings.NewReader(`LISTEN 0 5 127.0.0.1:9222 0.0.0.0:* users:(("Web Content",pid=99,fd=4))`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []RemotePort{rp(9222, "127.0.0.1", 99, "Web Content")}; !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSS = %+v, want %+v", got, want)
	}
}

func TestParseProcNetTCP(t *testing.T) {
	cases := map[string][]RemotePort{
		"proc-alpine.txt": {
			rp(8123, "0.0.0.0", 0, ""),
			rp(5173, "127.0.0.1", 0, ""),
			rp(8000, "::", 0, ""),
			rp(6006, "::1", 0, ""),
		},
		"proc-debian.txt": {
			rp(8123, "0.0.0.0", 0, ""),
			rp(5173, "127.0.0.1", 0, ""),
			rp(6006, "::1", 0, ""),
			rp(8000, "::", 0, ""),
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseProcNetTCP(open(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ParseProcNetTCP =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestParseProcNetTCPMappedAndMalformed(t *testing.T) {
	in := strings.Join([]string{
		// ::ffff:127.0.0.1 port 9000, as a dual-stack socket shows it.
		"   0: 0000000000000000FFFF00000100007F:2328 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0 100 0 0 10 0",
		"   1: 0100007F 00000000:0000 0A",                 // no port
		"   2: 0100007G:1F90 00000000:0000 0A",            // bad hex
		"   3: 01007F:1F90 00000000:0000 0A",              // wrong length
		"   4: 0100007F:0000 00000000:0000 0A",            // port 0
		"   5: 0100007F:1F90 00000000:0000 01",            // established
		"   6: 0100007F:1F90",                             // short
		"   7: 0100007F:1F90 00000000:0000 0A extra cols", // listening 8080
	}, "\n")
	got, err := ParseProcNetTCP(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []RemotePort{rp(9000, "127.0.0.1", 0, ""), rp(8080, "127.0.0.1", 0, "")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseProcNetTCP = %+v, want %+v", got, want)
	}
}

func TestMerge(t *testing.T) {
	got := Merge([]RemotePort{
		rp(8000, "::", 0, ""),
		rp(53, "127.0.0.54", 0, ""),
		rp(8000, "0.0.0.0", 12, "python3"),
		rp(53, "127.0.0.53", 0, ""),
		rp(8000, "0.0.0.0", 13, "other"),
	})
	want := []RemotePort{
		{Port: 53, Addrs: []string{"127.0.0.53", "127.0.0.54"}},
		{Port: 8000, Addrs: []string{"0.0.0.0", "::"}, PID: 12, Process: "python3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Merge =\n%+v\nwant\n%+v", got, want)
	}
	if got := Merge(nil); len(got) != 0 {
		t.Errorf("Merge(nil) = %+v", got)
	}
}

func TestMergeDoesNotAliasInput(t *testing.T) {
	in := []RemotePort{rp(80, "0.0.0.0", 0, ""), rp(80, "::", 0, "")}
	out := Merge(in)
	out[0].Addrs[0] = "changed"
	if in[0].Addrs[0] != "0.0.0.0" {
		t.Error("Merge shares Addrs with its input")
	}
}

func TestTarget(t *testing.T) {
	cases := []struct {
		addrs []string
		want  string
	}{
		{[]string{"0.0.0.0"}, "127.0.0.1"},
		{[]string{"::"}, "127.0.0.1"},
		{[]string{"*"}, "127.0.0.1"},
		{[]string{"127.0.0.1"}, "127.0.0.1"},
		{[]string{"127.0.0.1", "::1"}, "127.0.0.1"},
		{[]string{"::1"}, "::1"},
		{[]string{"10.0.0.5"}, "10.0.0.5"},
		{[]string{"10.0.0.5", "::1"}, "::1"},
		{[]string{"127.0.0.53", "127.0.0.54"}, "127.0.0.53"},
		{nil, "127.0.0.1"},
	}
	for _, c := range cases {
		if got := Target(c.addrs); got != c.want {
			t.Errorf("Target(%q) = %q, want %q", c.addrs, got, c.want)
		}
	}
}

func FuzzParseSS(f *testing.F) {
	for _, name := range []string{"ss-alpine-root.txt", "ss-alpine-user.txt", "ss-debian.txt", "ss-ubuntu-resolved.txt", "ss-multi-users.txt", "ss-old-format.txt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(data))
	}
	f.Fuzz(func(t *testing.T, in string) {
		ports, err := ParseSS(strings.NewReader(in))
		if err != nil {
			return
		}
		for _, p := range ports {
			if p.Port == 0 || len(p.Addrs) != 1 {
				t.Fatalf("bad port %+v from %q", p, in)
			}
		}
		Merge(ports)
	})
}

func FuzzParseProcNetTCP(f *testing.F) {
	for _, name := range []string{"proc-alpine.txt", "proc-debian.txt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(data))
	}
	f.Fuzz(func(t *testing.T, in string) {
		ports, err := ParseProcNetTCP(strings.NewReader(in))
		if err != nil {
			return
		}
		for _, p := range ports {
			if p.Port == 0 || len(p.Addrs) != 1 {
				t.Fatalf("bad port %+v from %q", p, in)
			}
		}
	})
}
