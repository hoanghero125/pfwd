package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/notify"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/tui"
	"github.com/hoanghero125/pfwd/internal/version"
)

// rootFlags are the root command's flags. The SSH ones win over the ssh
// config, like the same flags of ssh.
type rootFlags struct {
	port       int
	login      string
	identities []string
	sshConfig  string
	jump       string
	acceptNew  bool
	logLevel   string
	// These override the config file.
	auto, noAuto  bool
	bind          string
	interval      time.Duration
	noColor       bool
	allowLANSocks bool
}

func (f *rootFlags) register(fs *pflag.FlagSet) {
	fs.IntVarP(&f.port, "port", "p", 0, "SSH port, instead of the one in the ssh config")
	fs.StringVarP(&f.login, "login", "l", "", "SSH user, instead of the one in the ssh config")
	fs.StringArrayVarP(&f.identities, "identity", "i", nil, "private key file to try before the ssh config's keys (repeatable)")
	fs.StringVarP(&f.sshConfig, "ssh-config", "F", "", "ssh config file to read instead of ~/.ssh/config")
	fs.StringVarP(&f.jump, "jump", "J", "", "jump hosts to connect through, like ssh -J ([user@]host[:port], comma-separated)")
	fs.BoolVar(&f.acceptNew, "accept-new", false, "trust an unknown host key without asking; a changed key is still refused")
	fs.StringVar(&f.logLevel, "log-level", "", "how much goes into pfwd.log: debug, info, warn or error (default info, or $PFWD_LOG)")
	fs.BoolVar(&f.auto, "auto", false, "forward new ports automatically, whatever the config says")
	fs.BoolVar(&f.noAuto, "no-auto", false, "don't forward new ports automatically; press enter on a port instead")
	fs.StringVar(&f.bind, "bind", "", "local address forwards listen on, instead of forward.bind (127.0.0.1)")
	fs.DurationVar(&f.interval, "interval", 0, "how often to list the server's ports, instead of detect.interval (2s)")
	fs.BoolVar(&f.noColor, "no-color", false, "draw without colors, like NO_COLOR")
	fs.BoolVar(&f.allowLANSocks, "allow-lan-socks", false, "let a SOCKS proxy listen on a non-loopback --bind, instead of forward.allow_lan_socks")
}

// hostArgs accepts exactly one host, as "alias" or "user@alias".
func hostArgs(_ *cobra.Command, args []string) error {
	switch len(args) {
	case 0:
		return &usageError{errors.New("missing host: run pfwd <host> with a host from ~/.ssh/config, like: pfwd devbox")}
	case 1:
		if _, _, err := sshx.SplitTarget(args[0]); err != nil {
			return &usageError{err}
		}
		return nil
	default:
		return &usageError{errors.New("one host at a time for now; several hosts in one window arrive in v0.5.0")}
	}
}

// runTUI connects to target and runs the terminal UI until the user quits.
func runTUI(cmd *cobra.Command, target string, f *rootFlags) error {
	c, err := loadCommon(cmd, f)
	if err != nil {
		return err
	}
	in, out, ok := terminal(cmd.InOrStdin(), cmd.OutOrStdout())
	if !ok {
		return errors.New("pfwd needs an interactive terminal; in scripts, use pfwd ls or pfwd fwd")
	}

	// Printed before the TUI takes the screen, so a warning stays readable.
	logger, closeLog := headlessLog(c.level, cmd.ErrOrStderr())
	defer closeLog()

	ctx := cmd.Context()
	d := newHostDialer(c.cfg, f, logger)
	defer d.close()
	host, err := d.add(ctx, target, true)
	if err != nil {
		return err
	}
	hc := d.hostConfig(host)
	logger.Info("starting", "version", version.Get().Version, "host", hc.Alias, "hostname", hc.HostName, "port", hc.Port,
		"config", c.cfgPath, "not_applied_yet", c.cfg.Unapplied())

	var notifier engine.Notifier
	if c.cfg.UI.Notifications {
		notifier = notify.New("pfwd")
	}
	o := engineOptions(c.cfg, c.auto, notifier)
	o.Settings = d.settings(o.Settings)
	o.Logger = logger
	o.State = stateStore(c.cfg, logger)
	o.Dial = d.Dial
	eng := engine.New(o)
	defer func() {
		if err := eng.Close(); err != nil {
			logger.Warn("engine shutdown", "err", err)
		}
	}()
	eng.Connect(host)

	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)}
	if noColor(os.Getenv("NO_COLOR"), f.noColor) {
		// Bubble Tea only honors NO_COLOR values such as 1 or true.
		opts = append(opts, tea.WithColorProfile(colorprofile.Ascii))
	}
	prog := tea.NewProgram(tui.New(eng, host, tuiOptions(c.cfg, os.Getenv, runtime.GOOS)), opts...)
	final, err := prog.Run()
	if err != nil {
		return fmt.Errorf("run the terminal UI: %w", err)
	}
	if m, ok := final.(tui.Model); ok {
		return m.Err()
	}
	return nil
}

// terminal returns stdin and stdout as files when both are terminals.
func terminal(stdin io.Reader, stdout io.Writer) (*os.File, *os.File, bool) {
	in, inOK := stdin.(*os.File)
	out, outOK := stdout.(*os.File)
	if !inOK || !outOK || !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil, nil, false
	}
	return in, out, true
}
