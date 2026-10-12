package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// fakeNotifier stands in for the desktop notifier.
type fakeNotifier struct{}

func (fakeNotifier) Notify(context.Context, string, string) error { return nil }

func TestEngineOptionsFromConfig(t *testing.T) {
	no := false
	cfg := config.Default()
	cfg.Detect.Interval = 3 * time.Second
	cfg.Detect.DebounceScans = 4
	cfg.Detect.HTTPProbe = false
	cfg.Labels = map[uint16]string{3000: "API"}
	cfg.Forward.OnConflict = config.ConflictFail
	cfg.Forward.Bind = "0.0.0.0"
	cfg.Forward.MaxConns = 10
	cfg.Forward.AutoPorts = detect.PortSet{{Lo: 3000, Hi: 3999}}
	cfg.Hosts = map[string]config.Host{"devbox": {Auto: &no, AutoPorts: detect.PortSet{{Lo: 8888, Hi: 8888}}, HasAutoPorts: true}}

	o := engineOptions(cfg, nil, fakeNotifier{})
	if o.Interval != 3*time.Second || o.Debounce != 4 || o.HTTPProbe || o.Labels[3000] != "API" ||
		o.OnConflict != engine.ConflictFail || o.BindAddr != "0.0.0.0" || o.MaxConns != 10 || o.Notifier == nil {
		t.Errorf("engineOptions = %+v", o)
	}
	devbox, other := o.Settings("devbox"), o.Settings("gpu")
	if devbox.Auto || !other.Auto {
		t.Errorf("auto: devbox %v, gpu %v; want false (host override), true", devbox.Auto, other.Auto)
	}
	if !devbox.Filter.AutoPorts.Contains(8888) || devbox.Filter.AutoPorts.Contains(3000) || !other.Filter.AutoPorts.Contains(3000) {
		t.Errorf("auto_ports: devbox %v, gpu %v", devbox.Filter.AutoPorts, other.Filter.AutoPorts)
	}
	if !devbox.Filter.IgnorePorts.Contains(22) {
		t.Errorf("ignore_ports lost: %v", devbox.Filter.IgnorePorts)
	}

	yes := true
	if o := engineOptions(cfg, &yes, nil); !o.Settings("devbox").Auto || o.Notifier != nil {
		t.Errorf("--auto must win over the host's auto = false; notifier %v", o.Notifier)
	}
}

func TestTUIOptionsFromConfig(t *testing.T) {
	cfg := config.Default()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	o := tuiOptions(cfg, env(nil), "linux")
	if o.Theme != "" || !o.Mouse || o.SkipQuitConfirm || o.ShowHidden || o.ASCII || o.BindAddr != "127.0.0.1" || o.Open == nil || o.Copy == nil {
		t.Errorf("defaults = %+v", o)
	}

	cfg.UI = config.UI{Theme: config.ThemeLight, Mouse: false, ConfirmQuit: false, ShowHidden: true, Glyphs: config.GlyphsASCII}
	cfg.Forward.OnConflict = config.ConflictFail
	o = tuiOptions(cfg, env(nil), "linux")
	if o.Theme != "light" || o.Mouse || !o.SkipQuitConfirm || !o.ShowHidden || !o.ASCII || o.OnConflict != "fail" {
		t.Errorf("from config = %+v", o)
	}

	cfg.UI.Glyphs = config.GlyphsAuto
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want bool
	}{
		{"windows", nil, true}, // the legacy console
		{"windows", map[string]string{"WT_SESSION": "x"}, false},
		{"windows", map[string]string{"TERM_PROGRAM": "vscode"}, false},
		{"windows", map[string]string{"TERM": "xterm-256color"}, false},
		{"linux", nil, false},
	} {
		if got := tuiOptions(cfg, env(tc.env), tc.goos).ASCII; got != tc.want {
			t.Errorf("glyphs auto on %s with %v: ASCII %v, want %v", tc.goos, tc.env, got, tc.want)
		}
	}
	cfg.UI.Glyphs = config.GlyphsUnicode
	if tuiOptions(cfg, env(nil), "windows").ASCII {
		t.Error("glyphs = unicode still drew ASCII")
	}
}

func TestNoColor(t *testing.T) {
	for _, tc := range []struct {
		noColor string
		flag    bool
		want    bool
	}{
		{"", false, false},
		{"1", false, true},
		{"please", false, true}, // any value counts, per no-color.org
		{"", true, true},
	} {
		if got := noColor(tc.noColor, tc.flag); got != tc.want {
			t.Errorf("noColor(NO_COLOR=%q, --no-color=%v) = %v, want %v", tc.noColor, tc.flag, got, tc.want)
		}
	}
}

func TestSSHSettingsFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.SSH.UseOpenSSHConfig = false
	cfg.SSH.ConnectTimeout = 7 * time.Second
	env := sshEnv(cfg)
	if _, err := env.LookSSH(); err == nil {
		t.Error("use_openssh_config = false still runs ssh -G")
	}
	hc := &sshx.HostConfig{}
	applySSHConfig(hc, cfg)
	if hc.ConnectTimeout != 7*time.Second {
		t.Errorf("ConnectTimeout = %v, want the config's 7s", hc.ConnectTimeout)
	}
	hc = &sshx.HostConfig{ConnectTimeout: 3 * time.Second}
	applySSHConfig(hc, cfg)
	if hc.ConnectTimeout != 3*time.Second {
		t.Errorf("ConnectTimeout = %v; the ssh config's 3s must win", hc.ConnectTimeout)
	}
}

// given reports the named flags as set on the command line.
func given(names ...string) func(string) bool {
	return func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
}

func TestFlagsOverrideConfig(t *testing.T) {
	f := rootFlags{bind: "0.0.0.0", interval: time.Second, auto: true}
	cfg := config.Default()
	auto, err := f.applyTo(&cfg, given("bind", "interval", "auto"))
	if err != nil || cfg.Forward.Bind != "0.0.0.0" || cfg.Detect.Interval != time.Second || auto == nil || !*auto {
		t.Errorf("applyTo = %v, %v; config bind %q, interval %v", auto, err, cfg.Forward.Bind, cfg.Detect.Interval)
	}
	f = rootFlags{noAuto: true}
	if auto, err := f.applyTo(&cfg, given("no-auto")); err != nil || auto == nil || *auto {
		t.Errorf("--no-auto = %v, %v", auto, err)
	}
	if auto, _ := (&rootFlags{}).applyTo(&cfg, given()); auto != nil {
		t.Errorf("no flag gave auto %v", *auto)
	}
}

func TestExplicitFlagValuesCount(t *testing.T) {
	cfg := config.Default()
	if auto, err := (&rootFlags{auto: false}).applyTo(&cfg, given("auto")); err != nil || auto == nil || *auto {
		t.Errorf("--auto=false = %v, %v; want auto off", auto, err)
	}
	if auto, err := (&rootFlags{noAuto: false}).applyTo(&cfg, given("no-auto")); err != nil || auto == nil || !*auto {
		t.Errorf("--no-auto=false = %v, %v; want auto on", auto, err)
	}
	if _, err := (&rootFlags{auto: true, noAuto: true}).applyTo(&cfg, given("auto", "no-auto")); err == nil {
		t.Error("--auto --no-auto was accepted")
	}
	if _, err := (&rootFlags{auto: true, noAuto: false}).applyTo(&cfg, given("auto", "no-auto")); err != nil {
		t.Errorf("--auto --no-auto=false agree, but: %v", err)
	}
	if _, err := (&rootFlags{interval: 0}).applyTo(&cfg, given("interval")); err == nil {
		t.Error("--interval 0 was accepted")
	}
	if _, err := (&rootFlags{bind: ""}).applyTo(&cfg, given("bind")); err == nil {
		t.Error(`--bind "" was accepted`)
	}
}

func TestBadFlagValuesAreUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"devbox", "--auto", "--no-auto"}, "--auto"},
		{[]string{"devbox", "--bind", "my-laptop"}, "--bind"},
		{[]string{"devbox", "--interval", "100ms"}, "--interval"},
	} {
		code, _, errOut := run(t, tc.args...)
		if code != 2 || !strings.Contains(errOut, tc.want) {
			t.Errorf("pfwd %v: code %d, stderr %q; want 2 naming %s", tc.args, code, errOut, tc.want)
		}
	}
}

func TestEngineOptionsResilience(t *testing.T) {
	cfg := config.Default()
	cfg.SSH.ReconnectMaxBackoff = 45 * time.Second
	cfg.Forward.AutoExpire = 5 * time.Minute
	rules := []config.ForwardRule{}
	for _, raw := range []string{"5432", "0.0.0.0:15432:db.internal:5432", "-D 1080"} {
		r, err := config.ParseForward(raw)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, r)
	}
	cfg.Hosts = map[string]config.Host{"devbox": {Forwards: rules}}
	o := engineOptions(cfg, nil, nil)
	if o.MaxBackoff != 45*time.Second || o.AutoExpire != 5*time.Minute {
		t.Errorf("MaxBackoff %v, AutoExpire %v; want 45s and 5m", o.MaxBackoff, o.AutoExpire)
	}
	got := o.Settings("devbox").Forwards
	want := []engine.ForwardSpec{
		{Kind: engine.Local, LocalPort: 5432, RemotePort: 5432},
		{Kind: engine.Local, BindAddr: "0.0.0.0", LocalPort: 15432, RemoteHost: "db.internal", RemotePort: 5432},
		{Kind: engine.Dynamic, LocalPort: 1080},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("config forwards = %+v, want %+v", got, want)
	}
	if fs := o.Settings("gpu").Forwards; len(fs) != 0 {
		t.Errorf("a host without forwards got %+v", fs)
	}
}

