package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/safetext"
)

// common is what every command that connects reads first: the log level,
// the config with the command line's overrides, and the auto-forward
// override (nil when neither --auto nor --no-auto is given).
type common struct {
	cfg     config.Config
	cfgPath string
	auto    *bool
	level   string
}

func loadCommon(cmd *cobra.Command, f *rootFlags) (common, error) {
	level := f.logLevel
	if level == "" {
		level = os.Getenv("PFWD_LOG")
		if _, err := parseLogLevel(level); err != nil {
			return common{}, fmt.Errorf("invalid PFWD_LOG %q: use debug, info, warn or error, or unset it", level)
		}
	}
	if _, err := parseLogLevel(level); err != nil {
		return common{}, &usageError{fmt.Errorf("invalid --log-level %q: use debug, info, warn or error", level)}
	}
	if f.port < 0 || f.port > 65535 {
		return common{}, &usageError{fmt.Errorf("invalid port %d: use a number from 1 to 65535", f.port)}
	}
	cfg, cfgPath, err := loadConfig(cmd)
	if err != nil {
		return common{}, err
	}
	auto, err := f.applyTo(&cfg, cmd.Flags().Changed)
	if err != nil {
		return common{}, &usageError{err}
	}
	return common{cfg: cfg, cfgPath: cfgPath, auto: auto, level: level}, nil
}

// headlessLog opens pfwd.log at level; when it can't, it says so on stderr
// and logs nowhere.
func headlessLog(level string, stderr io.Writer) (*slog.Logger, func()) {
	logger, closeLog, err := openLog(level)
	if err != nil {
		fmt.Fprintf(stderr, "warning: %v; continuing without a log file\n", err)
		return slog.New(slog.DiscardHandler), func() {}
	}
	return logger, closeLog
}

// interruptible ends ctx on Ctrl+C or SIGTERM. After the first one the
// default handling is back, so a second Ctrl+C ends a command that is
// waiting for input.
func interruptible(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, func() { stop() })
	return ctx, stop
}

func newLsCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [host]",
		Short: "List detected ports and forwards",
		Long: `pfwd ls lists the daemon's hosts, their ports and forwards. With a host the
daemon isn't connected to, or no daemon, it connects to the host, lists
the TCP ports listening on it once, and disconnects. --json prints the
same as JSON for scripts.`,
		Example:           "  pfwd ls devbox\n  pfwd ls devbox --json",
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		ValidArgsFunction: completeHosts(f, true),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := ""
			if len(args) == 1 {
				host = args[0]
			}
			if ok, err := lsFromDaemon(cmd, host); ok || err != nil {
				return err
			}
			if host == "" {
				return &usageError{errors.New("name a host: `pfwd ls <host>` lists its ports once; with the daemon running (`pfwd daemon start`), `pfwd ls` lists all of its hosts")}
			}
			return lsOnce(cmd, f, host)
		},
	}
}

