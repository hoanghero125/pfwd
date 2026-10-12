package cli

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// hostDialer resolves and dials hosts for an engine. Each host's ssh
// config is resolved once, when it is added, so one engine can serve
// several hosts: TUI tabs, or the daemon.
type hostDialer struct {
	cfg    config.Config
	f      *rootFlags
	logger *slog.Logger
	keys   *sshx.KeyCache
	// warn reports an ssh config forward line pfwd can't use.
	warn func(host engine.HostID, msg string)

	mu       sync.Mutex
	hosts    map[engine.HostID]*sshx.HostConfig
	forwards map[engine.HostID][]engine.ForwardSpec
}

// newHostDialer returns a dialer that uses cfg and the command line's ssh
// flags. Decrypted keys and the password are kept for the dialer's life,
// so reconnects don't ask again; clear them with close.
func newHostDialer(cfg config.Config, f *rootFlags, logger *slog.Logger) *hostDialer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	d := &hostDialer{
		cfg: cfg, f: f, logger: logger, keys: sshx.NewKeyCache(),
		hosts:    make(map[engine.HostID]*sshx.HostConfig),
		forwards: make(map[engine.HostID][]engine.ForwardSpec),
	}
	d.warn = func(host engine.HostID, msg string) { d.logger.Warn(msg, "host", host) }
	return d
}

// close forgets the decrypted keys and the password.
func (d *hostDialer) close() { d.keys.Clear() }

// add resolves target (an alias or user@alias) and returns its host ID.
// withFlags applies -p, -l, -i and -J, which name the first host given on
// the command line.
func (d *hostDialer) add(ctx context.Context, target string, withFlags bool) (engine.HostID, error) {
	o := sshx.Overrides{ConfigFile: d.f.sshConfig}
	if withFlags {
		o.User, o.Port, o.Identities, o.Jump = d.f.login, d.f.port, d.f.identities, d.f.jump
	}
	hc, err := sshx.Resolve(ctx, target, o, sshEnv(d.cfg))
	if err != nil {
		return "", err
	}
	applySSHConfig(hc, d.cfg)
	host := engine.HostID(hc.Alias)
	d.remember(host, hc)
	return host, nil
}

// remember keeps host's resolved config and the forwards its ssh config's
// LocalForward, RemoteForward and DynamicForward lines ask for.
func (d *hostDialer) remember(host engine.HostID, hc *sshx.HostConfig) {
	specs := sshForwards(hc, func(msg string) { d.warn(host, msg) })
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hosts[host] = hc
	d.forwards[host] = specs
}

// hostConfig returns host's resolved config, or nil.
func (d *hostDialer) hostConfig(host engine.HostID) *sshx.HostConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hosts[host]
}

// settings adds each host's ssh config forwards after its config.toml ones.
func (d *hostDialer) settings(base func(engine.HostID) engine.HostSettings) func(engine.HostID) engine.HostSettings {
	return func(host engine.HostID) engine.HostSettings {
		s := base(host)
		d.mu.Lock()
		extra := d.forwards[host]
		d.mu.Unlock()
		if len(extra) > 0 {
			s.Forwards = append(append([]engine.ForwardSpec(nil), s.Forwards...), extra...)
		}
		return s
	}
}

// Dial connects to host, which add must have resolved.
func (d *hostDialer) Dial(ctx context.Context, host engine.HostID, p engine.Prompts) (engine.Conn, error) {
	hc := d.hostConfig(host)
	if hc == nil {
		return nil, fmt.Errorf("%s was not resolved from the ssh config; this is a bug in pfwd, please report it", host)
	}
	c, err := sshx.Dial(ctx, hc, sshx.DialOptions{
		Prompter: p, Credentials: p, Keys: d.keys, Keepalive: keepaliveFor(hc, d.cfg),
		AcceptNew: d.f.acceptNew, Logger: d.logger,
		// Jump hosts resolve from the same ssh config, without the -p, -l
		// and -i that apply to the target.
		Resolve: func(ctx context.Context, target string) (*sshx.HostConfig, error) {
			hop, err := sshx.Resolve(ctx, target, sshx.Overrides{ConfigFile: d.f.sshConfig}, sshEnv(d.cfg))
			if err == nil {
				applySSHConfig(hop, d.cfg)
			}
			return hop, err
		},
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// sshForwards turns a host's LocalForward, RemoteForward and DynamicForward
// lines into specs. A line pfwd can't use, such as a Unix-socket forward,
// is skipped and reported to logf.
func sshForwards(hc *sshx.HostConfig, logf func(string)) []engine.ForwardSpec {
	// ssh -G prints a LocalForward to a Unix socket a second time, as a
	// DynamicForward on the same listen address; that one is not real.
	socketListen := make(map[string]bool)
	for _, v := range hc.LocalForwards {
		if f := strings.Fields(v); len(f) == 2 && strings.Contains(f[1], "/") {
			socketListen[f[0]] = true
		}
	}
	var specs []engine.ForwardSpec
	for _, lines := range []struct {
		kind   string
		values []string
	}{{"localforward", hc.LocalForwards}, {"remoteforward", hc.RemoteForwards}, {"dynamicforward", hc.DynamicForwards}} {
		for _, v := range lines.values {
			if lines.kind == "dynamicforward" && socketListen[strings.TrimSpace(v)] {
				continue
			}
			r, err := config.ParseSSHForward(lines.kind, v)
			if err != nil {
				logf(fmt.Sprintf("skipping a forward from the ssh config: %v", err))
				continue
			}
			specs = append(specs, configForwards([]config.ForwardRule{r})...)
		}
	}
	return specs
}
