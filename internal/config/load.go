package config

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/hoanghero125/pfwd/internal/detect"
)

// Path returns the config file path: $PFWD_CONFIG if set, else
// pfwd/config.toml in the user config directory (XDG_CONFIG_HOME or
// ~/.config on Linux, ~/Library/Application Support on macOS, %AppData% on
// Windows).
func Path() (string, error) {
	if p := os.Getenv("PFWD_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the config directory: %w; set PFWD_CONFIG to the config file's path (on Linux, XDG_CONFIG_HOME must be an absolute path)", err)
	}
	return filepath.Join(dir, "pfwd", "config.toml"), nil
}

// Problem is one thing wrong in a config file. Line and Col are 0 when the
// position is unknown.
type Problem struct {
	Line, Col int
	Key       string
	Msg       string
}

// Problems is every problem found in one config file, sorted by position.
type Problems struct {
	Path string
	List []Problem
}

// Lines formats each problem as "path:line:col: key: message".
func (p *Problems) Lines() []string {
	out := make([]string, len(p.List))
	for i, q := range p.List {
		var b strings.Builder
		b.WriteString(p.Path)
		if q.Line > 0 {
			fmt.Fprintf(&b, ":%d:%d", q.Line, q.Col)
		}
		b.WriteString(": ")
		if q.Key != "" {
			b.WriteString(q.Key + ": ")
		}
		b.WriteString(q.Msg)
		out[i] = b.String()
	}
	return out
}

// Error returns the first problem and a pointer to the full list.
func (p *Problems) Error() string {
	lines := p.Lines()
	msg := lines[0]
	if n := len(lines) - 1; n > 0 {
		msg += fmt.Sprintf(" (and %d more)", n)
	}
	return msg + "; run `pfwd config validate` to see every problem"
}

// Load reads the config file at path. A missing file means the defaults.
// Unknown keys and bad values come back together as *Problems.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read the config file %s: %w", path, err)
	}
	return Parse(path, data)
}

// Parse reads a config file's contents; path names the file in problems.
// Unknown keys and bad values come back together as *Problems.
func Parse(path string, data []byte) (Config, error) {
	data = withoutBOM(data)

	idx := buildIndex(data)
	raw, list, ok := decodeAll(data, idx)
	if !ok {
		return Config{}, &Problems{Path: path, List: list}
	}

	v := validator{idx: idx}
	cfg := v.convert(raw)
	list = append(list, v.problems...)
	if len(list) > 0 {
		slices.SortStableFunc(list, func(a, b Problem) int {
			return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
		})
		return Config{}, &Problems{Path: path, List: list}
	}
	return cfg, nil
}

var wrongType = regexp.MustCompile(`cannot decode TOML (\w+(?: \w+)?) into .* of type (\S+)`)

// maxTypeErrors bounds how many wrong types decodeAll drops and retries.
const maxTypeErrors = 50

// decodeAll decodes data strictly into the raw config. A value of the
// wrong type is reported, removed, and the rest decoded again, so one
// wrong type doesn't hide the file's other problems. Unknown keys are
// reported too. ok is false when the file can't be decoded at all (a
// syntax error); list then says why. Positions come from idx, the index
// of the file as written, since a retry decodes re-encoded text.
func decodeAll(data []byte, idx keyIndex) (raw rawConfig, list []Problem, ok bool) {
	where := func(key []string, line, col int) (int, int) {
		if l, c := idx.at(key...); l != 0 {
			return l, c
		}
		return line, col
	}
	var doc map[string]any
	for range maxTypeErrors {
		raw = defaultRaw()
		err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&raw)
		var strict *toml.StrictMissingError
		var decode *toml.DecodeError
		// StrictMissingError first: errors.As finds a DecodeError inside it too.
		switch {
		case err == nil:
			return raw, list, true
		case errors.As(err, &strict):
			for _, e := range strict.Errors {
				line, col := where(e.Key(), 0, 0)
				if doc == nil {
					line, col = e.Position()
				}
				list = append(list, Problem{Line: line, Col: col, Key: joinKey(e.Key()), Msg: "unknown setting"})
			}
			return raw, list, true
		case errors.As(err, &decode):
			key := decode.Key()
			line, col := decode.Position()
			if doc != nil {
				line, col = where(key, line, col)
			}
			list = append(list, Problem{Line: line, Col: col, Key: joinKey(key), Msg: describe(decode)})
			if doc == nil && toml.Unmarshal(data, &doc) != nil {
				return raw, list, false
			}
			if len(key) == 0 || !deleteKey(doc, key) {
				return raw, list, false
			}
			if data, err = toml.Marshal(doc); err != nil {
				return raw, list, false
			}
		default:
			return raw, append(list, Problem{Msg: err.Error()}), false
		}
	}
	return raw, list, false
}

