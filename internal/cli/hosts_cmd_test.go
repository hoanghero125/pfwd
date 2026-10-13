package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/state"
)

// tempState points pfwd's state directory at a temporary one and returns
// the state.json path.
func tempState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)
	path, err := config.StatePath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// sshConfigFile writes an ssh config and returns its path, for -F.
func sshConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh_config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHostsCmd(t *testing.T) {
	writeConfig(t, "")
	statePath := tempState(t)
	now := time.Now()
	saveAt := func(host string, at time.Time, n int) {
		s := state.Open(statePath, state.Options{Now: func() time.Time { return at }})
		fs := make([]state.Forward, n)
		for i := range fs {
			fs[i] = state.Forward{Kind: "local", Local: uint16(5000 + i), RemoteHost: "127.0.0.1", Remote: uint16(5000 + i)}
		}
		if err := s.Save(host, fs); err != nil {
			t.Fatal(err)
		}
	}
	saveAt("old-host", now.Add(-50*time.Hour), 0)
	saveAt("devbox", now.Add(-2*time.Hour), 2)
	cfg := sshConfigFile(t, "Host gpu-node devbox\nHost alpha\n")

	code, out, errOut := run(t, "hosts", "-F", cfg)
	if code != 0 || errOut != "" {
		t.Fatalf("pfwd hosts: code %d, stderr %q", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var names []string
	for _, l := range lines[1:] {
		names = append(names, strings.Fields(l)[0])
	}
	if strings.Join(names, " ") != "devbox old-host alpha gpu-node" {
		t.Errorf("order = %q, want recent first, then never used by name:\n%s", names, out)
	}
	if !strings.Contains(lines[1], "2h ago") || !strings.Contains(lines[3], "never") {
		t.Errorf("last used column:\n%s", out)
	}

	code, out, _ = run(t, "hosts", "-F", cfg, "--json")
	var got []struct {
		Host     string  `json:"host"`
		LastUsed *string `json:"last_used"`
		Forwards int     `json:"forwards"`
	}
	if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil {
		t.Fatalf("pfwd hosts --json: code %d, %v:\n%s", code, err, out)
	}
	if len(got) != 4 || got[0].Host != "devbox" || got[0].Forwards != 2 || got[0].LastUsed == nil || got[3].LastUsed != nil {
		t.Errorf("JSON = %+v", got)
	}
}

func TestAgo(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{2 * time.Hour, "2h ago"},
		{50 * time.Hour, "2d ago"},
	} {
		if got := ago(c.d); got != c.want {
			t.Errorf("ago(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestHostCompletion(t *testing.T) {
	writeConfig(t, "")
	tempState(t)
	cfg := sshConfigFile(t, "Host devbox gpu-node\nHost *\n  User me\n")
	for _, args := range [][]string{
		{"__complete", "-F", cfg, "de"},
		{"__complete", "doctor", "-F", cfg, ""},
	} {
		code, out, _ := run(t, args...)
		if code != 0 || !strings.Contains(out, "devbox") || !strings.Contains(out, ":4") {
			t.Errorf("pfwd %q: code %d:\n%s", args, code, out)
		}
		if args[len(args)-1] == "de" && strings.Contains(out, "gpu-node") {
			t.Errorf("completion of \"de\" offered gpu-node:\n%s", out)
		}
	}
}
