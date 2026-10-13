package sshx

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigHosts(t *testing.T) {
	env := testEnv(t)
	sshDir := filepath.Join(env.Home, ".ssh")
	writeFile(t, filepath.Join(sshDir, "conf.d", "work"), "Host gpu-node\n  HostName 10.0.0.9\nHost devbox\n")
	writeFile(t, filepath.Join(sshDir, "config"), "# my hosts\n"+
		"Host devbox staging *.internal !bad web?\n  User me\n"+
		"Include conf.d/*\n"+
		"Host=bastion\n"+
		"  host  a5000_ts\t# a comment\n"+
		"Match host foo\n  User x\n"+
		"Host *\n  ServerAliveInterval 10\n")
	got, err := ConfigHosts("", env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"devbox", "staging", "gpu-node", "bastion", "a5000_ts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigHosts = %q, want %q", got, want)
	}
}

func TestConfigHostsFileFlagAndLoops(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(t.TempDir(), "ssh_config")
	// A file that includes itself must not loop.
	writeFile(t, path, "Host one\nInclude "+filepath.ToSlash(path)+"\nHost two\n")
	got, err := ConfigHosts(path, env)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigHosts = %q, want %q", got, want)
	}
	if got, err := ConfigHosts("", testEnv(t)); err != nil || len(got) != 0 {
		t.Errorf("without a config file: %q, %v; want none and no error", got, err)
	}
}

// TestConfigHostsSkipsDirectories: an Include glob that also matches a
// directory reads it as empty, as OpenSSH does.
func TestConfigHostsSkipsDirectories(t *testing.T) {
	env := testEnv(t)
	sshDir := filepath.Join(env.Home, ".ssh")
	writeFile(t, filepath.Join(sshDir, "conf.d", "work"), "Host gpu-node\n")
	if err := os.MkdirAll(filepath.Join(sshDir, "conf.d", "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sshDir, "config"), "Include conf.d/*\nHost devbox\n")
	got, err := ConfigHosts("", env)
	if want := []string{"gpu-node", "devbox"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigHosts = %q, %v; want %q", got, err, want)
	}
}
