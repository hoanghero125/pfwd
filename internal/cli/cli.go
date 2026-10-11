// Package cli wires pfwd's command tree: the root command that opens the
// TUI, the scriptable subcommands, and the mapping from errors to exit
// codes.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/version"
)

// Exit codes. Later releases add 6 (bind
// failed, for pfwd fwd) and 130 (interrupted).
const (
	exitOK      = 0
	exitError   = 1
	exitUsage   = 2
	exitConnect = 3
	exitAuth    = 4
	exitHostKey = 5
)

// Execute runs pfwd with args (without the program name), writes normal
// output to stdout and errors to stderr, and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	if args == nil {
		args = []string{} // cobra would fall back to os.Args
	}
	root := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	cmd, err := root.ExecuteC()
	if err == nil {
		return exitOK
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

// exitCode maps an error to the documented exit codes.
func exitCode(err error) int {
	switch {
	case isUsageError(err):
		return exitUsage
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
		Use:   "pfwd <host>",
		Short: "Detect and forward remote ports over SSH",
		Long: `pfwd is the VS Code Ports panel for any terminal. It connects to a host from
~/.ssh/config, watches which TCP ports start listening on it, and forwards
them to localhost with one keypress.

Name the host the way you would for ssh: an alias from ~/.ssh/config, or
user@host. The flags below override the ssh config.`,
		Example:       "  pfwd devbox\n  pfwd alice@10.0.0.5 -p 2222 -i ~/.ssh/id_work",
		Version:       version.Get().String(),
		Args:          hostArgs,
		SilenceErrors: true, // Execute prints errors itself
		SilenceUsage:  true, // no usage dump on runtime errors
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd, args[0], &flags)
		},
	}
	flags.register(root.Flags())
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")
	// Inherited by every subcommand: bad flags are usage errors (exit 2).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err}
	})
	root.PersistentFlags().Bool("json", false, "print JSON instead of human-readable output")
	root.PersistentFlags().String("config", "", "config file to use (default from $PFWD_CONFIG or `pfwd config path`)")
	root.AddCommand(newVersionCmd(), newConfigCmd())
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
