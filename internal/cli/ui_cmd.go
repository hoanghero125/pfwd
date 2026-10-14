package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/desktop"
	"github.com/hoanghero125/pfwd/internal/ipc"
)

// uiParams asks the daemon for the web UI: started on port (0 for any)
// the first time.
type uiParams struct {
	Port int `json:"port"`
}

// uiJSON is where the web UI is and a fresh one-time login URL.
type uiJSON struct {
	URL     string `json:"url"`
	Address string `json:"address"`
	// Reused is true when the server was already running.
	Reused bool `json:"reused"`
}

func newUICmd(f *rootFlags) *cobra.Command {
	var port int
	var noOpen, printURL bool
	cmd := &cobra.Command{
		Use:   "ui [host...]",
		Short: "Open the web UI in the browser",
		Long: `pfwd ui opens the web UI: the TUI's controls in a browser tab. It starts
the daemon if it isn't running (the web UI is one of its front ends, like
pfwd attach), connects the hosts you name, and opens a one-time login link
in your browser. Closing the tab leaves the forwards running; pfwd daemon
stop ends them.

The web UI listens on 127.0.0.1 only. The login link works once, for 10
minutes; run pfwd ui again for another. Host key, password and passphrase
questions show up in the page.

Like pfwd fwd --detach, the daemon reads hosts from your ssh config, so
-p, -l, -i, -J and -F are refused; set them in ~/.ssh/config.`,
		Example: `  pfwd ui
  pfwd ui devbox gpu-node
  pfwd ui --no-open      # print the login link instead of opening it
  pfwd ui --url          # print only the link, for scripts`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeHosts(f, false),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(cmd, args, port, noOpen, printURL)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "`port` for the web UI on 127.0.0.1 when the daemon starts it (default: any free port)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the login link instead of opening the browser")
	cmd.Flags().BoolVar(&printURL, "url", false, "print only the login link")
	return cmd
}

func runUI(cmd *cobra.Command, hosts []string, port int, noOpen, printURL bool) error {
	for _, name := range detachRefused {
		if name == "port" {
			continue // pfwd ui's own --port is the web UI's
		}
		if fl := cmd.Flags().Lookup(name); fl != nil && fl.Changed {
			return &usageError{fmt.Errorf("--%s doesn't work with pfwd ui: the daemon reads hosts from your ssh config (and LAN SOCKS from config.toml); set it there", name)}
		}
	}
	if port < 0 || port > 65535 {
		return &usageError{fmt.Errorf("invalid --port %d: use a number from 1 to 65535", port)}
	}
	ctx, stop := interruptible(cmd.Context())
	defer stop()
	path := ipc.DefaultPath()
	client, err := ipc.Dial(ctx, path)
	if errors.Is(err, ipc.ErrNotRunning) {
		if _, err := spawnDaemon(cmd, path, []string{"config", "log-level", "interval"}); err != nil {
			return err
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Started the pfwd daemon.")
		client, err = ipc.Dial(ctx, path)
	}
	if err != nil {
		return interrupted(ctx, err)
	}
	defer client.Close()
	for _, h := range slices.Compact(slices.Clone(hosts)) {
		if err := client.Call(ctx, "connect", connectParams{Target: h}, nil); err != nil {
			return interrupted(ctx, err)
		}
	}
	var res uiJSON
	if err := client.Call(ctx, "ui", uiParams{Port: port}, &res); err != nil {
		return interrupted(ctx, err)
	}
	out := cmd.OutOrStdout()
	if printURL {
		fmt.Fprintln(out, res.URL)
		return nil
	}
	if res.Reused && port != 0 && res.Address != fmt.Sprintf("http://127.0.0.1:%d", port) {
		fmt.Fprintf(cmd.ErrOrStderr(), "The web UI already runs at %s; --port applies when the daemon starts it.\n", res.Address)
	}
	if !noOpen {
		err := desktop.OpenURL(ctx, res.URL)
		if err == nil {
			fmt.Fprintf(out, "Opened the pfwd web UI (%s) in your browser.\n", res.Address)
			return nil
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Couldn't open a browser: %v\n", err)
	}
	fmt.Fprintf(out, "Open this link in your browser; it works once, for 10 minutes:\n  %s\n", res.URL)
	return nil
}
