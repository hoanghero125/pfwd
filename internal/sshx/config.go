package sshx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kevinburke/ssh_config"
)

// HostConfig is the resolved connection settings for one host.
type HostConfig struct {
	// Alias is the host as the user typed it, without any user@ prefix.
	Alias    string
	HostName string
	User     string
	Port     int
	// IdentityFiles are expanded key paths in the order they are tried.
	// Missing files are skipped at dial time, like OpenSSH does.
	IdentityFiles  []string
	IdentitiesOnly bool
	// IdentityAgent is "" for the default agent, "none" to disable the
	// agent, or a socket or pipe path.
	IdentityAgent string
	// KnownHostsFiles lists the user files first, then the global ones.
	KnownHostsFiles []string
	// StrictHostKeyChecking is "yes", "no", "ask" or "accept-new".
	StrictHostKeyChecking string
	HostKeyAlias          string
	// ConnectTimeout is 0 when unset; Dial then uses its own default.
	ConnectTimeout time.Duration
	// ProxyJump and ProxyCommand are "" when unset or "none".
	ProxyJump    string
	ProxyCommand string
}

// Overrides are command-line settings that win over the ssh config.
type Overrides struct {
	User string
	Port int
	// Identities are tried before the IdentityFile entries from the config.
	Identities []string
	// ConfigFile replaces ~/.ssh/config, like ssh -F.
	ConfigFile string
}

// Env holds what config resolution needs from the machine, so tests can
// replace it.
type Env struct {
	Home        string
	LocalUser   string
	ProgramData string
	// SystemConfig is the system-wide ssh_config read by the parser fallback.
	SystemConfig string
	// LookSSH finds the OpenSSH client; an error selects the parser fallback.
	LookSSH func() (string, error)
	// Run runs a command and returns its stdout.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DefaultEnv returns the Env of the current user and machine.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	env := Env{
		Home:        home,
		LocalUser:   localUser(),
		ProgramData: os.Getenv("ProgramData"),
		LookSSH:     lookSSH,
		Run:         runCommand,
	}
	if runtime.GOOS == "windows" {
		if env.ProgramData != "" {
			env.SystemConfig = filepath.Join(env.ProgramData, "ssh", "ssh_config")
		}
	} else {
		env.SystemConfig = "/etc/ssh/ssh_config"
	}
	return env
}

func localUser() string {
	u, err := user.Current()
	if err != nil {
		if name := os.Getenv("USER"); name != "" {
			return name
		}
		return os.Getenv("USERNAME")
	}
	name := u.Username
	// Windows reports DOMAIN\user.
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// lookSSH prefers the native OpenSSH on Windows: Git for Windows puts an MSYS
// ssh first on PATH, which prints /c/... paths and reads a different ~/.ssh.
func lookSSH() (string, error) {
	if runtime.GOOS == "windows" {
		if root := os.Getenv("SystemRoot"); root != "" {
			p := filepath.Join(root, "System32", "OpenSSH", "ssh.exe")
			if _, err := os.Stat(p); err == nil { //nolint:gosec // p is built from %SystemRoot%, not from user input
				return p, nil
			}
		}
	}
	return exec.LookPath("ssh")
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

// badHostChars are rejected in host aliases and user names on top of
// whitespace and control characters. None of them is valid in a host name,
// and they are the ones a shell or ssh option parser would interpret.
const badHostChars = "'\"`$;&|<>(){}\\*?!"

// ValidateAlias rejects host aliases that could be taken as an ssh option or
// interpreted by a shell. It runs before the alias reaches any command.
func ValidateAlias(alias string) error {
	return validateName("host", alias)
}

func validateName(what, s string) error {
	if s == "" {
		return fmt.Errorf("empty %s name; pass a host from ~/.ssh/config, like: pfwd devbox", what)
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("invalid %s %q: it can't start with '-'", what, s)
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(badHostChars, r) {
			return fmt.Errorf("invalid %s %q: it contains %q; use a host alias from ~/.ssh/config or a plain host name", what, s, r)
		}
	}
	return nil
}

// Resolve resolves target ("alias" or "user@alias") into a HostConfig. It
// runs `ssh -G` when OpenSSH is installed, which honors Match, Include and
// every default exactly as ssh does, and parses the config files otherwise.
func Resolve(ctx context.Context, target string, o Overrides, env Env) (*HostConfig, error) {
	alias := target
	if i := strings.LastIndexByte(target, '@'); i >= 0 {
		name := target[:i]
		alias = target[i+1:]
		if err := validateName("user", name); err != nil {
			return nil, err
		}
		if o.User == "" {
			o.User = name
		}
	}
	if err := ValidateAlias(alias); err != nil {
		return nil, err
	}

	var opts map[string][]string
	if sshPath, err := env.LookSSH(); err == nil {
		args := []string{"-G"}
		if o.ConfigFile != "" {
			args = append(args, "-F", o.ConfigFile)
		}
		args = append(args, "--", alias)
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := env.Run(runCtx, sshPath, args...)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: ssh -G failed: %w; run `ssh -G %s` to see the problem in your ssh config", alias, err, alias)
		}
		opts = parseSSHG(bytes.NewReader(out))
	} else {
		if opts, err = optionsFromFiles(alias, o, env); err != nil {
			return nil, err
		}
	}
	return fromOptions(alias, opts, o, env)
}

// parseSSHG parses `ssh -G` output: one "key value" pair per line, keys
// lowercased, repeated keys kept in order.
func parseSSHG(r io.Reader) map[string][]string {
	opts := make(map[string][]string)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		key = strings.ToLower(key)
		opts[key] = append(opts[key], strings.TrimSpace(value))
	}
	return opts
}