func TestStateStoreFollowsRestore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("APPDATA", dir)
	cfg := config.Default()
	if s := stateStore(cfg, nil); s == nil {
		t.Error("forward.restore = true remembers nothing")
	}
	cfg.Forward.Restore = false
	if s := stateStore(cfg, nil); s != nil {
		t.Error("forward.restore = false still remembers forwards")
	}
}

func TestKeepaliveFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.SSH.KeepaliveInterval, cfg.SSH.KeepaliveCountMax = 20*time.Second, 4
	if k := keepaliveFor(&sshx.HostConfig{}, cfg); k.Interval != 20*time.Second || k.CountMax != 4 {
		t.Errorf("keepalive = %+v, want the config's 20s/4", k)
	}
	hc := &sshx.HostConfig{ServerAliveInterval: 5 * time.Second, ServerAliveCountMax: 2}
	if k := keepaliveFor(hc, cfg); k.Interval != 5*time.Second || k.CountMax != 2 {
		t.Errorf("keepalive = %+v, want the ssh config's 5s/2", k)
	}
}

func TestEngineOptionsM4(t *testing.T) {
	cfg := config.Default()
	cfg.Forward.AllowLANSocks = true
	cfg.Forward.PrivilegedOffset = 20000
	var rules []config.ForwardRule
	for _, raw := range []string{"-R 9000:3000", "-D 1080"} {
		r, err := config.ParseForward(raw)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, r)
	}
	cfg.Hosts = map[string]config.Host{"devbox": {Forwards: rules}}
	o := engineOptions(cfg, nil, nil)
	if !o.AllowLANSocks || o.PrivilegedOffset != 20000 {
		t.Errorf("AllowLANSocks %v, PrivilegedOffset %d", o.AllowLANSocks, o.PrivilegedOffset)
	}
	got := o.Settings("devbox").Forwards
	want := []engine.ForwardSpec{
		{Kind: engine.Remote, RemotePort: 9000, LocalPort: 3000},
		{Kind: engine.Dynamic, LocalPort: 1080},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("config forwards = %+v, want %+v", got, want)
	}
	cfg.Forward.PrivilegedOffset = 0
	if o := engineOptions(cfg, nil, nil); o.PrivilegedOffset >= 0 {
		t.Errorf("privileged_offset = 0 gave %d, want mapping off", o.PrivilegedOffset)
	}
}

func TestSSHConfigForwardsJoinConfigOnes(t *testing.T) {
	cfg := config.Default()
	r, err := config.ParseForward("5432")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Hosts = map[string]config.Host{"devbox": {Forwards: []config.ForwardRule{r}}}
	o := engineOptions(cfg, nil, nil)
	hc := &sshx.HostConfig{LocalForwards: []string{"8080 localhost:80", "/tmp/s localhost:1"}, DynamicForwards: []string{"1080"}}
	var logs []string
	addSSHForwards(&o, "devbox", hc, func(msg string) { logs = append(logs, msg) })
	got := o.Settings("devbox").Forwards
	if len(got) != 3 || got[1] != (engine.ForwardSpec{Kind: engine.Local, LocalPort: 8080, RemoteHost: "localhost", RemotePort: 80}) ||
		got[2] != (engine.ForwardSpec{Kind: engine.Dynamic, LocalPort: 1080}) {
		t.Errorf("forwards = %+v", got)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "/tmp/s") {
		t.Errorf("logs = %q, want one note about the unix socket forward", logs)
	}
	if fs := o.Settings("other").Forwards; len(fs) != 0 {
		t.Errorf("another host got %+v", fs)
	}
}

func TestTUIOptionsListLocalPorts(t *testing.T) {
	if tuiOptions(config.Default(), func(string) string { return "" }, "linux").LocalPorts == nil {
		t.Error("the Reverse tab has no way to list local ports")
	}
}

// TestSSHConfigSocketForwardIsSkipped: ssh -G prints LocalForward 2375
// /var/run/docker.sock twice, the second time as a DynamicForward on the
// same port. Neither starts a forward. The lines are as OpenSSH 9.9
// prints them.
func TestSSHConfigSocketForwardIsSkipped(t *testing.T) {
	o := engineOptions(config.Default(), nil, nil)
	hc := &sshx.HostConfig{
		LocalForwards:   []string{"2375 /var/run/docker.sock", "8080 [localhost]:80"},
		DynamicForwards: []string{"2375", "1080"},
	}
	var logs []string
	addSSHForwards(&o, "devbox", hc, func(msg string) { logs = append(logs, msg) })
	got := o.Settings("devbox").Forwards
	want := []engine.ForwardSpec{
		{Kind: engine.Local, LocalPort: 8080, RemoteHost: "localhost", RemotePort: 80},
		{Kind: engine.Dynamic, LocalPort: 1080},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("forwards = %+v, want %+v", got, want)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "docker.sock") {
		t.Errorf("logs = %q, want one note about the socket forward", logs)
	}
}
