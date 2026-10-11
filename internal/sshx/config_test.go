package sshx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateAlias(t *testing.T) {
	good := []string{"devbox", "10.0.0.5", "::1", "host-1.example.com", "gpu_node", "my.host:alias"}
	for _, a := range good {
		if err := ValidateAlias(a); err != nil {
			t.Errorf("ValidateAlias(%q) = %v, want nil", a, err)
		}
	}
	bad := []string{"", "-oProxyCommand=x", "a b", "a;b", "$(x)", "a\nb", "`x`", "a|b", "a&b", "a'b", `a"b`, "a\\b", "a\tb", "a>b"}
	for _, a := range bad {
		if err := ValidateAlias(a); err == nil {
			t.Errorf("ValidateAlias(%q) = nil, want an error", a)
		}
	}
}

func TestParseSSHG(t *testing.T) {
	for _, name := range []string{"ssh-G-openssh-9.9-msys.txt", "ssh-G-openssh-for-windows-9.5.txt"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			opts := parseSSHG(f)
			if got := opts["hostname"]; !reflect.DeepEqual(got, []string{"devbox"}) {
				t.Errorf("hostname = %q", got)
			}
			if got := opts["user"]; !reflect.DeepEqual(got, []string{"alice"}) {
				t.Errorf("user = %q", got)
			}
			if got := opts["identityfile"]; len(got) < 6 || got[0] != "~/.ssh/id_rsa" {
				t.Errorf("identityfile = %q, want the default list starting with ~/.ssh/id_rsa", got)
			}
			if got := opts["userknownhostsfile"]; len(got) != 1 || !strings.Contains(got[0], "known_hosts2") {
				t.Errorf("userknownhostsfile = %q, want one line with two paths", got)
			}
			if got := opts["canonicalizepermittedcnames"]; got == nil {
				t.Errorf("keys are not lowercased: canonicalizePermittedcnames missing")
			}
		})
	}
}

func testEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{
		Home:        home,
		LocalUser:   "alice",
		ProgramData: filepath.Join(home, "ProgramData"),
		LookSSH:     func() (string, error) { return "", errors.New("no ssh") },
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("Run must not be called")
		},
	}
}

func TestExpandPath(t *testing.T) {
	env := testEnv(t)
	tok := tokens{alias: "dev", host: "dev.example.com", port: "2222", remoteUser: "bob"}
	sep := string(filepath.Separator)
	cases := []struct{ in, want string }{
		{"~/.ssh/id", filepath.Join(env.Home, ".ssh", "id")},
		{"~", env.Home},
		{"%d/keys/%h", filepath.Join(env.Home, "keys", "dev.example.com")},
		{"%d/keys/%u-%r-%n-%p", filepath.Join(env.Home, "keys", "alice-bob-dev-2222")},
		{"%d/keys/100%%", filepath.Join(env.Home, "keys", "100%")},
		{"%d/keys/%Z", filepath.Join(env.Home, "keys", "%Z")},
		{"", ""},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct{ in, want string }{"/c/Users/a/.ssh/k", `C:\Users\a\.ssh\k`},
			struct{ in, want string }{`__PROGRAMDATA__\ssh/ssh_known_hosts`, filepath.Join(env.ProgramData, "ssh", "ssh_known_hosts")},
			struct{ in, want string }{`C:\Users\a/.ssh/known_hosts`, `C:\Users\a\.ssh\known_hosts`},
			// /dev/null means "no file", as OpenSSH for Windows treats it.
			struct{ in, want string }{"/dev/null", os.DevNull},
			// Other drive-less POSIX paths (from an MSYS ssh) must not
			// resolve to the drive root, where any user can create files.
			struct{ in, want string }{"/etc/ssh/ssh_known_hosts", ""},
			struct{ in, want string }{`\etc\ssh\ssh_known_hosts`, ""},
			struct{ in, want string }{`\\.\pipe\openssh-ssh-agent`, `\\.\pipe\openssh-ssh-agent`},
			struct{ in, want string }{`\\server\share\key`, `\\server\share\key`},
		)
	}
	for _, c := range cases {
		if got := expandPath(c.in, tok, env); got != c.want {
			t.Errorf("expandPath(%q) = %q, want %q (sep %q)", c.in, got, c.want, sep)
		}
	}
}

