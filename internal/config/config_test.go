package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func problems(t *testing.T, err error) []Problem {
	t.Helper()
	var p *Problems
	if !errors.As(err, &p) {
		t.Fatalf("err = %v (%T), want *Problems", err, err)
	}
	return p.List
}

func TestDefault(t *testing.T) {
	c := Default()
	if c.Detect.Interval != 2*time.Second || !c.Detect.HTTPProbe || c.Detect.DebounceScans != 2 {
		t.Errorf("detect = %+v", c.Detect)
	}
	if !reflect.DeepEqual(c.Detect.IgnorePorts, detect.PortSet{{Lo: 22, Hi: 22}}) || len(c.Detect.IgnoreProcesses) != 7 {
		t.Errorf("ignore lists = %v %v", c.Detect.IgnorePorts, c.Detect.IgnoreProcesses)
	}
	f := c.Forward
	if !f.Auto || len(f.AutoPorts) != 0 || f.Bind != "127.0.0.1" || f.OnConflict != ConflictAsk ||
		f.PrivilegedOffset != 10000 || f.AutoExpire != 10*time.Minute || f.MaxConns != 256 || !f.Restore {
		t.Errorf("forward = %+v", f)
	}
	s := c.SSH
	if !s.UseOpenSSHConfig || s.KeepaliveInterval != 15*time.Second || s.KeepaliveCountMax != 3 ||
		s.ConnectTimeout != 10*time.Second || s.ReconnectMaxBackoff != 30*time.Second {
		t.Errorf("ssh = %+v", s)
	}
	u := c.UI
	if u.Theme != ThemeAuto || !u.Mouse || !u.ConfirmQuit || !u.Notifications || u.ShowHidden || u.Glyphs != GlyphsAuto {
		t.Errorf("ui = %+v", u)
	}
	if len(c.Labels) != 0 || len(c.Hosts) != 0 {
		t.Errorf("labels %v hosts %v, want none", c.Labels, c.Hosts)
	}
}

func TestLoadMissingFileMeansDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Errorf("Load of a missing file = %+v, want defaults", c)
	}
}

const fullExample = `[detect]
interval = "1s"
http_probe = false
debounce_scans = 3
ignore_ports = [22, "9000-9100"]
ignore_processes = ["sshd"]

[forward]
auto = false
auto_ports = ["3000-3999", 5173, 8888]
bind = "0.0.0.0"
on_conflict = "next"
privileged_offset = 20000
auto_expire = "5m"
max_conns = 64
restore = false

[ssh]
use_openssh_config = false
keepalive_interval = "20s"
keepalive_count_max = 5
connect_timeout = "4s"
reconnect_max_backoff = "1m"

[ui]
theme = "light"
mouse = false
confirm_quit = false
notifications = false
show_hidden = true
glyphs = "ascii"

[labels]
8000 = "API"
"5173" = "Frontend"

# Per-host overrides, matched against the ssh config alias
[hosts.devbox]
auto = true
forwards = ["5432", "15432:db.internal:5432", "-D 1080"]

[hosts.gpu-node]
auto_ports = [8888, 6006]

[hosts."my.box"]
auto_ports = []
`

func TestLoadFullExample(t *testing.T) {
	c, err := Load(writeConfig(t, fullExample))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Detect: Detect{Interval: time.Second, DebounceScans: 3,
			IgnorePorts: detect.PortSet{{Lo: 22, Hi: 22}, {Lo: 9000, Hi: 9100}}, IgnoreProcesses: []string{"sshd"}},
		Forward: Forward{AutoPorts: detect.PortSet{{Lo: 3000, Hi: 3999}, {Lo: 5173, Hi: 5173}, {Lo: 8888, Hi: 8888}},
			Bind: "0.0.0.0", OnConflict: ConflictNext, PrivilegedOffset: 20000, AutoExpire: 5 * time.Minute, MaxConns: 64},
		SSH:    SSH{KeepaliveInterval: 20 * time.Second, KeepaliveCountMax: 5, ConnectTimeout: 4 * time.Second, ReconnectMaxBackoff: time.Minute},
		UI:     UI{Theme: ThemeLight, ShowHidden: true, Glyphs: GlyphsASCII},
		Labels: map[uint16]string{8000: "API", 5173: "Frontend"},
		Hosts: map[string]Host{
			"devbox": {Auto: ptr(true), Forwards: []ForwardRule{
				{Kind: "local", Local: 5432, Remote: 5432, Raw: "5432"},
				{Kind: "local", Local: 15432, RemoteHost: "db.internal", Remote: 5432, Raw: "15432:db.internal:5432"},
				{Kind: "dynamic", Local: 1080, Raw: "-D 1080"},
			}},
			"gpu-node": {AutoPorts: detect.PortSet{{Lo: 8888, Hi: 8888}, {Lo: 6006, Hi: 6006}}, HasAutoPorts: true},
			"my.box":   {AutoPorts: detect.PortSet{}, HasAutoPorts: true},
		},
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("Load =\n%+v\nwant\n%+v", c, want)
	}
}

