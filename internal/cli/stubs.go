package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// stubCmds returns the planned subcommands whose behavior lands in later
// releases. Their positional arguments are
// validated already (bad ones exit 2); flags such as `stop --all` and
// `fwd -R/-D/--detach` arrive with each command's implementation.
func stubCmds() []*cobra.Command {
	return []*cobra.Command{
		stub("stop <id|host:port>...", "Stop forwards in the daemon", "v0.5.0", cobra.MinimumNArgs(1),
			"  pfwd stop devbox:5432"),
		group("daemon", "Run the engine in the background",
			stub("start", "Start the daemon", "v0.5.0", cobra.NoArgs, ""),
			stub("stop", "Stop the daemon and all its forwards", "v0.5.0", cobra.NoArgs, ""),
			stub("status", "Show whether the daemon is running", "v0.5.0", cobra.NoArgs, ""),
		),
		stub("attach", "Open the TUI on the running daemon", "v0.5.0", cobra.NoArgs, ""),
		stub("ui [host...]", "Open the web UI in the browser", "v0.6.0", cobra.ArbitraryArgs,
			"  pfwd ui devbox"),
	}
}

// stub builds a command that validates its arguments and then reports that
// it is not implemented yet.
func stub(use, short, release string, args cobra.PositionalArgs, example string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Example: example,
		Args:    usageArgs(args),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("%q is not implemented yet (planned for %s)", cmd.CommandPath(), release)
		},
	}
}

// group builds a parent command such as "daemon". Without a RunE, cobra
// prints help and exits 0 for "pfwd daemon bogus"; with one, the extra
// argument is a usage error.
func group(use, short string, children ...*cobra.Command) *cobra.Command {
	parent := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  usageArgs(cobra.NoArgs),
		RunE:  func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	parent.AddCommand(children...)
	return parent
}
