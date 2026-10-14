package cli

import (
	"github.com/spf13/cobra"
)

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