func ptr[T any](v T) *T { return &v }

func TestLoadUnknownKeys(t *testing.T) {
	_, err := Load(writeConfig(t, `[detect]
intervall = "2s"

[bogus]
x = 1

[hosts.devbox]
autoports = [1]
`))
	got := problems(t, err)
	want := []Problem{
		{Line: 2, Col: 1, Key: "detect.intervall", Msg: "unknown setting"},
		{Line: 4, Col: 2, Key: "bogus", Msg: "unknown setting"},
		{Line: 8, Col: 1, Key: "hosts.devbox.autoports", Msg: "unknown setting"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems =\n%+v\nwant\n%+v", got, want)
	}
}

func TestLoadBadValuesAreAllReported(t *testing.T) {
	_, err := Load(writeConfig(t, `[detect]
interval = "2 seconds"
debounce_scans = 0
ignore_ports = ["x"]

[forward]
auto_ports = [1.5, "3999-3000", 70000]
bind = "nonsense"
on_conflict = "maybe"
max_conns = 0

[ui]
theme = "blue"

[labels]
abc = "x"

[hosts.devbox]
auto_ports = ["a"]
forwards = ["bogus spec"]
`))
	got := problems(t, err)
	wantKeys := []string{
		"detect.interval", "detect.debounce_scans", "detect.ignore_ports",
		"forward.auto_ports", "forward.auto_ports", "forward.auto_ports",
		"forward.bind", "forward.on_conflict", "forward.max_conns",
		"ui.theme", "labels.abc", "hosts.devbox.auto_ports", "hosts.devbox.forwards",
	}
	var keys []string
	for _, p := range got {
		keys = append(keys, p.Key)
		if p.Line == 0 || p.Col == 0 || p.Msg == "" {
			t.Errorf("problem without a position or message: %+v", p)
		}
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("problem keys =\n%q\nwant\n%q", keys, wantKeys)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Line < got[i-1].Line {
			t.Errorf("problems are not sorted by line: %+v", got)
		}
	}
	if !strings.Contains(got[0].Msg, "2 seconds") || !strings.Contains(got[0].Msg, "2s") {
		t.Errorf("duration message should quote the value and show an example: %q", got[0].Msg)
	}
}

func TestLoadWrongType(t *testing.T) {
	_, err := Load(writeConfig(t, "[detect]\ndebounce_scans = \"x\"\n"))
	got := problems(t, err)
	if len(got) != 1 || got[0].Line != 2 || got[0].Key != "detect.debounce_scans" || !strings.Contains(got[0].Msg, "integer") {
		t.Errorf("problems = %+v", got)
	}
	if strings.Contains(got[0].Msg, "struct field") {
		t.Errorf("message leaks Go types: %q", got[0].Msg)
	}
}

func TestLoadSyntaxError(t *testing.T) {
	_, err := Load(writeConfig(t, "[detect]\ninterval = \n"))
	got := problems(t, err)
	if len(got) != 1 || got[0].Line != 2 {
		t.Errorf("problems = %+v", got)
	}
}

func TestLoadBOMAndCRLF(t *testing.T) {
	c, err := Load(writeConfig(t, "\xEF\xBB\xBF[detect]\r\ninterval = \"3s\"\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Detect.Interval != 3*time.Second {
		t.Errorf("interval = %v", c.Detect.Interval)
	}
}

func TestProblemsError(t *testing.T) {
	path := writeConfig(t, "[detect]\ninterval = \"x\"\ndebounce_scans = 0\n")
	_, err := Load(path)
	msg := err.Error()
	if !strings.Contains(msg, path+":2:") || !strings.Contains(msg, "1 more") || !strings.Contains(msg, "pfwd config validate") {
		t.Errorf("Error() = %q", msg)
	}
	var p *Problems
	errors.As(err, &p)
	lines := p.Lines()
	if len(lines) != 2 || !strings.HasPrefix(lines[0], path+":2:12: detect.interval: ") {
		t.Errorf("Lines() = %q", lines)
	}
}

func TestPathOverride(t *testing.T) {
	t.Setenv("PFWD_CONFIG", filepath.Join("x", "my.toml"))
	if p, err := Path(); err != nil || p != filepath.Join("x", "my.toml") {
		t.Errorf("Path = %q, %v", p, err)
	}
	t.Setenv("PFWD_CONFIG", "")
	p, err := Path()
	if err != nil || !strings.HasSuffix(p, filepath.Join("pfwd", "config.toml")) {
		t.Errorf("Path = %q, %v", p, err)
	}
}

func TestHostSettings(t *testing.T) {
	c, err := Load(writeConfig(t, fullExample))
	if err != nil {
		t.Fatal(err)
	}
	if !c.AutoFor("devbox") || c.AutoFor("elsewhere") || c.AutoFor("gpu-node") {
		t.Errorf("AutoFor: devbox %v elsewhere %v gpu-node %v; want true false false (global auto is false)",
			c.AutoFor("devbox"), c.AutoFor("elsewhere"), c.AutoFor("gpu-node"))
	}
	f := c.FilterFor("gpu-node")
	if !reflect.DeepEqual(f.AutoPorts, detect.PortSet{{Lo: 8888, Hi: 8888}, {Lo: 6006, Hi: 6006}}) {
		t.Errorf("gpu-node auto ports = %v", f.AutoPorts)
	}
	if got := c.FilterFor("my.box").AutoPorts; len(got) != 0 {
		t.Errorf("an empty host list means every port, got %v", got)
	}
	if got := c.FilterFor("elsewhere").AutoPorts; len(got) != 3 {
		t.Errorf("hosts without an override use forward.auto_ports, got %v", got)
	}
	if !c.FilterFor("x").Ignored(detect.RemotePort{Port: 9050}) {
		t.Error("detect.ignore_ports ranges apply to every host")
	}
}

func TestUnapplied(t *testing.T) {
	if notes := Default().Unapplied(); len(notes) != 0 {
		t.Errorf("defaults have notes: %q", notes)
	}
	c, err := Load(writeConfig(t, fullExample))
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(c.Unapplied(), "\n")
	for _, key := range []string{"forward.privileged_offset", "hosts.devbox.forwards"} {
		if !strings.Contains(notes, key) {
			t.Errorf("notes lack %s:\n%s", key, notes)
		}
	}
	// v0.3.0 applies these, and the local forwards of hosts.devbox.
	for _, key := range []string{"auto_expire", "restore", "keepalive", "reconnect_max_backoff", "15432"} {
		if strings.Contains(notes, key) {
			t.Errorf("notes still mention %s:\n%s", key, notes)
		}
	}
	if !strings.Contains(notes, "-D 1080") {
		t.Errorf("the note for hosts.devbox.forwards should name the -D entry:\n%s", notes)
	}
}

func TestTemplateIsValidAndDefault(t *testing.T) {
	c, err := Load(writeConfig(t, Template()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Errorf("the template does not decode to the defaults:\n%+v", c)
	}
}

func TestLoadWrongTypeDoesNotHideOthers(t *testing.T) {
	cfg := "[detect]\ndebounce_scans = \"x\"\nintervl = \"2s\"\n\n[forward]\nmax_conns = true\non_conflict = \"maybe\"\nauto_ports = [\n  3000,\n  \"bad\",\n]\n"
	_, err := Load(writeConfig(t, cfg))
	got := problems(t, err)
	var keys []string
	for _, p := range got {
		keys = append(keys, p.Key)
	}
	want := []string{"detect.debounce_scans", "detect.intervl", "forward.max_conns", "forward.on_conflict", "forward.auto_ports"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("problem keys = %q, want %q (all of them, by line)", keys, want)
	}
	if len(got) == len(want) && (got[2].Line != 6 || got[1].Line != 3) {
		t.Errorf("positions = %+v, want the lines in the file", got)
	}
	for _, p := range got {
		if strings.Contains(p.Msg, "a integer") || strings.Contains(p.Msg, "a array") {
			t.Errorf("wrong article: %q", p.Msg)
		}
	}
}