// deleteKey removes the value at path from a decoded document.
func deleteKey(doc map[string]any, path []string) bool {
	for _, k := range path[:len(path)-1] {
		next, ok := doc[k].(map[string]any)
		if !ok {
			return false
		}
		doc = next
	}
	if _, ok := doc[path[len(path)-1]]; !ok {
		return false
	}
	delete(doc, path[len(path)-1])
	return true
}

// describe words a decode error for users, without Go type names.
func describe(e *toml.DecodeError) string {
	msg := strings.TrimPrefix(e.Error(), "toml: ")
	m := wrongType.FindStringSubmatch(msg)
	if m == nil {
		return msg
	}
	want := "a different type"
	switch typ := m[2]; {
	case strings.HasPrefix(typ, "[]"):
		want = "a list"
	case strings.HasPrefix(typ, "map[") || strings.Contains(typ, "."):
		want = "a table"
	case strings.HasPrefix(typ, "int") || strings.HasPrefix(typ, "uint"):
		want = "an integer"
	case typ == "bool":
		want = "true or false"
	case typ == "string":
		want = `a string (in quotes, like "2s")`
	}
	return fmt.Sprintf("expected %s, got %s", want, withArticle(m[1]))
}

type rawConfig struct {
	Detect  rawDetect          `toml:"detect"`
	Forward rawForward         `toml:"forward"`
	SSH     rawSSH             `toml:"ssh"`
	UI      rawUI              `toml:"ui"`
	Labels  map[string]string  `toml:"labels"`
	Hosts   map[string]rawHost `toml:"hosts"`
}

type rawDetect struct {
	Interval        string   `toml:"interval"`
	HTTPProbe       bool     `toml:"http_probe"`
	DebounceScans   int      `toml:"debounce_scans"`
	IgnorePorts     []any    `toml:"ignore_ports"`
	IgnoreProcesses []string `toml:"ignore_processes"`
}

type rawForward struct {
	Auto             bool   `toml:"auto"`
	AutoPorts        []any  `toml:"auto_ports"`
	Bind             string `toml:"bind"`
	OnConflict       string `toml:"on_conflict"`
	PrivilegedOffset int    `toml:"privileged_offset"`
	AutoExpire       string `toml:"auto_expire"`
	MaxConns         int    `toml:"max_conns"`
	Restore          bool   `toml:"restore"`
	AllowLANSocks    bool   `toml:"allow_lan_socks"`
}

type rawSSH struct {
	UseOpenSSHConfig    bool   `toml:"use_openssh_config"`
	KeepaliveInterval   string `toml:"keepalive_interval"`
	KeepaliveCountMax   int    `toml:"keepalive_count_max"`
	ConnectTimeout      string `toml:"connect_timeout"`
	ReconnectMaxBackoff string `toml:"reconnect_max_backoff"`
}

type rawUI struct {
	Theme         string `toml:"theme"`
	Mouse         bool   `toml:"mouse"`
	ConfirmQuit   bool   `toml:"confirm_quit"`
	Notifications bool   `toml:"notifications"`
	ShowHidden    bool   `toml:"show_hidden"`
	Glyphs        string `toml:"glyphs"`
}

type rawHost struct {
	Auto      *bool    `toml:"auto"`
	AutoPorts []any    `toml:"auto_ports"`
	Forwards  []string `toml:"forwards"`
}

