//go:build e2e && perf && linux

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

// The performance budgets from the spec, measured against the sshd
// containers. `make perf` runs them; CI's perf job too.

// delayProxy forwards TCP to target, holding every chunk for delay in each
// direction: a link with an RTT of twice delay and no bandwidth limit.
type delayProxy struct {
	ln     net.Listener
	target string
	delay  time.Duration
}

func startDelayProxy(t *testing.T, target string, delay time.Duration) *delayProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &delayProxy{ln: ln, target: target, delay: delay}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.pipe(c)
		}
	}()
	return p
}

func (p *delayProxy) port() int { return p.ln.Addr().(*net.TCPAddr).Port } //nolint:errcheck,forcetypeassert // a tcp listener

func (p *delayProxy) pipe(client net.Conn) {
	server, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = client.Close()
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.delayed(server, client) }()
	go func() { defer wg.Done(); p.delayed(client, server) }()
	wg.Wait()
}

// delayed copies src to dst, each chunk leaving delay after it arrived.
func (p *delayProxy) delayed(dst, src net.Conn) {
	type chunk struct {
		data []byte
		at   time.Time
	}
	ch := make(chan chunk, 1024)
	go func() {
		defer close(ch)
		for {
			buf := make([]byte, 32<<10)
			n, err := src.Read(buf)
			if n > 0 {
				ch <- chunk{buf[:n], time.Now().Add(p.delay)}
			}
			if err != nil {
				return
			}
		}
	}()
	for c := range ch {
		time.Sleep(time.Until(c.at))
		if _, err := dst.Write(c.data); err != nil {
			break
		}
	}
	_ = dst.Close()
	_ = src.Close()
}

// ptyRun starts bin in a pseudo-terminal and returns a function that waits
// for text on the screen and reports how long after the start it came.
func ptyRun(t *testing.T, cmd *exec.Cmd) func(want string, timeout time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	term, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = term.Close()
	})
	var mu sync.Mutex
	var screen bytes.Buffer
	var seen []struct {
		at   time.Duration
		size int
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			mu.Lock()
			screen.Write(buf[:n])
			seen = append(seen, struct {
				at   time.Duration
				size int
			}{time.Since(start), screen.Len()})
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return func(want string, timeout time.Duration) time.Duration {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			mu.Lock()
			raw := screen.Bytes()
			// Find the first read that contained want.
			for _, s := range seen {
				if strings.Contains(ansi.Strip(string(raw[:s.size])), want) {
					mu.Unlock()
					return s.at
				}
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("%q never showed; the terminal got:\n%s", want, ansi.Strip(screen.String()))
		return 0
	}
}

// TestPerfStartAndFirstPorts: the first frame within 300 ms of the start,
// and the remote's listening ports within 1.5 s on a 50 ms RTT link.
func TestPerfStartAndFirstPorts(t *testing.T) {
	dir := t.TempDir()
	bin := buildPfwd(t, dir)
	startServer(t, "ss", "8150")
	// Time pfwd, not python's start-up: wait until the server listens.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := compose("exec", "ss", "sh", "-c", "ss -ltn | grep -q ':8150 '"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server on :8150 never listened")
		}
		time.Sleep(100 * time.Millisecond)
	}
	proxy := startDelayProxy(t, fmt.Sprintf("127.0.0.1:%d", sshPort(t, "ss")), 25*time.Millisecond)
	cfg := filepath.Join(dir, "slow_config")
	config := fmt.Sprintf("Host slow\n  HostName 127.0.0.1\n  Port %d\n  User pfwd\n  IdentityFile %s\n  IdentitiesOnly yes\n  IdentityAgent none\n  UserKnownHostsFile %s\n  StrictHostKeyChecking accept-new\n",
		proxy.port(), keyPath, filepath.Join(dir, "known_hosts"))
	if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := pfwdCmd(t, bin, dir, "-F", cfg, "--no-auto", "slow")
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	wait := ptyRun(t, cmd)
	first := wait("slow", 5*time.Second)
	ports := wait(":8150", 10*time.Second)
	t.Logf("first frame %v; first detected ports %v (50 ms RTT)", first.Round(time.Millisecond), ports.Round(time.Millisecond))
	if first > 300*time.Millisecond {
		t.Errorf("first frame after %v, budget 300ms", first)
	}
	if ports > 1500*time.Millisecond {
		t.Errorf("first detected ports after %v, budget 1.5s", ports)
	}
}

// procStat returns a process's CPU time (user + system) and resident set.
func procStat(t *testing.T, pid int) (cpu time.Duration, rss int64) {
	t.Helper()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	// Fields after the command name, which is in parentheses.
	f := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+2:]))
	utime, _ := strconv.ParseInt(f[11], 10, 64)
	stime, _ := strconv.ParseInt(f[12], 10, 64)
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			kb, _ := strconv.ParseInt(strings.Fields(line)[1], 10, 64)
			rss = kb << 10
		}
	}
	const clockTicks = 100 // USER_HZ on Linux
	return time.Duration(utime+stime) * time.Second / clockTicks, rss
}

