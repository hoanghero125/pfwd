package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// stubCmds returns the subcommands from docs/SPEC.md "CLI design" whose
// behavior lands in later milestones. Their arguments are validated already,
// so scripts get the final usage errors (exit 2) from day one.
func stubCmds() []*cobra.Command {
	return []*cobra.Command{
		stub("fwd <host> <spec>...", "Create forwards on a host", "M5", cobra.MinimumNArgs(2),
			"  pfwd fwd devbox 5432 8080:3000"),
		stub("ls [host]", "List detected ports and forwards", "M5", cobra.MaximumNArgs(1),
			"  pfwd ls devbox --json"),
		stub("stop <id|host:port>...", "Stop forwards in the daemon", "M5", cobra.MinimumNArgs(1),
			"  pfwd stop devbox:5432"),
		stub("hosts", "List hosts from ssh config with last-used time", "M5", cobra.NoArgs, ""),
		group("daemon", "Run the engine in the background",
			stub("start", "Start the daemon", "M5", cobra.NoArgs, ""),
			stub("stop", "Stop the daemon and all its forwards", "M5", cobra.NoArgs, ""),
			stub("status", "Show whether the daemon is running", "M5", cobra.NoArgs, ""),
		),
		stub("attach", "Open the TUI on the running daemon", "M5", cobra.NoArgs, ""),
		stub("doctor [host]", "Diagnose config, auth, detection and port binding for a host", "M5", cobra.MaximumNArgs(1),
			"  pfwd doctor devbox"),
		group("config", "Manage the config file",
			stub("path", "Print the config file path", "M2", cobra.NoArgs, ""),
			stub("edit", "Open the config file in your editor", "M2", cobra.NoArgs, ""),
			stub("validate", "Check the config file for unknown keys and bad values", "M2", cobra.NoArgs, ""),
		),
		stub("ui [host...]", "Open the web UI in the browser", "M6", cobra.ArbitraryArgs,
			"  pfwd ui devbox"),
	}
}

// stub builds a command that validates its arguments and then reports that
// it is not implemented yet.
func stub(use, short, milestone string, args cobra.PositionalArgs, example string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Example: example,
		Args:    usageArgs(args),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("%q is not implemented yet (planned for milestone %s in docs/SPEC.md)", cmd.CommandPath(), milestone)
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