// defaultRaw is Default in the raw form, so keys missing from the file
// keep their defaults.
func defaultRaw() rawConfig {
	d := Default()
	return rawConfig{
		Detect: rawDetect{
			Interval: "2s", HTTPProbe: d.Detect.HTTPProbe, DebounceScans: d.Detect.DebounceScans,
			IgnorePorts: []any{int64(22)}, IgnoreProcesses: d.Detect.IgnoreProcesses,
		},
		Forward: rawForward{
			Auto: d.Forward.Auto, AutoPorts: []any{}, Bind: d.Forward.Bind, OnConflict: string(d.Forward.OnConflict),
			PrivilegedOffset: d.Forward.PrivilegedOffset, AutoExpire: "10m", MaxConns: d.Forward.MaxConns, Restore: d.Forward.Restore,
		},
		SSH: rawSSH{
			UseOpenSSHConfig: d.SSH.UseOpenSSHConfig, KeepaliveInterval: "15s", KeepaliveCountMax: d.SSH.KeepaliveCountMax,
			ConnectTimeout: "10s", ReconnectMaxBackoff: "30s",
		},
		UI: rawUI{
			Theme: string(d.UI.Theme), Mouse: d.UI.Mouse, ConfirmQuit: d.UI.ConfirmQuit,
			Notifications: d.UI.Notifications, ShowHidden: d.UI.ShowHidden, Glyphs: string(d.UI.Glyphs),
		},
	}
}

// validator converts the raw config and collects every problem.
type validator struct {
	idx      keyIndex
	problems []Problem
}

func (v *validator) add(path []string, format string, args ...any) {
	line, col := v.idx.at(path...)
	v.problems = append(v.problems, Problem{Line: line, Col: col, Key: joinKey(path), Msg: fmt.Sprintf(format, args...)})
}

