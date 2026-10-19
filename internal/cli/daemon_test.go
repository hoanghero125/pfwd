package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// daemonSocket points PFWD_SOCKET at a fresh socket or pipe.
func daemonSocket(t *testing.T) string {
	t.Helper()
	var path string
	if runtime.GOOS == "windows" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		path = `\\.\pipe\pfwd-test-` + hex.EncodeToString(b)
	} else {
		dir, err := os.MkdirTemp("", "pf") //nolint:usetesting // t.TempDir is too long for a socket path on macOS
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		path = filepath.Join(dir, "d", "pfwd.sock")
	}
	t.Setenv("PFWD_SOCKET", path)
	return path
}

// startDaemon runs `pfwd daemon run` in this process until the test ends,
// and waits until it answers.
func startDaemon(t *testing.T, args ...string) string {
	t.Helper()
	path := daemonSocket(t)
	ctx, cancel := context.WithCancel(t.Context())
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- executeContext(ctx, append([]string{"daemon", "run"}, args...), &out, &errOut) }()
	t.Cleanup(func() {
		cancel()
		if code := <-done; code != 0 && code != exitInterrupted {
			t.Errorf("pfwd daemon run: code %d, stderr %q", code, errOut.String())
		}
	})
	if _, err := waitStatus(t.Context(), path, 5*time.Second); err != nil {
		t.Fatalf("the daemon didn't answer: %v; stderr %q", err, errOut.String())
	}
	return path
}

func TestDaemonStatusAndStop(t *testing.T) {
	headlessEnv(t)
	startDaemon(t)
	code, out, _ := run(t, "daemon", "status")
	if code != 0 || !strings.Contains(out, "running") || !strings.Contains(out, "0 forwards") {
		t.Errorf("pfwd daemon status: code %d:\n%s", code, out)
	}
	code, out, _ = run(t, "daemon", "status", "--json")
	var st statusJSON
	if err := json.Unmarshal([]byte(out), &st); code != 0 || err != nil || !st.Running || st.PID != os.Getpid() {
		t.Errorf("pfwd daemon status --json: code %d, %v:\n%s", code, err, out)
	}
	if code, _, errOut := run(t, "daemon", "start"); code != 1 || !strings.Contains(errOut, "already running") {
		t.Errorf("pfwd daemon start while one runs: code %d, stderr %q", code, errOut)
	}
	if code, out, _ := run(t, "daemon", "stop"); code != 0 || !strings.Contains(out, "stopped") {
		t.Errorf("pfwd daemon stop: code %d:\n%s", code, out)
	}
	if code, out, _ := run(t, "daemon", "status"); code != 1 || !strings.Contains(out, "No pfwd daemon") {
		t.Errorf("pfwd daemon status after stop: code %d:\n%s", code, out)
	}
	if code, out, _ := run(t, "daemon", "stop"); code != 0 || !strings.Contains(out, "No pfwd daemon") {
		t.Errorf("pfwd daemon stop with none running: code %d:\n%s", code, out)
	}
}

func TestDaemonStatusNotRunning(t *testing.T) {
	headlessEnv(t)
	daemonSocket(t)
	code, out, _ := run(t, "daemon", "status", "--json")
	if code != 1 || strings.TrimSpace(out) != "{\n  \"running\": false\n}" {
		t.Errorf("pfwd daemon status --json with none running: code %d, %q", code, out)
	}
}

func TestPromptBroker(t *testing.T) {
	b := promptBroker{pending: map[string]pendingPrompt{}}
	keyReply := make(chan bool, 1)
	credReply := make(chan engine.CredentialAnswer, 1)
	kp, _ := b.add(engine.NewHostKeyPrompt("devbox", sshx.HostKeyQuestion{Host: "devbox", KeyType: "ssh-ed25519", Fingerprint: "SHA256:x"}, keyReply))
	cp, _ := b.add(engine.NewCredentialPrompt("gpu", sshx.CredentialQuestion{Host: "gpu", Kind: sshx.Password, Prompt: "alice"}, credReply))
	if kp.Kind != "host_key" || cp.Kind != "password" || len(b.list()) != 2 || b.list()[0].ID != kp.ID {
		t.Fatalf("prompts = %+v", b.list())
	}
	if err := b.answer(answerJSON{ID: kp.ID, Accept: true}); err != nil || !<-keyReply {
		t.Errorf("answering the host key: %v", err)
	}
	if err := b.answer(answerJSON{ID: kp.ID, Accept: true}); err == nil {
		t.Error("a second answer to the same prompt was accepted")
	}
	secret := "hunter2"
	if err := b.answer(answerJSON{ID: cp.ID, Secret: &secret}); err != nil {
		t.Fatal(err)
	}
	if a := <-credReply; string(a.Secret) != "hunter2" {
		t.Errorf("credential answer = %+v", a)
	}
	b.add(engine.NewHostKeyPrompt("gpu", sshx.HostKeyQuestion{Host: "gpu"}, make(chan bool, 1)))
	if ids := b.dropHost("gpu"); len(ids) != 1 || len(b.list()) != 0 {
		t.Errorf("dropHost = %v, left %+v", ids, b.list())
	}
}

