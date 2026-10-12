//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
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

// pfwdCmd runs bin with its config, state and log in dir, and an ssh
// config naming the ss container e2e-ss.
func pfwdCmd(t *testing.T, bin, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cfg := filepath.Join(dir, "ssh_config")
	if _, err := os.Stat(cfg); err != nil {
		config := fmt.Sprintf("Host e2e-ss\n  HostName 127.0.0.1\n  Port %d\n  User pfwd\n  IdentityFile %s\n  IdentitiesOnly yes\n  IdentityAgent none\n  UserKnownHostsFile %s\n  StrictHostKeyChecking accept-new\n",
			sshPort(t, "ss"), filepath.ToSlash(keyPath), filepath.ToSlash(filepath.Join(dir, "known_hosts")))
		if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[ui]\nnotifications = false\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, append(args, "-F", cfg)...)
	cmd.Env = append(os.Environ(), "HOME="+dir, "XDG_STATE_HOME="+filepath.Join(dir, "state"), "APPDATA="+filepath.Join(dir, "appdata"),
		"PFWD_CONFIG="+filepath.Join(dir, "config.toml"))
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
