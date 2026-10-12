package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/sshx"
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
