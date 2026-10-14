package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/notify"
	"github.com/hoanghero125/pfwd/internal/safetext"
)

// daemonWait bounds waiting for the daemon to start or stop.
const daemonWait = 5 * time.Second

func newDaemonCmd(f *rootFlags) *cobra.Command {
	return group("daemon", "Run the engine in the background",
		&cobra.Command{
			Use:   "start",
			Short: "Start the daemon",
			Long: `pfwd daemon start runs pfwd in the background. pfwd fwd --detach, pfwd ls,
pfwd stop and pfwd attach then use it, and its forwards outlive the
terminal. It logs to pfwd.log.`,
			Args: usageArgs(cobra.NoArgs),
			RunE: func(cmd *cobra.Command, _ []string) error { return daemonStart(cmd) },
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the daemon and all its forwards",
			Args:  usageArgs(cobra.NoArgs),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return daemonStop(cmd)
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show whether the daemon is running",
			Args:  usageArgs(cobra.NoArgs),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return daemonStatus(cmd)
			},
		},
		&cobra.Command{
			Use:    "run",
			Short:  "Run the daemon in the foreground (pfwd daemon start runs this)",
			Hidden: true,
			Args:   usageArgs(cobra.NoArgs),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return daemonRun(cmd, f)
			},
		},
	)
}

// daemonRun serves the engine over IPC until shutdown or an interrupt.
func daemonRun(cmd *cobra.Command, f *rootFlags) error {
	c, err := loadCommon(cmd, f)
	if err != nil {
		return err
	}
	path := ipc.DefaultPath()
	ln, err := ipc.Listen(path)
	if err != nil {
		return err
	}
	logger, closeLog := headlessLog(c.level, cmd.ErrOrStderr())
	defer closeLog()
	logger.Info("daemon starting", "pid", os.Getpid(), "socket", path)

	ctx, stop := interruptible(cmd.Context())
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	d := newHostDialer(c.cfg, f, logger)
	defer d.close()
	var notifier engine.Notifier
	if c.cfg.UI.Notifications {
		notifier = notify.New("pfwd")
	}
	o := engineOptions(c.cfg, c.auto, notifier)
	o.Settings = d.settings(o.Settings)
	o.Dial, o.Logger, o.State = d.Dial, logger, stateStore(c.cfg, logger)
	eng := engine.New(o)

	h := newDaemonHandler(eng, d, path, logger)
	h.cfgPath, h.ctx = c.cfgPath, ctx
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		h.run(ctx)
	}()
	stopper := make(chan struct{})
	go func() {
		defer close(stopper)
		select {
		case <-h.stopping:
			// Let the shutdown call's reply go out first.
			time.Sleep(100 * time.Millisecond)
			cancel()
		case <-ctx.Done():
		}
	}()
	serveErr := ipc.NewServer(h, logger).Serve(ctx, ln)
	cancel()
	<-ran
	<-stopper
	h.webMu.Lock() // no web UI starts after this
	h.webDone.Wait()
	h.webMu.Unlock()
	if err := eng.Close(); err != nil {
		logger.Warn("engine shutdown", "err", err)
	}
	logger.Info("daemon stopped")
	return serveErr
}

// daemonFlags are the flags a started daemon inherits from the command
// line.
var daemonFlags = []string{"config", "ssh-config", "log-level", "accept-new", "bind", "allow-lan-socks", "interval", "auto", "no-auto"}

// daemonStart starts `pfwd daemon run` in the background and waits until it
// answers.
func daemonStart(cmd *cobra.Command) error {
	path := ipc.DefaultPath()
	if c, err := ipc.Dial(cmd.Context(), path); err == nil {
		_ = c.Close()
		return ipc.ErrRunning
	}
	st, err := spawnDaemon(cmd, path, daemonFlags)
	if err != nil {
		return err
	}
	if wantJSON(cmd) {
		return writeJSON(cmd.OutOrStdout(), st)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "pfwd daemon started (pid %d) on %s\n", st.PID, st.Socket)
	return nil
}

// waitStatus polls the daemon at path until it answers status.
func waitStatus(ctx context.Context, path string, wait time.Duration) (statusJSON, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		c, err := ipc.Dial(ctx, path)
		if err == nil {
			var st statusJSON
			err = c.Call(ctx, "status", nil, &st)
			_ = c.Close()
			if err == nil {
				return st, nil
			}
		}
		select {
		case <-ctx.Done():
			return statusJSON{}, err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// daemonStop asks the daemon to shut down and waits until it is gone. With
// no daemon there is nothing to do.
func daemonStop(cmd *cobra.Command) error {
	path := ipc.DefaultPath()
	ctx := cmd.Context()
	c, err := ipc.Dial(ctx, path)
	if errors.Is(err, ipc.ErrNotRunning) {
		fmt.Fprintln(cmd.OutOrStdout(), "No pfwd daemon is running.")
		return nil
	}
	if err != nil {
		return err
	}
	var st statusJSON
	_ = c.Call(ctx, "status", nil, &st)
	err = c.Call(ctx, "shutdown", nil, nil)
	_ = c.Close()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(daemonWait)
	for time.Now().Before(deadline) {
		if c, err := ipc.Dial(ctx, path); errors.Is(err, ipc.ErrNotRunning) {
			fmt.Fprintf(cmd.OutOrStdout(), "pfwd daemon stopped; %d forwards ended.\n", st.Forwards)
			return nil
		} else if err == nil {
			_ = c.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the daemon (pid %d) is still running after %s; end it with your system's process tools", st.PID, daemonWait)
}

// daemonStatus prints the daemon's state; exit 1 when none runs.
func daemonStatus(cmd *cobra.Command) error {
	c, err := ipc.Dial(cmd.Context(), ipc.DefaultPath())
	if errors.Is(err, ipc.ErrNotRunning) {
		if wantJSON(cmd) {
			_ = writeJSON(cmd.OutOrStdout(), map[string]bool{"running": false})
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "No pfwd daemon is running; start one with `pfwd daemon start`.")
		}
		return &silentExit{exitError}
	}
	if err != nil {
		return err
	}
	defer c.Close()
	var st statusJSON
	if err := c.Call(cmd.Context(), "status", nil, &st); err != nil {
		return err
	}
	if wantJSON(cmd) {
		return writeJSON(cmd.OutOrStdout(), st)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "pfwd daemon %s running (pid %d) since %s on %s\n", st.Version, st.PID, st.StartedAt.Local().Format("15:04:05"), st.Socket)
	fmt.Fprintf(w, "%d forwards\n", st.Forwards)
	for _, h := range st.Hosts {
		fmt.Fprintf(w, "  %s: %s\n", safetext.Clean(h.Host), h.State)
	}
	return nil
}
