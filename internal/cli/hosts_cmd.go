package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/state"
)

// hostRow is one line of pfwd hosts.
type hostRow struct {
	Host     string
	LastUsed time.Time // zero when never used
	Forwards int
}

// newHostsCmd is pfwd hosts: the ssh config's hosts and the ones pfwd
// remembers, most recently used first.
func newHostsCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "hosts",
		Short: "List hosts from ssh config with last-used time",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, err := listHosts(f.sshConfig)
			if err != nil {
				return err
			}
			if wantJSON(cmd) {
				type hostJSON struct {
					Host     string     `json:"host"`
					LastUsed *time.Time `json:"last_used"`
					Forwards int        `json:"forwards"`
				}
				out := make([]hostJSON, 0, len(rows))
				for _, r := range rows {
					h := hostJSON{Host: r.Host, Forwards: r.Forwards}
					if !r.LastUsed.IsZero() {
						h.LastUsed = &r.LastUsed
					}
					out = append(out, h)
				}
				return writeJSON(cmd.OutOrStdout(), out)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "HOST\tLAST USED\tFORWARDS")
			now := time.Now()
			for _, r := range rows {
				last := "never"
				if !r.LastUsed.IsZero() {
					last = ago(now.Sub(r.LastUsed))
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\n", safetext.Clean(r.Host), last, r.Forwards)
			}
			return tw.Flush()
		},
	}
}

// listHosts merges the ssh config's hosts with the ones state.json
// remembers: most recently used first, then the never-used ones by name.
func listHosts(sshConfig string) ([]hostRow, error) {
	names, err := sshx.ConfigHosts(sshConfig, sshx.DefaultEnv())
	if err != nil {
		return nil, fmt.Errorf("%w; check the file, or name another with -F", err)
	}
	info := map[string]state.HostInfo{}
	if path, err := config.StatePath(); err == nil {
		if info, err = state.Open(path, state.Options{}).Hosts(); err != nil {
			return nil, err
		}
	}
	rows := make([]hostRow, 0, len(names)+len(info))
	listed := make(map[string]bool)
	for _, n := range names {
		listed[n] = true
		rows = append(rows, hostRow{Host: n, LastUsed: info[n].LastUsed, Forwards: info[n].Forwards})
	}
	for n, h := range info {
		if !listed[n] {
			rows = append(rows, hostRow{Host: n, LastUsed: h.LastUsed, Forwards: h.Forwards})
		}
	}
	slices.SortFunc(rows, func(a, b hostRow) int {
		return cmp.Or(b.LastUsed.Compare(a.LastUsed), strings.Compare(a.Host, b.Host))
	})
	return rows, nil
}

// ago says how long ago something was, in one unit.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// completeHosts completes host arguments from pfwd hosts. With firstOnly,
// only the first argument is a host.
func completeHosts(f *rootFlags, firstOnly bool) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if firstOnly && len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		rows, err := listHosts(f.sshConfig)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var out []string
		for _, r := range rows {
			if strings.HasPrefix(r.Host, toComplete) && !slices.Contains(args, r.Host) {
				out = append(out, r.Host)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
