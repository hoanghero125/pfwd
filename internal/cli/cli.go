// Package cli wires pfwd's command tree: the root command that opens the
// TUI, the scriptable subcommands, and the mapping from errors to the exit
// codes documented in docs/SPEC.md ("CLI design").
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/version"
)

// Exit codes from docs/SPEC.md "CLI design". Later milestones add 3-6
// (connection, auth, host key, bind) and 130 (interrupted).
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
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
	fmt.Fprintf(stderr, "Error: %v\n", err)
	if isUsageError(err) {
		fmt.Fprintf(stderr, "Run '%s --help' for usage.\n", cmd.CommandPath())
		return exitUsage
	}
	return exitError
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "pfwd [host...]",
		Short: "Detect and forward remote ports over SSH",
		Long: `pfwd is the VS Code Ports panel for any terminal. It connects to hosts from
~/.ssh/config, watches which TCP ports start listening on the remote machine,
and forwards them to localhost with one keypress or automatically.

Run pfwd with no arguments to pick a host, or name hosts to open them.`,
		Example:       "  pfwd devbox\n  pfwd devbox gpu-node",
		Version:       version.Get().String(),
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true, // Execute prints errors itself
		SilenceUsage:  true, // no usage dump on runtime errors
		RunE: func(*cobra.Command, []string) error {
			return errors.New("the TUI is not implemented yet (planned for milestone M1 in docs/SPEC.md)")
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")
	// Inherited by every subcommand: bad flags are usage errors (exit 2).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err}
	})
	root.PersistentFlags().Bool("json", false, "print JSON instead of human-readable output")
	root.AddCommand(newVersionCmd())
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