// TestPerfIdle: the daemon with 3 hosts and 20 forwards idles under 1% CPU
// in under 40 MB.
func TestPerfIdle(t *testing.T) {
	dir := t.TempDir()
	bin := buildPfwd(t, dir)
	socket := e2eSocket(t)
	cmd := func(args ...string) *exec.Cmd {
		c := pfwdCmd(t, bin, dir, args...)
		c.Env = append(c.Env, "PFWD_SOCKET="+socket)
		return c
	}
	_ = cmd("version")
	// Two more aliases: e2e-ss2 for the same container, e2e-proc for the
	// other one.
	f, err := os.OpenFile(filepath.Join(dir, ".ssh", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []struct {
		name    string
		service string
	}{{"e2e-ss2", "ss"}, {"e2e-proc", "proc"}} {
		fmt.Fprintf(f, "Host %s\n  HostName 127.0.0.1\n  Port %d\n  User pfwd\n  IdentityFile %s\n  IdentitiesOnly yes\n  IdentityAgent none\n  UserKnownHostsFile %s\n  StrictHostKeyChecking accept-new\n",
			h.name, sshPort(t, h.service), keyPath, filepath.Join(dir, "known_hosts"))
	}
	_ = f.Close()
	t.Cleanup(func() { _ = cmd("daemon", "stop").Run() })

	forwards := 0
	for i, host := range []string{"e2e-ss", "e2e-ss2", "e2e-proc"} {
		var specs []string
		for j := range []int{7, 7, 6}[i] {
			specs = append(specs, fmt.Sprintf("%d:%d", freePort(t), 9100+j))
		}
		args := append([]string{"fwd", host}, specs...)
		if out, err := cmd(append(args, "--detach", "--no-auto")...).CombinedOutput(); err != nil {
			t.Fatalf("pfwd fwd %s: %v\n%s", host, err, out)
		}
		forwards += len(specs)
	}
	out, err := cmd("daemon", "status", "--json").Output()
	var st struct {
		PID      int `json:"pid"`
		Forwards int `json:"forwards"`
		Hosts    []struct {
			State string `json:"state"`
		} `json:"hosts"`
	}
	if err != nil || json.Unmarshal(out, &st) != nil || st.Forwards != 20 || len(st.Hosts) != 3 {
		t.Fatalf("daemon status: %v\n%s", err, out)
	}
	time.Sleep(5 * time.Second) // let the first scans and listeners settle
	cpu0, _ := procStat(t, st.PID)
	const window = 20 * time.Second
	time.Sleep(window)
	cpu1, rss := procStat(t, st.PID)
	share := float64(cpu1-cpu0) / float64(window) * 100
	t.Logf("idle with %d hosts and %d forwards: CPU %.2f%%, RSS %.1f MB", len(st.Hosts), forwards, share, float64(rss)/(1<<20))
	if share >= 1 {
		t.Errorf("idle CPU %.2f%%, budget 1%%", share)
	}
	if rss >= 40<<20 {
		t.Errorf("RSS %.1f MB, budget 40 MB", float64(rss)/(1<<20))
	}
}

// download fetches url and returns the bytes per second.
func download(t *testing.T, url string, size int64) float64 {
	t.Helper()
	start := time.Now()
	resp, err := http.Get(url) //nolint:noctx // a test against a local forward
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n != size {
		t.Fatalf("download %s: %d bytes, %v", url, n, err)
	}
	return float64(n) / time.Since(start).Seconds()
}

func median(xs []float64) float64 {
	slices.Sort(xs)
	return xs[len(xs)/2]
}

// waitListening waits until something accepts on 127.0.0.1:port.
func waitListening(t *testing.T, port uint16) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nothing listens on %d", port)
}

// TestPerfThroughput: a forward carries at least 80% of what ssh -L does
// on the same link (the median of three 64 MB downloads each).
func TestPerfThroughput(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("needs OpenSSH's ssh to compare with")
	}
	const size = 64 << 20
	if out, err := compose("exec", "-u", "pfwd", "ss", "sh", "-c", fmt.Sprintf("head -c %d /dev/urandom > /tmp/blob", size)); err != nil {
		t.Fatalf("make the blob: %v\n%s", err, out)
	}
	if out, err := compose("exec", "-d", "-u", "pfwd", "ss", "python3", "-m", "http.server", "8151", "--bind", "127.0.0.1", "--directory", "/tmp"); err != nil {
		t.Fatalf("serve the blob: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = compose("exec", "ss", "pkill", "-f", "http.server 8151") })

	dir := t.TempDir()
	bin := buildPfwd(t, dir)
	viaPfwd := freePort(t)
	fwd := pfwdCmd(t, bin, dir, "fwd", "e2e-ss", fmt.Sprintf("%d:8151", viaPfwd), "--no-auto")
	if err := fwd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fwd.Process.Kill(); _ = fwd.Wait() })
	viaSSH := freePort(t)
	sshL := exec.Command("ssh", "-N", "-p", strconv.Itoa(sshPort(t, "ss")), "-i", keyPath,
		"-o", "IdentitiesOnly=yes", "-o", "UserKnownHostsFile="+filepath.Join(dir, "ssh_known_hosts"), "-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes", "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:8151", viaSSH), "pfwd@127.0.0.1")
	if err := sshL.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sshL.Process.Kill(); _ = sshL.Wait() })
	waitListening(t, viaPfwd)
	waitListening(t, viaSSH)

	var pfwdRates, sshRates []float64
	for range 3 {
		pfwdRates = append(pfwdRates, download(t, fmt.Sprintf("http://127.0.0.1:%d/blob", viaPfwd), size))
		sshRates = append(sshRates, download(t, fmt.Sprintf("http://127.0.0.1:%d/blob", viaSSH), size))
	}
	p, o := median(pfwdRates), median(sshRates)
	t.Logf("throughput: pfwd %.1f MB/s, ssh -L %.1f MB/s (%.0f%%)", p/(1<<20), o/(1<<20), p/o*100)
	if p < 0.8*o {
		t.Errorf("pfwd carries %.0f%% of ssh -L, budget 80%%", p/o*100)
	}
}