// fileKeys are the single-valued options the parser fallback reads.
var fileKeys = []string{
	"hostname", "user", "port", "identitiesonly", "identityagent", "proxyjump",
	"proxycommand", "stricthostkeychecking", "hostkeyalias", "connecttimeout",
	"userknownhostsfile", "globalknownhostsfile",
}

var defaultIdentityFiles = []string{
	"~/.ssh/id_rsa", "~/.ssh/id_ecdsa", "~/.ssh/id_ecdsa_sk",
	"~/.ssh/id_ed25519", "~/.ssh/id_ed25519_sk", "~/.ssh/id_dsa",
}

// optionsFromFiles reads the user and system ssh configs with the
// ssh_config parser and returns the same shape as parseSSHG, with OpenSSH's
// defaults filled in. It does not understand Match blocks.
func optionsFromFiles(alias string, o Overrides, env Env) (map[string][]string, error) {
	var cfgs []*ssh_config.Config
	if o.ConfigFile != "" {
		c, err := decodeFile(o.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("read ssh config %s: %w", o.ConfigFile, err)
		}
		cfgs = append(cfgs, c)
	} else {
		for _, p := range []string{filepath.Join(env.Home, ".ssh", "config"), env.SystemConfig} {
			if p == "" {
				continue
			}
			c, err := decodeFile(p)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read ssh config %s: %w", p, err)
			}
			cfgs = append(cfgs, c)
		}
	}

	opts := make(map[string][]string)
	for _, key := range fileKeys {
		for _, c := range cfgs {
			v, err := c.Get(alias, key)
			if err != nil {
				return nil, fmt.Errorf("read ssh config for %s: %w", alias, err)
			}
			if v != "" {
				opts[key] = []string{v}
				break
			}
		}
	}
	// IdentityFile accumulates across files, like OpenSSH.
	for _, c := range cfgs {
		vs, err := c.GetAll(alias, "identityfile")
		if err != nil {
			return nil, fmt.Errorf("read ssh config for %s: %w", alias, err)
		}
		opts["identityfile"] = append(opts["identityfile"], vs...)
	}
	if len(opts["identityfile"]) == 0 {
		opts["identityfile"] = defaultIdentityFiles
	}
	if hn := opts["hostname"]; len(hn) == 1 {
		opts["hostname"] = []string{strings.ReplaceAll(hn[0], "%h", alias)}
	}
	if _, ok := opts["userknownhostsfile"]; !ok {
		opts["userknownhostsfile"] = []string{"~/.ssh/known_hosts ~/.ssh/known_hosts2"}
	}
	if _, ok := opts["globalknownhostsfile"]; !ok {
		if runtime.GOOS == "windows" {
			opts["globalknownhostsfile"] = []string{`__PROGRAMDATA__\ssh/ssh_known_hosts __PROGRAMDATA__\ssh/ssh_known_hosts2`}
		} else {
			opts["globalknownhostsfile"] = []string{"/etc/ssh/ssh_known_hosts /etc/ssh/ssh_known_hosts2"}
		}
	}
	return opts, nil
}

func decodeFile(path string) (*ssh_config.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ssh_config.Decode(f)
}

