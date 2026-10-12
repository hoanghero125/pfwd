package detect

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// linesParser returns one port per non-empty line, with the line as the
// process name, so frame boundaries are easy to see.
func linesParser(r io.Reader) ([]RemotePort, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []RemotePort
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l != "" {
			out = append(out, RemotePort{Port: 1, Process: l})
		}
	}
	return out, nil
}

func names(ports []RemotePort) []string {
	var out []string
	for _, p := range ports {
		out = append(out, p.Process)
	}
	return out
}

func frame(body string) string { return "\x1ePFWD\n" + body + "\x1ePFWD END\n" }

func TestReadFrames(t *testing.T) {
	in := "noise before\n" + frame("A\n") + "noise between\n" + frame("B\nC\n") + frame("") + "\x1ePFWD\npartial\n"
	var got [][]string
	err := readFrames(strings.NewReader(in), linesParser, func(p []RemotePort) { got = append(got, names(p)) })
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"A"}, {"B", "C"}, nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frames = %q, want %q", got, want)
	}
}

func TestReadFramesCRLF(t *testing.T) {
	in := "\x1ePFWD\r\nA\r\n\x1ePFWD END\r\n"
	var got [][]string
	if err := readFrames(strings.NewReader(in), linesParser, func(p []RemotePort) { got = append(got, names(p)) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("frames = %q, want one", got)
	}
}

func TestReadFramesDropsOversizedFrame(t *testing.T) {
	huge := strings.Repeat("x", 1000) + "\n"
	in := "\x1ePFWD\n" + strings.Repeat(huge, 5000) + frame("ok\n")
	var got [][]string
	if err := readFrames(strings.NewReader(in), linesParser, func(p []RemotePort) { got = append(got, names(p)) }); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"ok"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("frames = %d, want only the small frame", len(got))
	}
}

// fakeRunner answers the probe with probe and serves streams in order; once
// they run out, Start blocks until ctx is done.
type fakeRunner struct {
	probe    string
	probeErr error

	mu      sync.Mutex
	streams []string
	cmds    []string
}

func (f *fakeRunner) Output(_ context.Context, cmd string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, cmd)
	f.mu.Unlock()
	return []byte(f.probe), f.probeErr
}

func (f *fakeRunner) Start(ctx context.Context, cmd string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if len(f.streams) > 0 {
		s := f.streams[0]
		f.streams = f.streams[1:]
		return io.NopCloser(strings.NewReader(s)), nil
	}
	pr, pw := io.Pipe()
	stop := context.AfterFunc(ctx, func() { pw.Close() })
	return readCloser{pr, func() error { stop(); return pw.Close() }}, nil
}

type readCloser struct {
	io.Reader
	close func() error
}

func (r readCloser) Close() error { return r.close() }

func ssFrame(lines ...string) string {
	return frame(strings.Join(lines, "\n") + "\n")
}

const (
	ssA = `LISTEN 0 5 0.0.0.0:8123 0.0.0.0:* users:(("python3",pid=10,fd=3))`
	ssB = `LISTEN 0 5 127.0.0.1:5173 0.0.0.0:* users:(("node",pid=11,fd=3))`
)

// collect runs Watch until it has n updates, then cancels it.
func collect(t *testing.T, r Runner, n int) ([]Update, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var got []Update
	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, r, WatchOptions{Interval: 2 * time.Second}, func(u Update) {
			mu.Lock()
			got = append(got, u)
			if len(got) == n {
				cancel()
			}
			mu.Unlock()
		})
	}()
	err := <-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) < n {
		t.Fatalf("got %d updates before Watch returned (%v), want %d", len(got), err, n)
	}
	return got, err
}

func ports(u Update) []uint16 {
	var out []uint16
	for _, p := range u.Ports {
		out = append(out, p.Port)
	}
	return out
}

func TestWatchEmitsOnChange(t *testing.T) {
	r := &fakeRunner{
		probe:   "PFWD_OS Linux\nHAVE_SS\nHAVE_PROC\n",
		streams: []string{ssFrame(ssA) + ssFrame(ssA) + ssFrame(ssA, ssB) + ssFrame(ssB, ssA)},
	}
	// Frames: A (baseline), A (unchanged: no update), A+B (changed), A+B
	// again (B is now stable: an update with Added), B+A (same: none).
	got, err := collect(t, r, 3)
	if err != nil {
		t.Fatalf("Watch = %v, want nil after cancel", err)
	}
	if len(got) != 3 || !reflect.DeepEqual(ports(got[0]), []uint16{8123}) || !reflect.DeepEqual(ports(got[1]), []uint16{5173, 8123}) {
		t.Errorf("updates = %+v", got)
	}
	if len(got) == 3 && (len(got[2].Added) != 1 || got[2].Added[0].Port != 5173) {
		t.Errorf("third update Added = %+v, want :5173", got[2].Added)
	}
	if got[0].Strategy != SS || got[0].OS != "Linux" || got[0].Ports[0].Process != "python3" {
		t.Errorf("first update = %+v", got[0])
	}
	if r.cmds[0] != ShellCommand(ProbeScript) || r.cmds[1] != ShellCommand(LoopScript(SS, "Linux", 2*time.Second)) {
		t.Errorf("commands = %q", r.cmds)
	}
}

