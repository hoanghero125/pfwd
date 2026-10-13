// Package cli wires pfwd's command tree: the root command that opens the
// TUI, the scriptable subcommands, and the mapping from errors to exit
// codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/version"
)

// Exit codes, as the README documents them.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitConnect     = 3
	exitAuth        = 4
	exitHostKey     = 5
	exitBind        = 6
	exitInterrupted = 130
)

// Execute runs pfwd with args (without the program name), writes normal
// output to stdout and errors to stderr, and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	return executeContext(context.Background(), args, stdout, stderr)
}

// executeContext is Execute with a context that commands such as pfwd fwd
// stop on, as on Ctrl+C.
func executeContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if args == nil {
		args = []string{} // cobra would fall back to os.Args
	}
	root := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return exitOK
	}
	if errors.Is(err, errInterrupted) || errors.Is(err, context.Canceled) {
		return exitInterrupted // the user knows; nothing to explain
	}
	var quiet *silentExit
	if errors.As(err, &quiet) {
		return quiet.code // the command printed its own report
	}
	fmt.Fprint(stderr, errorLine(err))
	code := exitCode(err)
	if code == exitUsage {
		fmt.Fprintf(stderr, "Run '%s --help' for usage.\n", cmd.CommandPath())
	}
	return code
}

// errorLine formats err for stderr. Errors can carry text from the server,
// so it goes through safetext.Clean like everything the TUI draws. Config
// problems are listed one per line.
func errorLine(err error) string {
	var probs *config.Problems
	if errors.As(err, &probs) {
		var b strings.Builder
		b.WriteString("Error: fix the config file, then check it with `pfwd config validate`:\n")
		for _, line := range probs.Lines() {
			b.WriteString("  " + safetext.Clean(line) + "\n")
		}
		return b.String()
	}
	return "Error: " + safetext.Clean(err.Error()) + "\n"
}

// ipcExitCode maps the code of a daemon's error to an exit code.
func ipcExitCode(err error) int {
	var ie *ipc.Error
	if !errors.As(err, &ie) {
		return exitError
	}
	switch ie.Code {
	case ipc.CodeUsage:
		return exitUsage
	case ipc.CodeConnect:
		return exitConnect
	case ipc.CodeAuth:
		return exitAuth
	case ipc.CodeHostKey:
		return exitHostKey
	case ipc.CodeBind:
		return exitBind
	}
	return exitError
}

// exitCode maps an error to the documented exit codes.
func exitCode(err error) int {
	switch {
	case isUsageError(err):
		return exitUsage
	case errors.As(err, new(*bindError)):
		return exitBind
	case errors.As(err, new(*ipc.Error)):
		return ipcExitCode(err)
	case errors.Is(err, sshx.ErrHostKey):
		return exitHostKey
	case errors.Is(err, sshx.ErrAuth):
		return exitAuth
	case errors.Is(err, sshx.ErrConnect):
		return exitConnect
	default:
		return exitError
	}
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	var flags rootFlags
	root := &cobra.Command{
		Use:   "pfwd <host>...",
		Short: "Detect and forward remote ports over SSH",
		Long: `pfwd is the VS Code Ports panel for any terminal. It connects to a host from
~/.ssh/config, watches which TCP ports start listening on it, and forwards
them to localhost with one keypress.

Name the host the way you would for ssh: an alias from ~/.ssh/config, or
user@host. The flags below override the ssh config.`,
		Example:       "  pfwd devbox\n  pfwd devbox gpu-node\n  pfwd alice@10.0.0.5 -p 2222 -i ~/.ssh/id_work",
		Version:       version.Get().String(),
		Args:          hostArgs,
		SilenceErrors: true, // Execute prints errors itself
		SilenceUsage:  true, // no usage dump on runtime errors
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd, args, &flags)
		},
	}
	// The ssh and forwarding flags apply to every command that connects.
	flags.register(root.PersistentFlags())
	root.ValidArgsFunction = completeHosts(&flags, false)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")
	// Inherited by every subcommand: bad flags are usage errors (exit 2).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err}
	})
	root.PersistentFlags().Bool("json", false, "print JSON instead of human-readable output")
	root.PersistentFlags().String("config", "", "`path` of the config file to use, instead of $PFWD_CONFIG or the default that pfwd config path prints")
	root.AddCommand(newVersionCmd(), newConfigCmd())
	root.AddCommand(newHostsCmd(&flags), newLsCmd(&flags), newFwdCmd(&flags), newDoctorCmd(&flags), newDaemonCmd(&flags), newStopCmd(), newAttachCmd(&flags))
	root.AddCommand(stubCmds()...)
	wireCompletion(root) // after SetOut: cobra captures the writer when it builds the shell commands
	return root
}

// wireCompletion adds cobra's "completion" command up front (cobra would
// otherwise add it lazily) and makes bad arguments usage errors. cobra's own
// version prints help and exits 0 for an unknown shell, so a typo in
// `source <(pfwd completion bsh)` would make the shell evaluate the help text.
func wireCompletion(root *cobra.Command) {
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() != "completion" {
			continue
		}
		c.Args = usageArgs(cobra.NoArgs)
		c.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
		for _, shell := range c.Commands() {
			shell.Args = usageArgs(cobra.NoArgs)
		}
	}
}

// usageError marks a bad invocation (unknown subcommand, bad flag, wrong
// number of arguments) so that Execute exits with code 2 instead of 1.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// usageArgs wraps a cobra argument validator so that its failures are usage errors.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return &usageError{err}
		}
		return nil
	}
}

// isUsageError reports whether err comes from a bad invocation.
func isUsageError(err error) bool {
	var ue *usageError
	return errors.As(err, &ue)
}