// fromOptions builds a HostConfig from ssh -G style options, applies the
// command-line overrides and expands paths.
func fromOptions(alias string, opts map[string][]string, o Overrides, env Env) (*HostConfig, error) {
	first := func(key string) string {
		if v := opts[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	hc := &HostConfig{Alias: alias, HostName: first("hostname"), User: first("user")}
	if hc.HostName == "" {
		hc.HostName = alias
	}
	if o.User != "" {
		hc.User = o.User
	}
	if hc.User == "" {
		hc.User = env.LocalUser
	}

	switch {
	case o.Port != 0:
		if o.Port < 1 || o.Port > 65535 {
			return nil, fmt.Errorf("invalid port %d: use a number from 1 to 65535", o.Port)
		}
		hc.Port = o.Port
	case first("port") == "":
		hc.Port = 22
	default:
		p, err := strconv.Atoi(first("port"))
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("invalid port %q in the ssh config for %s: use a number from 1 to 65535", first("port"), alias)
		}
		hc.Port = p
	}

	tok := tokens{alias: alias, host: hc.HostName, port: strconv.Itoa(hc.Port), remoteUser: hc.User}
	seen := make(map[string]bool)
	for _, p := range append(append([]string(nil), o.Identities...), opts["identityfile"]...) {
		if p = expandPath(p, tok, env); p != "" && !seen[p] {
			seen[p] = true
			hc.IdentityFiles = append(hc.IdentityFiles, p)
		}
	}
	hc.IdentitiesOnly = isYes(first("identitiesonly"))
	switch agent := first("identityagent"); agent {
	case "", "SSH_AUTH_SOCK":
	case "none":
		hc.IdentityAgent = "none"
	default:
		hc.IdentityAgent = expandPath(agent, tok, env)
	}
	for _, key := range []string{"userknownhostsfile", "globalknownhostsfile"} {
		for _, line := range opts[key] {
			for _, p := range splitPaths(line) {
				if p != "none" {
					hc.KnownHostsFiles = append(hc.KnownHostsFiles, expandPath(p, tok, env))
				}
			}
		}
	}
	switch strings.ToLower(first("stricthostkeychecking")) {
	case "yes", "true":
		hc.StrictHostKeyChecking = "yes"
	case "no", "false", "off":
		hc.StrictHostKeyChecking = "no"
	case "accept-new":
		hc.StrictHostKeyChecking = "accept-new"
	default:
		hc.StrictHostKeyChecking = "ask"
	}
	hc.HostKeyAlias = noneToEmpty(first("hostkeyalias"))
	if n, err := strconv.Atoi(first("connecttimeout")); err == nil && n > 0 {
		hc.ConnectTimeout = time.Duration(n) * time.Second
	}
	hc.ProxyJump = noneToEmpty(first("proxyjump"))
	hc.ProxyCommand = noneToEmpty(first("proxycommand"))
	return hc, nil
}

func isYes(v string) bool {
	v = strings.ToLower(v)
	return v == "yes" || v == "true"
}

func noneToEmpty(v string) string {
	if strings.EqualFold(v, "none") {
		return ""
	}
	return v
}

// splitPaths splits a space-separated path list from ssh -G. OpenSSH prints
// the list without quoting, so a path with a space (C:\Users\John Doe/...)
// is rejoined: a new path only starts at a token that looks like the start
// of a path.
func splitPaths(line string) []string {
	var paths []string
	for _, f := range strings.Fields(line) {
		if len(paths) > 0 && !looksLikePathStart(f) {
			paths[len(paths)-1] += " " + f
			continue
		}
		paths = append(paths, f)
	}
	return paths
}

func looksLikePathStart(s string) bool {
	switch {
	case s == "none":
		return true
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "~"), strings.HasPrefix(s, "%"),
		strings.HasPrefix(s, `\`), strings.HasPrefix(s, "__PROGRAMDATA__"):
		return true
	case len(s) >= 2 && s[1] == ':' && unicode.IsLetter(rune(s[0])):
		return true
	}
	return false
}

// tokens are the values for the percent tokens ssh_config(5) allows in
// IdentityFile, IdentityAgent and UserKnownHostsFile.
type tokens struct {
	alias, host, port, remoteUser string
}

// expandPath expands ~ and the %d %u %h %n %p %r %% tokens, then on Windows
// converts MSYS paths (/c/Users/...) and the __PROGRAMDATA__ placeholder
// that the native OpenSSH prints.
func expandPath(p string, tok tokens, env Env) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		p = env.Home
	} else if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = env.Home + p[1:]
	}

	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '%' || i+1 == len(p) {
			b.WriteByte(p[i])
			continue
		}
		i++
		switch p[i] {
		case '%':
			b.WriteByte('%')
		case 'd':
			b.WriteString(env.Home)
		case 'u':
			b.WriteString(env.LocalUser)
		case 'h':
			b.WriteString(tok.host)
		case 'n':
			b.WriteString(tok.alias)
		case 'p':
			b.WriteString(tok.port)
		case 'r':
			b.WriteString(tok.remoteUser)
		default:
			b.WriteByte('%')
			b.WriteByte(p[i])
		}
	}
	p = b.String()

	if runtime.GOOS == "windows" {
		if rest, ok := strings.CutPrefix(p, "__PROGRAMDATA__"); ok {
			p = env.ProgramData + rest
		}
		if len(p) >= 2 && p[0] == '/' && unicode.IsLetter(rune(p[1])) && (len(p) == 2 || p[2] == '/') {
			p = strings.ToUpper(p[1:2]) + `:\` + strings.TrimPrefix(p[2:], "/")
		}
	}
	return filepath.Clean(filepath.FromSlash(p))
}
