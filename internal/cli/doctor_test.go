package cli

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// checkLine returns the doctor output line of the check named name.
func checkLine(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

func TestDoctorAllGood(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo")), AllowRemoteForward: true}, true)
	code, out, errOut := run(t, "doctor", "testbox", "-F", h.sshConfig)
	if code != 0 {
		t.Fatalf("pfwd doctor: code %d, stderr %q:\n%s", code, errOut, out)
	}
	for _, name := range []string{"ssh config", "reach", "host key", "login", "detection", "-L forwards", "-R forwards", "local bind"} {
		if l := checkLine(out, name); !strings.Contains(l, "✓") {
			t.Errorf("check %q is not ok: %q\n%s", name, l, out)
		}
	}
	if !strings.Contains(checkLine(out, "detection"), "Linux") || !strings.Contains(checkLine(out, "login"), "tester") {
		t.Errorf("details missing:\n%s", out)
	}
}

func TestDoctorForwardingRefused(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo")), DenyTCPForwarding: true}, true)
	code, out, _ := run(t, "doctor", "testbox", "-F", h.sshConfig)
	if code != 1 || !strings.Contains(checkLine(out, "-L forwards"), "✗") || !strings.Contains(out, "AllowTcpForwarding") {
		t.Errorf("pfwd doctor against a server without forwarding: code %d:\n%s", code, out)
	}
	if !strings.Contains(checkLine(out, "-R forwards"), "!") {
		t.Errorf("-R refused should be a warning:\n%s", out)
	}
}

func TestDoctorUnknownHostKey(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux()}, false)
	code, out, _ := run(t, "doctor", "testbox", "-F", h.sshConfig)
	if code != 5 || !strings.Contains(checkLine(out, "host key"), "✗") || !strings.Contains(out, "pfwd testbox") {
		t.Errorf("pfwd doctor with an unknown host key: code %d:\n%s", code, out)
	}
	if strings.Contains(out, "detection") {
		t.Errorf("doctor went on after the host key failed:\n%s", out)
	}
	if data, _ := os.ReadFile(h.knownHosts); len(data) != 0 {
		t.Errorf("doctor wrote known_hosts: %q", data)
	}
}

func TestDoctorJSON(t *testing.T) {
	headlessEnv(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(), AllowRemoteForward: true}, true)
	code, out, _ := run(t, "doctor", "testbox", "-F", h.sshConfig, "--json")
	var got struct {
		Host   string `json:"host"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
			Fix    string `json:"fix"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("pfwd doctor --json: code %d, %v:\n%s", code, err, out)
	}
	if got.Host != "testbox" || len(got.Checks) < 6 || got.Checks[0].Name != "ssh config" || got.Checks[0].Status != "ok" {
		t.Errorf("JSON = %+v", got)
	}
}

func TestDoctorBundle(t *testing.T) {
	headlessEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	statePath := tempState(t)
	logPath := filepath.Join(filepath.Dir(statePath), "pfwd.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	logBody := `{"msg":"dial","key":"` + filepath.ToSlash(filepath.Join(home, ".ssh", "id_ed25519")) + `","password":"hunter2"}` + "\n"
	if err := os.WriteFile(logPath, []byte(logBody), 0o600); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	code, out, errOut := run(t, "doctor", "--bundle", "--output", zipPath)
	if code != 0 || !strings.Contains(out, zipPath) {
		t.Fatalf("pfwd doctor --bundle: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(data)
	}
	for _, name := range []string{"version.txt", "config.toml", "state.txt", "pfwd.log"} {
		if _, ok := files[name]; !ok {
			t.Errorf("bundle lacks %s; has %v", name, files)
		}
	}
	log := files["pfwd.log"]
	if strings.Contains(log, "hunter2") || !strings.Contains(log, "[redacted]") {
		t.Errorf("the log in the bundle keeps a secret:\n%s", log)
	}
	if strings.Contains(log, filepath.ToSlash(home)) || !strings.Contains(log, "~/.ssh/id_ed25519") {
		t.Errorf("the home directory is not replaced:\n%s", log)
	}
}

func TestRedact(t *testing.T) {
	in := "password=hunter2 passphrase: \"s3cret\" token=abc\n-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n/home/alice/x"
	got := redact(in, "/home/alice")
	for _, bad := range []string{"hunter2", "s3cret", "abc", "AAAA", "/home/alice"} {
		if strings.Contains(got, bad) {
			t.Errorf("redact kept %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "~/x") {
		t.Errorf("redact = %q", got)
	}
}
