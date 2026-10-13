//go:build e2e

package e2e

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// buildPfwd builds the real binary into dir.
func buildPfwd(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "pfwd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pfwd").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// pfwdCmd runs bin with dir as its home: config, state, log, and an ssh
// config at ~/.ssh/config naming the ss container e2e-ss, which pfwd reads
// itself (use_openssh_config = false), so no command needs -F.
func pfwdCmd(t *testing.T, bin, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cfg := filepath.Join(dir, ".ssh", "config")
	if _, err := os.Stat(cfg); err != nil {
		config := fmt.Sprintf("Host e2e-ss\n  HostName 127.0.0.1\n  Port %d\n  User pfwd\n  IdentityFile %s\n  IdentitiesOnly yes\n  IdentityAgent none\n  UserKnownHostsFile %s\n  StrictHostKeyChecking accept-new\n",
			sshPort(t, "ss"), filepath.ToSlash(keyPath), filepath.ToSlash(filepath.Join(dir, "known_hosts")))
		if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		settings := "[ssh]\nuse_openssh_config = false\n\n[ui]\nnotifications = false\n"
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+dir, "USERPROFILE="+dir, "XDG_STATE_HOME="+filepath.Join(dir, "state"),
		"APPDATA="+filepath.Join(dir, "appdata"), "PFWD_CONFIG="+filepath.Join(dir, "config.toml"))
	return cmd
}

// TestE2ELsAndFwd: pfwd ls --json lists a server on the container, and pfwd
// fwd forwards it until interrupted.
func TestE2ELsAndFwd(t *testing.T) {
	dir := t.TempDir()
	bin := buildPfwd(t, dir)
	startServer(t, "ss", "8140")

	var listed struct {
		Hosts []struct {
			Ports []struct {
				Port    int    `json:"port"`
				Process string `json:"process"`
			} `json:"ports"`
		} `json:"hosts"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		out, err := pfwdCmd(t, bin, dir, "ls", "e2e-ss", "--json").Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				out = append(out, exit.Stderr...)
			}
			t.Fatalf("pfwd ls: %v\n%s", err, out)
		}
		if err := json.Unmarshal(out, &listed); err != nil {
			t.Fatalf("pfwd ls --json printed %q: %v", out, err)
		}
		if len(listed.Hosts) == 1 && slices.ContainsFunc(listed.Hosts[0].Ports, func(p struct {
			Port    int    `json:"port"`
			Process string `json:"process"`
		}) bool {
			return p.Port == 8140
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pfwd ls never listed :8140:\n%s", out)
		}
		time.Sleep(500 * time.Millisecond)
	}

	local := freePort(t)
	cmd := pfwdCmd(t, bin, dir, "fwd", "e2e-ss", fmt.Sprintf("%d:8140", local))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	select {
	case line := <-lines:
		if want := fmt.Sprintf("Forwarded :8140 to localhost:%d", local); line != want {
			t.Fatalf("pfwd fwd printed %q, want %q", line, want)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("pfwd fwd printed nothing in 15 s")
	}
	get(t, fmt.Sprint(local))

	if runtime.GOOS == "windows" {
		return // no Ctrl+C to send; the deferred Kill ends it
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	for range lines {
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Errorf("pfwd fwd after Ctrl+C: %v, want exit code 130", err)
	}
}

// TestE2EDoctor: every check passes against the ss container.
func TestE2EDoctor(t *testing.T) {
	dir := t.TempDir()
	bin := buildPfwd(t, dir)
	out, err := pfwdCmd(t, bin, dir, "doctor", "e2e-ss").CombinedOutput()
	if err != nil {
		t.Fatalf("pfwd doctor: %v\n%s", err, out)
	}
	for _, name := range []string{"login", "detection", "-L forwards", "-R forwards", "local bind"} {
		found := false
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, name) && (strings.HasPrefix(l, "✓") || strings.HasPrefix(l, "+")) {
				found = true
			}
		}
		if !found {
			t.Errorf("check %q did not pass:\n%s", name, out)
		}
	}
}

// TestE2EDaemon: pfwd fwd --detach starts the real binary's daemon, which
// keeps the forward to the ss container after the command returns; ls
// lists it, stop ends it, and daemon stop ends the daemon.
func TestE2EDaemon(t *testing.T) {
	dir := t.TempDir()
	bin := buildPfwd(t, dir)
	socket := e2eSocket(t)
	cmd := func(args ...string) *exec.Cmd {
		c := pfwdCmd(t, bin, dir, args...)
		c.Env = append(c.Env, "PFWD_SOCKET="+socket)
		return c
	}
	t.Cleanup(func() {
		out, err := cmd("daemon", "status", "--json").Output()
		var st struct {
			Running bool `json:"running"`
			PID     int  `json:"pid"`
		}
		if err != nil || json.Unmarshal(out, &st) != nil || !st.Running {
			return
		}
		if cmd("daemon", "stop").Run() != nil {
			if p, err := os.FindProcess(st.PID); err == nil {
				_ = p.Kill()
			}
		}
	})
	startServer(t, "ss", "8141")

	local := freePort(t)
	out, err := cmd("fwd", "e2e-ss", fmt.Sprintf("%d:8141", local), "--detach").CombinedOutput()
	if err != nil || !strings.Contains(string(out), fmt.Sprintf("Forwarded :8141 to localhost:%d", local)) ||
		!strings.Contains(string(out), "Started the pfwd daemon") {
		t.Fatalf("pfwd fwd --detach without a daemon: %v\n%s", err, out)
	}
	get(t, fmt.Sprint(local))

	out, err = cmd("ls", "--json").Output()
	var listed struct {
		Hosts []struct {
			Host     string `json:"host"`
			Forwards []struct {
				Local int `json:"local"`
			} `json:"forwards"`
		} `json:"hosts"`
	}
	if err != nil || json.Unmarshal(out, &listed) != nil || len(listed.Hosts) != 1 || len(listed.Hosts[0].Forwards) != 1 ||
		listed.Hosts[0].Forwards[0].Local != int(local) {
		t.Fatalf("pfwd ls --json: %v\n%s", err, out)
	}
	if out, err := cmd("stop", fmt.Sprintf("e2e-ss:%d", local)).CombinedOutput(); err != nil {
		t.Fatalf("pfwd stop: %v\n%s", err, out)
	}
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 2*time.Second); err == nil {
		_ = c.Close()
		t.Error("the stopped forward still accepts connections")
	}
	if out, err := cmd("daemon", "stop").CombinedOutput(); err != nil || !strings.Contains(string(out), "stopped") {
		t.Fatalf("pfwd daemon stop: %v\n%s", err, out)
	}
	if err := cmd("daemon", "status").Run(); err == nil {
		t.Error("pfwd daemon status succeeds after stop")
	}
}

// e2eSocket is a fresh daemon socket, or named pipe on Windows.
func e2eSocket(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return `\\.\pipe\pfwd-e2e-` + hex.EncodeToString(b)
	}
	dir, err := os.MkdirTemp("", "pf") //nolint:usetesting // t.TempDir is too long for a socket path on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d", "pfwd.sock")
}