func TestIPCErrorsMapToExitCodes(t *testing.T) {
	for code, want := range map[string]int{ipc.CodeUsage: 2, ipc.CodeConnect: 3, ipc.CodeAuth: 4, ipc.CodeHostKey: 5, ipc.CodeBind: 6, ipc.CodeNotFound: 1} {
		if got := exitCode(ipc.Errorf(code, "x")); got != want {
			t.Errorf("exit code for %s = %d, want %d", code, got, want)
		}
	}
}

// TestDaemonStartRefusesTestBinary: started from a test binary, daemon
// start would run the tests again in the background.
func TestDaemonStartRefusesTestBinary(t *testing.T) {
	headlessEnv(t)
	daemonSocket(t)
	if code, _, errOut := run(t, "daemon", "start"); code != 1 || !strings.Contains(errOut, "test binary") {
		t.Errorf("pfwd daemon start from a test: code %d, stderr %q", code, errOut)
	}
}

// TestFwdDetachLsStop: pfwd fwd --detach leaves a forward in the daemon,
// pfwd ls lists it, and pfwd stop ends it.
func TestFwdDetachLsStop(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo"))}, true)
	startDaemon(t, "-F", h.sshConfig)
	local := freeLocalPort(t)
	code, out, errOut := run(t, "fwd", "testbox", fmt.Sprintf("%d:%d", local, echo), "--detach")
	if want := fmt.Sprintf("Forwarded :%d to localhost:%d", echo, local); code != 0 || !strings.Contains(out, want) {
		t.Fatalf("pfwd fwd --detach: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 5*time.Second)
	if err != nil {
		t.Fatalf("the daemon's forward doesn't answer: %v", err)
	}
	_ = c.Close()

	code, out, _ = run(t, "ls", "--json")
	var listed struct {
		Hosts []hostJSON `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &listed); code != 0 || err != nil || len(listed.Hosts) != 1 ||
		listed.Hosts[0].Host != "testbox" || len(listed.Hosts[0].Forwards) != 1 {
		t.Fatalf("pfwd ls --json from the daemon: code %d, %v:\n%s", code, err, out)
	}
	if code, out, _ := run(t, "ls"); code != 0 || !strings.Contains(out, fmt.Sprintf("localhost:%d active", local)) {
		t.Errorf("pfwd ls from the daemon: code %d:\n%s", code, out)
	}
	if code, _, errOut := run(t, "stop", "99"); code != 1 || !strings.Contains(errOut, "no forward 99") {
		t.Errorf("pfwd stop 99: code %d, stderr %q", code, errOut)
	}
	if code, out, _ := run(t, "stop", fmt.Sprintf("testbox:%d", local)); code != 0 || !strings.Contains(out, "Stopped 1 forward") {
		t.Errorf("pfwd stop testbox:%d: code %d, %q", local, code, out)
	}
	_, out, _ = run(t, "ls", "--json")
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed.Hosts[0].Forwards) != 0 {
		t.Errorf("the forward is still listed after stop:\n%s", out)
	}
}

// TestDaemonStopEndsForwardsFirst: when pfwd daemon stop returns, the
// daemon's forwards have ended, so their local ports are free again.
func TestDaemonStopEndsForwardsFirst(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo"))}, true)
	startDaemon(t, "-F", h.sshConfig)
	local := freeLocalPort(t)
	if code, out, errOut := run(t, "fwd", "testbox", fmt.Sprintf("%d:%d", local, echo), "--detach"); code != 0 {
		t.Fatalf("pfwd fwd --detach: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, out, _ := run(t, "daemon", "stop"); code != 0 {
		t.Fatalf("pfwd daemon stop: code %d:\n%s", code, out)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", local))
	if err != nil {
		t.Fatalf("port %d is still in use after pfwd daemon stop: %v", local, err)
	}
	_ = ln.Close()
}

// TestFwdDetachUnknownHostKeyWithoutTerminal: the daemon's host-key prompt
// reaches the client, which has no terminal to ask: exit 5 with the fix.
func TestFwdDetachUnknownHostKeyWithoutTerminal(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux()}, false)
	startDaemon(t, "-F", h.sshConfig)
	code, _, errOut := run(t, "fwd", "testbox", "5432", "--detach")
	if code != 5 || !strings.Contains(errOut, "--accept-new") {
		t.Errorf("pfwd fwd --detach with an unknown key: code %d, stderr %q", code, errOut)
	}
}

func TestStopWithoutDaemon(t *testing.T) {
	headlessEnv(t)
	daemonSocket(t)
	if code, _, errOut := run(t, "stop", "devbox:5432"); code != 1 || !strings.Contains(errOut, "no pfwd daemon") {
		t.Errorf("pfwd stop without a daemon: code %d, stderr %q", code, errOut)
	}
	if code, _, _ := run(t, "stop"); code != 2 {
		t.Errorf("pfwd stop with nothing named: code %d, want 2", code)
	}
}

// nextEvent reads events from eng until one matches, or fails.
func nextEvent[T engine.Event](t *testing.T, eng *attachEngine, match func(T) bool) T {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				t.Fatal("the event stream ended")
			}
			if e, ok := ev.(T); ok && (match == nil || match(e)) {
				return e
			}
		case <-timeout:
			var zero T
			t.Fatalf("no %T within 10 s", zero)
		}
	}
}

// attached returns an attach engine on the daemon at path, watching until
// the test ends.
func attached(t *testing.T, path string) *attachEngine {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	eng, err := newAttachEngine(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- eng.watch(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		eng.close()
	})
	return eng
}

func TestAttachEngine(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo"))}, true)
	path := startDaemon(t, "-F", h.sshConfig)
	local := freeLocalPort(t)
	if code, _, errOut := run(t, "fwd", "testbox", fmt.Sprintf("%d:%d", local, echo), "--detach"); code != 0 {
		t.Fatalf("pfwd fwd --detach: code %d, stderr %q", code, errOut)
	}
	eng := attached(t, path)
	nextEvent(t, eng, func(s engine.SessionChanged) bool { return s.Host == "testbox" && s.State == engine.Connected })
	first := nextEvent(t, eng, func(f engine.ForwardChanged) bool { return f.Forward.Spec.LocalPort == uint16(local) })
	if first.Forward.State != engine.Active || first.Forward.Spec.Kind != engine.Local {
		t.Errorf("the daemon's forward = %+v", first.Forward)
	}
	if fws := eng.Forwards(); len(fws) != 1 || fws[0].ID != first.Forward.ID {
		t.Errorf("Forwards = %+v", fws)
	}
	second, err := eng.Forward("testbox", engine.ForwardSpec{LocalPort: uint16(freeLocalPort(t)), RemotePort: uint16(echo)})
	if err != nil || second.State != engine.Active {
		t.Fatalf("Forward through the daemon = %+v, %v", second, err)
	}
	if err := eng.SetPinned(second.ID, true); err != nil {
		t.Error(err)
	}
	if err := eng.Stop(first.Forward.ID); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, eng, func(f engine.ForwardChanged) bool {
		return f.Forward.ID == first.Forward.ID && f.Forward.State == engine.Stopped
	})
}

// TestAttachAnswersHostKeyPrompt: a host connected with + asks about its
// host key; the TUI's yes reaches the daemon, which connects and saves it.
func TestAttachAnswersHostKeyPrompt(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux()}, false)
	path := startDaemon(t, "-F", h.sshConfig)
	eng := attached(t, path)
	if _, err := eng.connect(t.Context(), "testbox"); err != nil {
		t.Fatal(err)
	}
	p := nextEvent[engine.HostKeyPrompt](t, eng, nil)
	if p.Host != "testbox" || p.Question.Fingerprint == "" {
		t.Fatalf("prompt = %+v", p)
	}
	p.Answer(true)
	nextEvent(t, eng, func(s engine.SessionChanged) bool { return s.Host == "testbox" && s.State == engine.Connected })
	if data, _ := os.ReadFile(h.knownHosts); len(data) == 0 {
		t.Error("the accepted key wasn't saved")
	}
}

// TestFwdDetachRefusesHostFlags: the daemon resolves hosts from the ssh
// config, so per-host flags would be ignored; they are refused instead.
func TestFwdDetachRefusesHostFlags(t *testing.T) {
	headlessEnv(t)
	for _, flag := range [][]string{{"-p", "2222"}, {"-l", "bob"}, {"-J", "bastion"}, {"-i", "key"}, {"-F", "cfg"}, {"--allow-lan-socks"}} {
		args := append([]string{"fwd", "devbox", "5432", "--detach"}, flag...)
		if code, _, errOut := run(t, args...); code != 2 || !strings.Contains(errOut, "--detach") {
			t.Errorf("pfwd %q: code %d, stderr %q; want 2", args, code, errOut)
		}
	}
}

// TestFwdDetachInterrupted: an interrupt while the daemon works exits 130.
func TestFwdDetachInterrupted(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux()}, true)
	startDaemon(t, "-F", h.sshConfig)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, errOut bytes.Buffer
	if code := executeContext(ctx, []string{"fwd", "testbox", "5432", "--detach"}, &out, &errOut); code != 130 {
		t.Errorf("pfwd fwd --detach interrupted: code %d, stderr %q; want 130", code, errOut.String())
	}
}

// TestFwdDetachAcceptNew: with --accept-new, the client trusts the new
// host key the daemon asks about.
func TestFwdDetachAcceptNew(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo"))}, false)
	startDaemon(t, "-F", h.sshConfig)
	code, _, errOut := run(t, "fwd", "testbox", fmt.Sprintf("%d:%d", freeLocalPort(t), echo), "--detach", "--accept-new")
	if code != 0 {
		t.Fatalf("pfwd fwd --detach --accept-new: code %d, stderr %q", code, errOut)
	}
	if data, _ := os.ReadFile(h.knownHosts); len(data) == 0 {
		t.Error("the accepted key wasn't saved")
	}
}
