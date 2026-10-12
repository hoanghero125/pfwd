package config

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
)

// OnConflict says what happens when a forward's local port is taken.
type OnConflict string

// Port conflict behaviors.
const (
	ConflictAsk    OnConflict = "ask"
	ConflictNext   OnConflict = "next"
	ConflictRandom OnConflict = "random"
	ConflictFail   OnConflict = "fail"
)

// Theme picks the light or dark palette.
type Theme string

// Themes. Auto asks the terminal for its background color.
const (
	ThemeAuto  Theme = "auto"
	ThemeDark  Theme = "dark"
	ThemeLight Theme = "light"
)

// Glyphs picks the symbols the TUI draws.
type Glyphs string

// Glyph sets. Auto uses ASCII in the legacy Windows console, whose fonts
// lack the Unicode ones.
const (
	GlyphsAuto    Glyphs = "auto"
	GlyphsUnicode Glyphs = "unicode"
	GlyphsASCII   Glyphs = "ascii"
)

// Config is the loaded configuration with every value checked.
type Config struct {
	Detect  Detect
	Forward Forward
	SSH     SSH
	UI      UI
	// Labels are user labels by port; they win over the built-in ones.
	Labels map[uint16]string
	// Hosts are per-host overrides by ssh config alias.
	Hosts map[string]Host
}

// Detect configures port detection.
type Detect struct {
	Interval        time.Duration
	HTTPProbe       bool
	DebounceScans   int
	IgnorePorts     detect.PortSet
	IgnoreProcesses []string
}

// Forward configures forwards.
type Forward struct {
	Auto             bool
	AutoPorts        detect.PortSet
	Bind             string
	OnConflict       OnConflict
	PrivilegedOffset int
	AutoExpire       time.Duration
	MaxConns         int
	Restore          bool
}

// SSH configures connections.
type SSH struct {
	UseOpenSSHConfig    bool
	KeepaliveInterval   time.Duration
	KeepaliveCountMax   int
	ConnectTimeout      time.Duration
	ReconnectMaxBackoff time.Duration
}

// UI configures the terminal UI.
type UI struct {
	Theme         Theme
	Mouse         bool
	ConfirmQuit   bool
	Notifications bool
	ShowHidden    bool
	Glyphs        Glyphs
}

// Host holds one host's overrides.
type Host struct {
	// Auto overrides forward.auto when set.
	Auto *bool
	// AutoPorts overrides forward.auto_ports when HasAutoPorts is set; an
	// empty set then means every port.
	AutoPorts    detect.PortSet
	HasAutoPorts bool
	// Forwards are always-on, pinned forwards, created when the host
	// connects. Remote and dynamic ones apply from v0.4.0.
	Forwards []ForwardRule
}

// Default returns the configuration used when there is no config file.
func Default() Config {
	f := detect.DefaultFilter()
	return Config{
		Detect: Detect{
			Interval:        2 * time.Second,
			HTTPProbe:       true,
			DebounceScans:   2,
			IgnorePorts:     f.IgnorePorts,
			IgnoreProcesses: f.IgnoreProcesses,
		},
		Forward: Forward{
			Auto:             true,
			AutoPorts:        detect.PortSet{},
			Bind:             "127.0.0.1",
			OnConflict:       ConflictAsk,
			PrivilegedOffset: 10000,
			AutoExpire:       10 * time.Minute,
			MaxConns:         256,
			Restore:          true,
		},
		SSH: SSH{
			UseOpenSSHConfig:    true,
			KeepaliveInterval:   15 * time.Second,
			KeepaliveCountMax:   3,
			ConnectTimeout:      10 * time.Second,
			ReconnectMaxBackoff: 30 * time.Second,
		},
		UI: UI{
			Theme:         ThemeAuto,
			Mouse:         true,
			ConfirmQuit:   true,
			Notifications: true,
			Glyphs:        GlyphsAuto,
		},
		Labels: map[uint16]string{},
		Hosts:  map[string]Host{},
	}
}

// AutoFor reports whether auto-forward starts on for a host.
func (c Config) AutoFor(alias string) bool {
	if h, ok := c.Hosts[alias]; ok && h.Auto != nil {
		return *h.Auto
	}
	return c.Forward.Auto
}

// FilterFor returns the hide and auto-forward rules for a host.
func (c Config) FilterFor(alias string) detect.Filter {
	f := detect.Filter{
		IgnorePorts:     slices.Clone(c.Detect.IgnorePorts),
		IgnoreProcesses: slices.Clone(c.Detect.IgnoreProcesses),
		AutoPorts:       slices.Clone(c.Forward.AutoPorts),
	}
	if h, ok := c.Hosts[alias]; ok && h.HasAutoPorts {
		f.AutoPorts = slices.Clone(h.AutoPorts)
	}
	return f
}

// Unapplied lists settings that are set but only take effect in a later
// version, so `pfwd config validate` can say so.
func (c Config) Unapplied() []string {
	d := Default()
	var notes []string
	note := func(key, version string) {
		notes = append(notes, fmt.Sprintf("%s is accepted but applies from %s", key, version))
	}
	if c.Forward.PrivilegedOffset != d.Forward.PrivilegedOffset {
		note("forward.privileged_offset", "v0.4.0")
	}
	aliases := make([]string, 0, len(c.Hosts))
	for a := range c.Hosts {
		aliases = append(aliases, a)
	}
	slices.Sort(aliases)
	for _, a := range aliases {
		for _, r := range c.Hosts[a].Forwards {
			if r.Kind != "local" {
				note(fmt.Sprintf("hosts.%s.forwards entry %q", quoteKey(a), r.Raw), "v0.4.0")
			}
		}
	}
	return notes
}

// quoteKey quotes a TOML key part that isn't a bare key.
func quoteKey(k string) string {
	bare := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
	}
	for _, r := range k {
		if !bare(r) {
			return fmt.Sprintf("%q", k)
		}
	}
	if k == "" {
		return `""`
	}
	return k
}

// joinKey formats a key path the way a user writes it in the file.
func joinKey(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = quoteKey(p)
	}
	return strings.Join(q, ".")
}
