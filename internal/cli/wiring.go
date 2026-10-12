package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/desktop"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/state"
	"github.com/hoanghero125/pfwd/internal/tui"
)

// minInterval is the shortest detection interval, as in the config.
const minInterval = 500 * time.Millisecond

// applyTo applies the flags that override the config, and returns the
// auto-forward override for the host (nil when neither --auto nor
// --no-auto is given). changed reports whether a flag was given, so an
// explicit --auto=false or --interval 0 counts, rather than looking like
// the flag's default.
func (f *rootFlags) applyTo(cfg *config.Config, changed func(name string) bool) (*bool, error) {
	if changed("bind") {
		if a, err := netip.ParseAddr(f.bind); f.bind != "localhost" && (err != nil || a.Zone() != "") {
			return nil, fmt.Errorf("invalid --bind %q: use an IP address such as 127.0.0.1 or 0.0.0.0", f.bind)
		}
		cfg.Forward.Bind = f.bind
	}
	if changed("interval") {
		if f.interval < minInterval {
			return nil, fmt.Errorf("invalid --interval %v: use 500ms or more", f.interval)
		}
		cfg.Detect.Interval = f.interval
	}
	var auto *bool
	if changed("auto") {
		on := f.auto
		auto = &on
	}
	if changed("no-auto") {
		on := !f.noAuto
		if auto != nil && *auto != on {
			return nil, errors.New("--auto and --no-auto contradict each other: pick one")
		}
		auto = &on
	}
	return auto, nil
}

// engineOptions returns the engine's settings from the config. auto, when
// set, wins over forward.auto and the hosts' own auto. A nil notifier
// turns notifications off.
func engineOptions(cfg config.Config, auto *bool, n engine.Notifier) engine.Options {
	o := engine.Options{
		Interval: cfg.Detect.Interval,
		Debounce: cfg.Detect.DebounceScans,
		Settings: func(host engine.HostID) engine.HostSettings {
			s := engine.HostSettings{Auto: cfg.AutoFor(string(host)), Filter: cfg.FilterFor(string(host)),
				Forwards: configForwards(cfg.Hosts[string(host)].Forwards)}
			if auto != nil {
				s.Auto = *auto
			}
			return s
		},
		Labels:     cfg.Labels,
		HTTPProbe:  cfg.Detect.HTTPProbe,
		OnConflict: string(cfg.Forward.OnConflict),
		BindAddr:   cfg.Forward.Bind,
		MaxConns:   cfg.Forward.MaxConns,
		Notifier:   n,
		MaxBackoff: cfg.SSH.ReconnectMaxBackoff,
		AutoExpire: cfg.Forward.AutoExpire,
	}
	return o
}

// configForwards turns a host's config forwards into engine specs. Remote
// and dynamic ones wait for v0.4.0; config validate notes them.
func configForwards(rules []config.ForwardRule) []engine.ForwardSpec {
	var specs []engine.ForwardSpec
	for _, r := range rules {
		if r.Kind != "local" {
			continue
		}
		specs = append(specs, engine.ForwardSpec{Kind: engine.Local, BindAddr: r.Bind, LocalPort: r.Local, RemoteHost: r.RemoteHost, RemotePort: r.Remote})
	}
	return specs
}

// stateStore returns where forwards are remembered, or nil when
// forward.restore is off or there is no state directory.
func stateStore(cfg config.Config, logger *slog.Logger) *state.Store {
	if !cfg.Forward.Restore {
		return nil
	}
	path, err := config.StatePath()
	if err != nil {
		if logger != nil {
			logger.Warn("not remembering forwards", "err", err)
		}
		return nil
	}
	return state.Open(path, state.Options{Logger: logger})
}

// keepaliveFor is the ssh config's ServerAlive settings when it sets them,
// else the config file's.
func keepaliveFor(hc *sshx.HostConfig, cfg config.Config) sshx.Keepalive {
	return hc.KeepaliveOr(sshx.Keepalive{Interval: cfg.SSH.KeepaliveInterval, CountMax: cfg.SSH.KeepaliveCountMax})
}

// tuiOptions returns the TUI's settings from the config. getenv and goos
// decide whether glyphs = "auto" means ASCII.
func tuiOptions(cfg config.Config, getenv func(string) string, goos string) tui.Options {
	o := tui.Options{
		Open:            desktop.OpenURL,
		Copy:            desktop.Copy,
		Mouse:           cfg.UI.Mouse,
		SkipQuitConfirm: !cfg.UI.ConfirmQuit,
		ShowHidden:      cfg.UI.ShowHidden,
		BindAddr:        cfg.Forward.Bind,
		OnConflict:      string(cfg.Forward.OnConflict),
	}
	if cfg.UI.Theme == config.ThemeDark || cfg.UI.Theme == config.ThemeLight {
		o.Theme = string(cfg.UI.Theme)
	}
	switch cfg.UI.Glyphs {
	case config.GlyphsASCII:
		o.ASCII = true
	case config.GlyphsAuto:
		o.ASCII = legacyConsole(getenv, goos)
	}
	return o
}

// legacyConsole reports whether this is the old Windows console host,
// whose fonts lack the TUI's Unicode glyphs. Windows Terminal sets
// WT_SESSION, and other terminals TERM_PROGRAM or TERM.
func legacyConsole(getenv func(string) string, goos string) bool {
	return goos == "windows" && getenv("WT_SESSION") == "" && getenv("TERM_PROGRAM") == "" && getenv("TERM") == ""
}

// noColor reports whether colors are off: NO_COLOR set to anything
// (no-color.org), or --no-color.
func noColor(env string, flag bool) bool {
	return env != "" || flag
}

// sshEnv is the host-resolution environment; use_openssh_config = false
// skips `ssh -G` and reads the ssh config files directly.
func sshEnv(cfg config.Config) sshx.Env {
	env := sshx.DefaultEnv()
	if !cfg.SSH.UseOpenSSHConfig {
		env.LookSSH = func() (string, error) { return "", errors.New("use_openssh_config is off") }
	}
	return env
}

// applySSHConfig fills what the ssh config left unset.
func applySSHConfig(hc *sshx.HostConfig, cfg config.Config) {
	if hc.ConnectTimeout == 0 {
		hc.ConnectTimeout = cfg.SSH.ConnectTimeout
	}
}