// lsOnce connects to target, waits for the first port snapshot, prints it
// and disconnects. Nothing is forwarded or remembered.
func lsOnce(cmd *cobra.Command, f *rootFlags, target string) error {
	c, err := loadCommon(cmd, f)
	if err != nil {
		return err
	}
	ctx, stop := interruptible(cmd.Context())
	defer stop()
	logger, closeLog := headlessLog(c.level, cmd.ErrOrStderr())
	defer closeLog()
	d := newHostDialer(c.cfg, f, logger)
	defer d.close()
	host, err := d.add(ctx, target, true)
	if err != nil {
		return err
	}
	off := false
	o := engineOptions(c.cfg, &off, nil)
	o.Settings = noForwards(o.Settings)
	o.Dial, o.Logger = d.Dial, logger
	eng := engine.New(o)
	defer func() { _ = eng.Close() }()

	wait := d.hostConfig(host).ConnectTimeout + 10*time.Second
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	eng.Connect(host)
	p := newTextPrompter(cmd.InOrStdin(), cmd.ErrOrStderr())
	if err := waitConnected(ctx, eng, p, host); err != nil {
		return timedOut(ctx, err, host, wait)
	}
	var snap engine.PortsChanged
	err = runEvents(ctx, eng, p, func(ev engine.Event) (bool, error) {
		pc, ok := ev.(engine.PortsChanged)
		if ok && pc.Host == host {
			snap = pc
		}
		return ok && pc.Host == host, nil
	})
	if err != nil {
		return timedOut(ctx, err, host, wait)
	}
	if snap.Strategy == detect.None {
		why := "found no ss, netstat, lsof or /proc there"
		if snap.Err != nil {
			why = snap.Err.Error()
		}
		return fmt.Errorf("can't list the ports on %s: %s; run `pfwd doctor %s` for details", host, why, host)
	}
	h := hostJSON{Host: string(host), State: engine.Connected.String(), OS: snap.OS, Forwards: []forwardJSON{}}
	for _, port := range snap.Ports {
		h.Ports = append(h.Ports, toPortJSON(port))
	}
	return printHosts(cmd, []hostJSON{h})
}

// timedOut turns an interrupt caused by the deadline into an error that
// says what took too long.
func timedOut(ctx context.Context, err error, host engine.HostID, wait time.Duration) error {
	if errors.Is(err, errInterrupted) && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("no port list from %s within %s; run `pfwd doctor %s` to see where it stops", host, wait, host)
	}
	return err
}

// printHosts prints hosts as pfwd ls does: a table per host, or JSON.
func printHosts(cmd *cobra.Command, hosts []hostJSON) error {
	for i := range hosts {
		if hosts[i].Ports == nil {
			hosts[i].Ports = []portJSON{}
		}
		slices.SortFunc(hosts[i].Ports, func(a, b portJSON) int { return cmp.Compare(a.Port, b.Port) })
	}
	if wantJSON(cmd) {
		return writeJSON(cmd.OutOrStdout(), map[string]any{"hosts": hosts})
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	for i, h := range hosts {
		if len(hosts) > 1 {
			if i > 0 {
				fmt.Fprintln(tw)
			}
			fmt.Fprintf(tw, "%s (%s)\n", safetext.Clean(h.Host), h.State)
		}
		fmt.Fprintln(tw, "PORT\tADDRESS\tPROCESS\tLABEL\tFORWARD")
		for _, p := range h.Ports {
			fmt.Fprintf(tw, ":%d\t%s\t%s\t%s\t%s\n", p.Port, strings.Join(p.Addrs, ","), dashIfEmpty(safetext.Clean(p.Process)),
				dashIfEmpty(safetext.Clean(p.Label)), forwardOf(h.Forwards, p.Port))
		}
		for _, fw := range h.Forwards {
			if fw.Kind != engine.Local.String() || !slices.ContainsFunc(h.Ports, func(p portJSON) bool { return p.Port == fw.Remote }) {
				fmt.Fprintf(tw, ":%d\t\t%s\t\t%s\n", fw.Remote, fw.Kind, forwardText(fw))
			}
		}
	}
	return tw.Flush()
}

// forwardOf is the FORWARD column for a port: the local forward of it, if
// any.
func forwardOf(fws []forwardJSON, port uint16) string {
	for _, fw := range fws {
		if fw.Kind == engine.Local.String() && fw.Remote == port {
			return forwardText(fw)
		}
	}
	return "—"
}

func forwardText(fw forwardJSON) string {
	addr := fw.Bind
	if addr == "" || addr == "127.0.0.1" {
		addr = "localhost"
	}
	if fw.Kind == engine.Remote.String() {
		addr = "localhost"
	}
	text := addr + ":" + strconv.Itoa(int(fw.Local)) + " " + fw.State
	if fw.Pause != "" {
		text += " (" + fw.Pause + ")"
	}
	return text
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