func TestFromOptionsDefaults(t *testing.T) {
	env := testEnv(t)
	hc, err := fromOptions("devbox", map[string][]string{}, Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if hc.HostName != "devbox" || hc.Port != 22 || hc.User != "alice" || hc.StrictHostKeyChecking != "ask" {
		t.Errorf("defaults = %+v", hc)
	}
	if hc.ConnectTimeout != 0 || hc.IdentitiesOnly || hc.ProxyJump != "" || hc.ProxyCommand != "" {
		t.Errorf("zero values = %+v", hc)
	}
}

func TestFromOptions(t *testing.T) {
	env := testEnv(t)
	opts := map[string][]string{
		"hostname":              {"10.0.0.5"},
		"user":                  {"bob"},
		"port":                  {"2222"},
		"identityfile":          {"~/.ssh/id_ed25519", "%d/keys/%r"},
		"identitiesonly":        {"yes"},
		"identityagent":         {"none"},
		"userknownhostsfile":    {"~/.ssh/known_hosts ~/.ssh/known_hosts2"},
		"globalknownhostsfile":  {"/etc/ssh/ssh_known_hosts"},
		"stricthostkeychecking": {"accept-new"},
		"hostkeyalias":          {"devbox-key"},
		"connecttimeout":        {"5"},
		"proxyjump":             {"none"},
		"proxycommand":          {"none"},
	}
	hc, err := fromOptions("devbox", opts, Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	want := &HostConfig{
		Alias:                 "devbox",
		HostName:              "10.0.0.5",
		User:                  "bob",
		Port:                  2222,
		IdentityFiles:         []string{filepath.Join(env.Home, ".ssh", "id_ed25519"), filepath.Join(env.Home, "keys", "bob")},
		IdentitiesOnly:        true,
		IdentityAgent:         "none",
		KnownHostsFiles:       []string{filepath.Join(env.Home, ".ssh", "known_hosts"), filepath.Join(env.Home, ".ssh", "known_hosts2")},
		StrictHostKeyChecking: "accept-new",
		HostKeyAlias:          "devbox-key",
		ConnectTimeout:        5 * time.Second,
	}
	if runtime.GOOS != "windows" {
		// On Windows a drive-less POSIX path is dropped (TestExpandPath).
		want.KnownHostsFiles = append(want.KnownHostsFiles, "/etc/ssh/ssh_known_hosts")
	}
	if !reflect.DeepEqual(hc, want) {
		t.Errorf("fromOptions =\n%+v\nwant\n%+v", hc, want)
	}
}

func TestFromOptionsOverrides(t *testing.T) {
	env := testEnv(t)
	if err := os.WriteFile(filepath.Join(env.Home, "k"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := map[string][]string{"user": {"bob"}, "port": {"2222"}, "identityfile": {"~/.ssh/id_rsa"}}
	hc, err := fromOptions("devbox", opts, Overrides{User: "carol", Port: 2200, Identities: []string{"~/k"}}, env)
	if err != nil {
		t.Fatal(err)
	}
	if hc.User != "carol" || hc.Port != 2200 {
		t.Errorf("user/port = %q/%d, want carol/2200", hc.User, hc.Port)
	}
	if want := []string{filepath.Join(env.Home, "k"), filepath.Join(env.Home, ".ssh", "id_rsa")}; !reflect.DeepEqual(hc.IdentityFiles, want) {
		t.Errorf("IdentityFiles = %q, want %q", hc.IdentityFiles, want)
	}
}

// TestFromOptionsMissingIdentity: a key named with -i must exist, unlike
// the IdentityFile defaults, which ssh skips when absent.
func TestFromOptionsMissingIdentity(t *testing.T) {
	env := testEnv(t)
	opts := map[string][]string{"identityfile": {"~/.ssh/id_absent"}}
	_, err := fromOptions("devbox", opts, Overrides{Identities: []string{"~/.ssh/id_typo"}}, env)
	want := filepath.Join(env.Home, ".ssh", "id_typo")
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "-i") {
		t.Errorf("fromOptions with a missing -i file: %v; want an error naming %s and -i", err, want)
	}
	if _, err := fromOptions("devbox", opts, Overrides{}, env); err != nil {
		t.Errorf("a missing IdentityFile from the ssh config must not fail: %v", err)
	}
}

func TestFromOptionsKnownHostsSettings(t *testing.T) {
	env := testEnv(t)
	hc, err := fromOptions("h", map[string][]string{"hashknownhosts": {"yes"}, "userknownhostsfile": {"none"}, "globalknownhostsfile": {"~/global"}}, Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if !hc.HashKnownHosts || !hc.NoUserKnownHosts {
		t.Errorf("HashKnownHosts %v, NoUserKnownHosts %v; want both", hc.HashKnownHosts, hc.NoUserKnownHosts)
	}
	hc, err = fromOptions("h", map[string][]string{"hashknownhosts": {"no"}, "userknownhostsfile": {"~/kh"}}, Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if hc.HashKnownHosts || hc.NoUserKnownHosts {
		t.Errorf("HashKnownHosts %v, NoUserKnownHosts %v; want neither", hc.HashKnownHosts, hc.NoUserKnownHosts)
	}
}

func TestFromOptionsStrictValues(t *testing.T) {
	env := testEnv(t)
	for in, want := range map[string]string{"yes": "yes", "true": "yes", "no": "no", "off": "no", "false": "no", "accept-new": "accept-new", "ask": "ask", "": "ask"} {
		hc, err := fromOptions("h", map[string][]string{"stricthostkeychecking": {in}}, Overrides{}, env)
		if err != nil {
			t.Fatal(err)
		}
		if hc.StrictHostKeyChecking != want {
			t.Errorf("StrictHostKeyChecking %q → %q, want %q", in, hc.StrictHostKeyChecking, want)
		}
	}
}

func TestFromOptionsDevNullKnownHosts(t *testing.T) {
	env := testEnv(t)
	opts := map[string][]string{
		"userknownhostsfile":   {"/dev/null"},
		"globalknownhostsfile": {"/etc/ssh/ssh_known_hosts"},
	}
	hc, err := fromOptions("vagrant", opts, Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{os.DevNull, filepath.FromSlash("/etc/ssh/ssh_known_hosts")}
	if runtime.GOOS == "windows" {
		// The POSIX global file doesn't exist on Windows and is dropped.
		want = []string{os.DevNull}
	}
	if !reflect.DeepEqual(hc.KnownHostsFiles, want) {
		t.Errorf("KnownHostsFiles = %q, want %q", hc.KnownHostsFiles, want)
	}
}

func TestFromOptionsBadPort(t *testing.T) {
	env := testEnv(t)
	for _, p := range []string{"abc", "0", "70000"} {
		if _, err := fromOptions("h", map[string][]string{"port": {p}}, Overrides{}, env); err == nil {
			t.Errorf("port %q accepted", p)
		}
	}
}

func TestResolveUsesSSHG(t *testing.T) {
	env := testEnv(t)
	var gotArgs []string
	env.LookSSH = func() (string, error) { return "/usr/bin/ssh", nil }
	env.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "/usr/bin/ssh" {
			t.Errorf("ran %q", name)
		}
		gotArgs = args
		return []byte("hostname 10.0.0.5\nuser bob\nport 2222\nidentityfile ~/.ssh/id_ed25519\n"), nil
	}
	hc, err := Resolve(t.Context(), "devbox", Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-G", "--", "devbox"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %q, want %q", gotArgs, want)
	}
	if hc.HostName != "10.0.0.5" || hc.User != "bob" || hc.Port != 2222 || hc.Alias != "devbox" {
		t.Errorf("hc = %+v", hc)
	}

	if _, err := Resolve(t.Context(), "devbox", Overrides{ConfigFile: "cfg"}, env); err != nil {
		t.Fatal(err)
	}
	if want := []string{"-G", "-F", "cfg", "--", "devbox"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args with -F = %q, want %q", gotArgs, want)
	}
}

func TestResolveSSHGFailure(t *testing.T) {
	env := testEnv(t)
	env.LookSSH = func() (string, error) { return "ssh", nil }
	env.Run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("exit status 255: line 3: Bad configuration option: hostnme")
	}
	_, err := Resolve(t.Context(), "devbox", Overrides{}, env)
	if err == nil || !strings.Contains(err.Error(), "hostnme") || !strings.Contains(err.Error(), "ssh -G devbox") {
		t.Errorf("err = %v, want the ssh error and a hint naming `ssh -G devbox`", err)
	}
}

func TestResolveFallsBackToParser(t *testing.T) {
	env := testEnv(t)
	sshDir := filepath.Join(env.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "Host devbox\n  HostName 10.0.0.5\n  Port 2222\n  IdentityFile ~/k\n\nHost *\n  User fallback\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	hc, err := Resolve(t.Context(), "devbox", Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if hc.HostName != "10.0.0.5" || hc.Port != 2222 || hc.User != "fallback" {
		t.Errorf("hc = %+v", hc)
	}
	if want := []string{filepath.Join(env.Home, "k")}; !reflect.DeepEqual(hc.IdentityFiles, want) {
		t.Errorf("IdentityFiles = %q, want %q", hc.IdentityFiles, want)
	}
	if len(hc.KnownHostsFiles) == 0 || hc.KnownHostsFiles[0] != filepath.Join(sshDir, "known_hosts") {
		t.Errorf("KnownHostsFiles = %q", hc.KnownHostsFiles)
	}

	other, err := Resolve(t.Context(), "elsewhere", Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if other.HostName != "elsewhere" || other.Port != 22 || len(other.IdentityFiles) < 3 {
		t.Errorf("unmatched host = %+v, want hostname=alias, port 22 and the default identity files", other)
	}
}

func TestResolveFallbackNoConfigFile(t *testing.T) {
	env := testEnv(t)
	hc, err := Resolve(t.Context(), "devbox", Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if hc.HostName != "devbox" || hc.User != "alice" {
		t.Errorf("hc = %+v", hc)
	}
}

func TestResolveExplicitConfigFileMissing(t *testing.T) {
	env := testEnv(t)
	_, err := Resolve(t.Context(), "devbox", Overrides{ConfigFile: filepath.Join(env.Home, "nope")}, env)
	if err == nil {
		t.Fatal("missing -F file accepted")
	}
}

func TestResolveUserAtHost(t *testing.T) {
	env := testEnv(t)
	hc, err := Resolve(t.Context(), "bob@devbox", Overrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if hc.User != "bob" || hc.Alias != "devbox" || hc.HostName != "devbox" {
		t.Errorf("hc = %+v", hc)
	}
	hc, err = Resolve(t.Context(), "bob@devbox", Overrides{User: "carol"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if hc.User != "carol" {
		t.Errorf("-l should win over user@: got %q", hc.User)
	}
	for _, bad := range []string{"@devbox", "bob@", "b;b@devbox", "bob@-x"} {
		if _, err := Resolve(t.Context(), bad, Overrides{}, env); err == nil {
			t.Errorf("Resolve(%q) accepted", bad)
		}
	}
}

func TestSplitPaths(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"~/.ssh/known_hosts ~/.ssh/known_hosts2", []string{"~/.ssh/known_hosts", "~/.ssh/known_hosts2"}},
		{`C:\Users\John Doe/.ssh/known_hosts C:\Users\John Doe/.ssh/known_hosts2`, []string{`C:\Users\John Doe/.ssh/known_hosts`, `C:\Users\John Doe/.ssh/known_hosts2`}},
		{`__PROGRAMDATA__\ssh/ssh_known_hosts __PROGRAMDATA__\ssh/ssh_known_hosts2`, []string{`__PROGRAMDATA__\ssh/ssh_known_hosts`, `__PROGRAMDATA__\ssh/ssh_known_hosts2`}},
		{"/home/a b/kh none", []string{"/home/a b/kh", "none"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := splitPaths(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitPaths(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
