package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStateDir(t *testing.T) {
	home := filepath.FromSlash("/home/alice")
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	homeDir := func() (string, error) { return home, nil }
	xdg := filepath.Join(t.TempDir(), "state") // absolute on every OS
	cases := []struct {
		goos string
		vars map[string]string
		want string
	}{
		{"linux", nil, filepath.Join(home, ".local", "state", "pfwd")},
		{"linux", map[string]string{"XDG_STATE_HOME": xdg}, filepath.Join(xdg, "pfwd")},
		{"linux", map[string]string{"XDG_STATE_HOME": "relative/path"}, filepath.Join(home, ".local", "state", "pfwd")},
		{"freebsd", nil, filepath.Join(home, ".local", "state", "pfwd")},
		{"darwin", nil, filepath.Join(home, "Library", "Application Support", "pfwd")},
		{"windows", map[string]string{"APPDATA": filepath.FromSlash("/Users/alice/AppData/Roaming")}, filepath.Join(filepath.FromSlash("/Users/alice/AppData/Roaming"), "pfwd")},
		{"windows", nil, filepath.Join(home, "AppData", "Roaming", "pfwd")},
	}
	for _, c := range cases {
		got, err := stateDir(c.goos, env(c.vars), homeDir)
		if err != nil || got != c.want {
			t.Errorf("stateDir(%s, %v) = %q, %v; want %q", c.goos, c.vars, got, err, c.want)
		}
	}
	if _, err := stateDir("linux", env(nil), func() (string, error) { return "", errors.New("no home") }); err == nil {
		t.Error("stateDir without a home directory succeeded")
	}
}
