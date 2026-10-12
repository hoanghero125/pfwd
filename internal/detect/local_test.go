package detect

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeLocal answers commands and file reads from the sample files.
type fakeLocal struct {
	outputs map[string]string // "name arg..." -> testdata file
	files   map[string]string // path -> testdata file
	ran     []string
}

func (f *fakeLocal) run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.ran = append(f.ran, cmd)
	sample, ok := f.outputs[cmd]
	if !ok {
		return nil, errors.New("executable file not found")
	}
	return os.ReadFile(filepath.Join("testdata", sample))
}

func (f *fakeLocal) read(path string) ([]byte, error) {
	sample, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join("testdata", sample))
}

func hasPort(ports []RemotePort, port uint16, process string) bool {
	return slices.ContainsFunc(ports, func(p RemotePort) bool { return p.Port == port && p.Process == process })
}

func TestLocalPortsPerOS(t *testing.T) {
	cases := []struct {
		goos    string
		fake    fakeLocal
		port    uint16
		process string
	}{
		{"darwin", fakeLocal{outputs: map[string]string{"lsof -nP -iTCP -sTCP:LISTEN -F pctn": "lsof-macos.txt"}}, 9001, "nc"},
		{"freebsd", fakeLocal{outputs: map[string]string{"sockstat -46 -l -P tcp": "sockstat-freebsd.txt"}}, 3000, "node"},
		{"openbsd", fakeLocal{outputs: map[string]string{"netstat -an -p tcp": "netstat-openbsd.txt"}}, 3000, ""},
		{"linux", fakeLocal{outputs: map[string]string{"ss -tlnp": "ss-debian.txt"}}, 5173, "python3"},
		{"linux", fakeLocal{files: map[string]string{"/proc/net/tcp": "proc-debian.txt"}}, 8123, ""},
		{"windows", fakeLocal{outputs: map[string]string{
			"netstat -ano -p TCP":   "netstat-windows.txt",
			"netstat -ano -p TCPv6": "netstat6-windows.txt",
			"tasklist /fo csv /nh":  "tasklist-windows.txt",
		}}, 8123, "python"},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			ports, err := LocalPorts(t.Context(), tc.goos, tc.fake.run, tc.fake.read)
			if err != nil {
				t.Fatal(err)
			}
			if !hasPort(ports, tc.port, tc.process) {
				t.Errorf("LocalPorts = %+v, want :%d (%q)", ports, tc.port, tc.process)
			}
			if !slices.IsSortedFunc(ports, func(a, b RemotePort) int { return int(a.Port) - int(b.Port) }) {
				t.Errorf("ports are not sorted: %+v", ports)
			}
		})
	}
}

func TestLocalPortsError(t *testing.T) {
	_, err := LocalPorts(t.Context(), "darwin", (&fakeLocal{}).run, (&fakeLocal{}).read)
	if err == nil || !strings.Contains(err.Error(), "lsof") {
		t.Errorf("err = %v, want one naming lsof", err)
	}
}

// TestLocalPortsHere lists this machine's ports for real and finds a
// listener the test opens.
func TestLocalPortsHere(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := netip.MustParseAddrPort(ln.Addr().String()).Port()
	ports, err := LocalPorts(t.Context(), runtime.GOOS, RunLocal, os.ReadFile)
	if err != nil {
		t.Skipf("no port-listing tool here: %v", err)
	}
	if !slices.ContainsFunc(ports, func(p RemotePort) bool { return p.Port == port }) {
		t.Errorf("LocalPorts lacks the test's listener on :%d", port)
	}
}
