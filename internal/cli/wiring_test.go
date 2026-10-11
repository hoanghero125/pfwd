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

func TestFlagsOverrideConfig(t *testing.T) {
	f := rootFlags{bind: "0.0.0.0", interval: time.Second, auto: true}
	cfg := config.Default()
	auto, err := f.applyTo(&cfg)
	if err != nil || cfg.Forward.Bind != "0.0.0.0" || cfg.Detect.Interval != time.Second || auto == nil || !*auto {
		t.Errorf("applyTo = %v, %v; config bind %q, interval %v", auto, err, cfg.Forward.Bind, cfg.Detect.Interval)
	}
	f = rootFlags{noAuto: true}
	if auto, err := f.applyTo(&cfg); err != nil || auto == nil || *auto {
		t.Errorf("--no-auto = %v, %v", auto, err)
	}
	if auto, _ := (&rootFlags{}).applyTo(&cfg); auto != nil {
		t.Errorf("no flag gave auto %v", *auto)
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