func TestWatchFirstSnapshotEmptyIsEmitted(t *testing.T) {
	r := &fakeRunner{probe: "PFWD_OS Linux\nHAVE_PROC\n", streams: []string{frame("")}}
	got, _ := collect(t, r, 1)
	if got[0].Strategy != Proc || len(got[0].Ports) != 0 {
		t.Errorf("update = %+v, want an empty Proc snapshot", got[0])
	}
}

func TestWatchNoStrategy(t *testing.T) {
	r := &fakeRunner{probe: "PFWD_OS SunOS\nHAVE_NETSTAT\n"}
	var got []Update
	err := Watch(t.Context(), r, WatchOptions{Interval: time.Second}, func(u Update) { got = append(got, u) })
	if err != nil {
		t.Fatal(err)
	}
	if want := []Update{{Strategy: None, OS: "SunOS"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("updates = %+v, want %+v", got, want)
	}
}

func TestWatchProbeError(t *testing.T) {
	r := &fakeRunner{probeErr: errors.New("exec disabled")}
	err := Watch(t.Context(), r, WatchOptions{Interval: time.Second}, func(Update) { t.Error("emit called") })
	if err == nil || !strings.Contains(err.Error(), "exec disabled") {
		t.Errorf("Watch = %v, want the probe error", err)
	}
}

func TestWatchRestartsLoop(t *testing.T) {
	r := &fakeRunner{
		probe:   "PFWD_OS Linux\nHAVE_SS\n",
		streams: []string{ssFrame(ssA), "", ssFrame(ssB)},
	}
	start := time.Now()
	got, _ := collect(t, r, 2)
	if !reflect.DeepEqual(ports(got[1]), []uint16{5173}) {
		t.Errorf("after restart = %+v", got[1])
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("restart took %v", d)
	}
}

func TestWatchOverSSH(t *testing.T) {
	ssOut, err := os.ReadFile(filepath.Join("testdata", "ss-alpine-root.txt"))
	if err != nil {
		t.Fatal(err)
	}
	probeCmd := ShellCommand(ProbeScript)
	loopCmd := ShellCommand(LoopScript(SS, "Linux", 2*time.Second))
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	srv := sshtest.Start(t, sshtest.Options{
		AuthorizedKeys: []ssh.PublicKey{signer.PublicKey()},
		Exec: func(ctx context.Context, cmd string, stdout io.Writer) int {
			switch cmd {
			case probeCmd:
				_, _ = io.WriteString(stdout, "PFWD_OS Linux\nHAVE_SS\nHAVE_PROC\n")
			case loopCmd:
				_, _ = io.WriteString(stdout, frame(string(ssOut)))
				<-ctx.Done()
			default:
				t.Errorf("unexpected command %q", cmd)
				return 127
			}
			return 0
		},
	})
	dir := t.TempDir()
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "id")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte(srv.KnownHostsLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conn, err := sshx.Dial(t.Context(), &sshx.HostConfig{
		Alias: "test", HostName: srv.Host(), Port: srv.Port(), User: "u",
		IdentityFiles: []string{keyPath}, IdentityAgent: "none",
		KnownHostsFiles: []string{kh}, StrictHostKeyChecking: "yes",
	}, sshx.DialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	got, err := collect(t, conn, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{5173, 6006, 8000, 8123}; !reflect.DeepEqual(ports(got[0]), want) {
		t.Errorf("ports = %v, want %v", ports(got[0]), want)
	}
}

func TestWatchReportsAddedAfterDebounce(t *testing.T) {
	r := &fakeRunner{
		probe:   "PFWD_OS Linux\nHAVE_SS\n",
		streams: []string{ssFrame(ssA) + ssFrame(ssA, ssB) + ssFrame(ssA, ssB) + ssFrame(ssA)},
	}
	got, _ := collect(t, r, 4)
	if len(got[0].Added) != 0 || len(got[1].Added) != 0 {
		t.Errorf("baseline and first sighting must not be Added: %+v %+v", got[0].Added, got[1].Added)
	}
	if len(got[2].Added) != 1 || got[2].Added[0].Port != 5173 {
		t.Errorf("third frame: Added = %+v, want :5173 after two consecutive frames", got[2].Added)
	}
	if len(got[3].Removed) != 1 || got[3].Removed[0] != 5173 {
		t.Errorf("fourth frame: Removed = %v, want [5173]", got[3].Removed)
	}
}

func TestWatchProbeTimesOut(t *testing.T) {
	r := &slowProbeRunner{}
	start := time.Now()
	err := Watch(t.Context(), r, WatchOptions{Interval: time.Second, ProbeTimeout: 100 * time.Millisecond}, func(Update) {})
	if err == nil {
		t.Fatal("Watch returned nil after the probe hung")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the probe timeout took %v", time.Since(start))
	}
}

// slowProbeRunner never answers the probe until its context ends.
type slowProbeRunner struct{}

func (slowProbeRunner) Output(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (slowProbeRunner) Start(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}
