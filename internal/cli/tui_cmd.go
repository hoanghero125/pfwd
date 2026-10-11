package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/hoanghero125/pfwd/internal/engine"
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
	acceptNew  bool
	logLevel   string
}

func (f *rootFlags) register(fs *pflag.FlagSet) {
	fs.IntVarP(&f.port, "port", "p", 0, "SSH port, instead of the one in the ssh config")
	fs.StringVarP(&f.login, "login", "l", "", "SSH user, instead of the one in the ssh config")
	fs.StringArrayVarP(&f.identities, "identity", "i", nil, "private key file to try before the ssh config's keys (repeatable)")
	fs.StringVarP(&f.sshConfig, "ssh-config", "F", "", "ssh config file to read instead of ~/.ssh/config")
	fs.BoolVar(&f.acceptNew, "accept-new", false, "trust an unknown host key without asking; a changed key is still refused")
	fs.StringVar(&f.logLevel, "log-level", "", "how much goes into pfwd.log: debug, info, warn or error (default info, or $PFWD_LOG)")
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
	level := f.logLevel
	if level == "" {
		level = os.Getenv("PFWD_LOG")
	}
	if _, err := parseLogLevel(level); err != nil {
		return &usageError{err}
	}
	if f.port < 0 || f.port > 65535 {
		return &usageError{fmt.Errorf("invalid port %d: use a number from 1 to 65535", f.port)}
	}
	in, out, ok := terminal(cmd.InOrStdin(), cmd.OutOrStdout())
	if !ok {
		return errors.New("pfwd needs an interactive terminal; the scriptable commands (pfwd ls, pfwd fwd) arrive in v0.5.0")
	}

	logger, closeLog, err := openLog(level)
	if err != nil {
		// Printed before the TUI takes the screen, so it stays readable.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v; continuing without a log file\n", err)
		logger, closeLog = slog.New(slog.DiscardHandler), func() {}
	}
	defer closeLog()

	ctx := cmd.Context()
	hc, err := sshx.Resolve(ctx, target, sshx.Overrides{
		User: f.login, Port: f.port, Identities: f.identities, ConfigFile: f.sshConfig,
	}, sshx.DefaultEnv())
	if err != nil {
		return err
	}
	logger.Info("starting", "version", version.Get().Version, "host", hc.Alias, "hostname", hc.HostName, "port", hc.Port)

	eng := engine.New(engine.Options{
		Logger: logger,
		Dial: func(ctx context.Context, _ engine.HostID, p sshx.HostKeyPrompter) (engine.Conn, error) {
			c, err := sshx.Dial(ctx, hc, sshx.DialOptions{Prompter: p, AcceptNew: f.acceptNew, Logger: logger})
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	})
	defer func() {
		if err := eng.Close(); err != nil {
			logger.Warn("engine shutdown", "err", err)
		}
	}()
	host := engine.HostID(hc.Alias)
	eng.Connect(host)

	prog := tea.NewProgram(tui.New(eng, host), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
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
