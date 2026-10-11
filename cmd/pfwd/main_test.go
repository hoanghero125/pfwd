package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestVersionFromLdflags builds the real binary the way releases do and
// checks that `pfwd version --json` reports the injected values.
func TestVersionFromLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pfwd binary")
	}
	goBin, err := exec.LookPath("go") // go test puts GOROOT/bin first on PATH
	if err != nil {
		t.Skip("go command not found")
	}
	bin := filepath.Join(t.TempDir(), "pfwd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	const pkg = "github.com/hoanghero125/pfwd/internal/version"
	ldflags := "-s -w -X " + pkg + ".Version=1.2.3 -X " + pkg + ".Commit=0123456789abcdef0123456789abcdef01234567 -X " + pkg + ".Date=2026-10-11T09:12:00Z"
	build := exec.Command(goBin, "build", "-trimpath", "-ldflags", ldflags, "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "version", "--json").Output()
	if err != nil {
		t.Fatalf("pfwd version --json: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := map[string]string{
		"version": "1.2.3",
		"commit":  "0123456789abcdef0123456789abcdef01234567",
		"date":    "2026-10-11T09:12:00Z",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