func (v *validator) convert(r rawConfig) Config {
	c := Config{Labels: map[uint16]string{}, Hosts: map[string]Host{}}

	c.Detect.Interval = v.duration([]string{"detect", "interval"}, r.Detect.Interval, 500*time.Millisecond)
	c.Detect.HTTPProbe = r.Detect.HTTPProbe
	c.Detect.DebounceScans = v.atLeast([]string{"detect", "debounce_scans"}, r.Detect.DebounceScans, 1)
	c.Detect.IgnorePorts = v.ports([]string{"detect", "ignore_ports"}, r.Detect.IgnorePorts)
	c.Detect.IgnoreProcesses = r.Detect.IgnoreProcesses
	for _, p := range r.Detect.IgnoreProcesses {
		if strings.TrimSpace(p) == "" {
			v.add([]string{"detect", "ignore_processes"}, "process names can't be empty")
		}
	}

	c.Forward.Auto = r.Forward.Auto
	c.Forward.AutoPorts = v.ports([]string{"forward", "auto_ports"}, r.Forward.AutoPorts)
	c.Forward.Bind = r.Forward.Bind
	if a, err := netip.ParseAddr(r.Forward.Bind); r.Forward.Bind != "localhost" && (err != nil || a.Zone() != "") {
		v.add([]string{"forward", "bind"}, "invalid address %q: use an IP address such as 127.0.0.1 or 0.0.0.0", r.Forward.Bind)
	}
	c.Forward.OnConflict = OnConflict(v.oneOf([]string{"forward", "on_conflict"}, r.Forward.OnConflict, "ask", "next", "random", "fail"))
	c.Forward.PrivilegedOffset = r.Forward.PrivilegedOffset
	if r.Forward.PrivilegedOffset < 0 || r.Forward.PrivilegedOffset > 64511 {
		v.add([]string{"forward", "privileged_offset"}, "must be from 0 to 64511, so mapped ports stay below 65536")
	}
	c.Forward.AutoExpire = v.duration([]string{"forward", "auto_expire"}, r.Forward.AutoExpire, time.Second)
	c.Forward.MaxConns = v.atLeast([]string{"forward", "max_conns"}, r.Forward.MaxConns, 1)
	c.Forward.Restore = r.Forward.Restore
	c.Forward.AllowLANSocks = r.Forward.AllowLANSocks

	c.SSH.UseOpenSSHConfig = r.SSH.UseOpenSSHConfig
	c.SSH.KeepaliveInterval = v.duration([]string{"ssh", "keepalive_interval"}, r.SSH.KeepaliveInterval, time.Second)
	c.SSH.KeepaliveCountMax = v.atLeast([]string{"ssh", "keepalive_count_max"}, r.SSH.KeepaliveCountMax, 1)
	c.SSH.ConnectTimeout = v.duration([]string{"ssh", "connect_timeout"}, r.SSH.ConnectTimeout, time.Second)
	c.SSH.ReconnectMaxBackoff = v.duration([]string{"ssh", "reconnect_max_backoff"}, r.SSH.ReconnectMaxBackoff, time.Second)

	c.UI.Theme = Theme(v.oneOf([]string{"ui", "theme"}, r.UI.Theme, "auto", "dark", "light"))
	c.UI.Mouse = r.UI.Mouse
	c.UI.ConfirmQuit = r.UI.ConfirmQuit
	c.UI.Notifications = r.UI.Notifications
	c.UI.ShowHidden = r.UI.ShowHidden
	c.UI.Glyphs = Glyphs(v.oneOf([]string{"ui", "glyphs"}, r.UI.Glyphs, "auto", "unicode", "ascii"))

	for _, k := range sortedKeys(r.Labels) {
		n, err := strconv.ParseUint(k, 10, 16)
		if err != nil || n == 0 {
			v.add([]string{"labels", k}, "label keys are ports from 1 to 65535, like 8000 = \"API\"")
			continue
		}
		c.Labels[uint16(n)] = r.Labels[k]
	}

	for _, alias := range sortedKeys(r.Hosts) {
		rh := r.Hosts[alias]
		h := Host{Auto: rh.Auto}
		if rh.AutoPorts != nil {
			h.HasAutoPorts = true
			h.AutoPorts = v.ports([]string{"hosts", alias, "auto_ports"}, rh.AutoPorts)
		}
		for _, f := range rh.Forwards {
			rule, err := ParseForward(f)
			if err != nil {
				v.add([]string{"hosts", alias, "forwards"}, "%v", err)
				continue
			}
			h.Forwards = append(h.Forwards, rule)
		}
		c.Hosts[alias] = h
	}
	return c
}

// withArticle puts "a" or "an" in front of a type name.
func withArticle(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an " + word
	}
	return "a " + word
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (v *validator) duration(path []string, s string, minimum time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		v.add(path, "invalid duration %q: use a number with a unit, such as 2s, 500ms or 1m", s)
		return 0
	}
	if d < minimum {
		v.add(path, "must be at least %v", minimum)
	}
	return d
}

func (v *validator) atLeast(path []string, n, minimum int) int {
	if n < minimum {
		v.add(path, "must be %d or more", minimum)
	}
	return n
}

func (v *validator) oneOf(path []string, s string, allowed ...string) string {
	if !slices.Contains(allowed, s) {
		v.add(path, "invalid value %q: use %s", s, orList(allowed))
	}
	return s
}

func orList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// ports converts a list of ports (5173) and ranges ("3000-3999").
func (v *validator) ports(path []string, list []any) detect.PortSet {
	set := detect.PortSet{}
	for _, item := range list {
		switch x := item.(type) {
		case int64:
			if x < 1 || x > 65535 {
				v.add(path, "%d is not a port from 1 to 65535", x)
				continue
			}
			set = append(set, detect.PortRange{Lo: uint16(x), Hi: uint16(x)})
		case string:
			r, err := detect.ParsePortRange(x)
			if err != nil {
				v.add(path, "%v", err)
				continue
			}
			set = append(set, r)
		default:
			v.add(path, "invalid entry %v: use a port like 5173 or a range like \"3000-3999\"", x)
		}
	}
	return set
}
