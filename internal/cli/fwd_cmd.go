package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/safetext"
)

// bindError is a forward that could not listen, here or on the server
// (exit 6).
type bindError struct{ err error }

func (e *bindError) Error() string { return e.err.Error() }
func (e *bindError) Unwrap() error { return e.err }

// fwdFlags are pfwd fwd's own flags.
type fwdFlags struct {
	remote, dynamic []string
}

func newFwdCmd(f *rootFlags) *cobra.Command {
	var ff fwdFlags
	cmd := &cobra.Command{
		Use:   "fwd <host> [spec]...",
		Short: "Create forwards on a host",
		Long: `pfwd fwd connects to a host, creates the forwards you name and keeps them
up, reconnecting when the connection drops, until you press Ctrl+C.

Specs use ssh's -L syntax:
  PORT                  local PORT to PORT on the server
  LOCAL:PORT            local LOCAL to PORT on the server
  LOCAL:HOST:PORT       local LOCAL to HOST:PORT, as the server sees it
  BIND:LOCAL:HOST:PORT  the same, listening on BIND
-R PORT[:LOCAL] exposes a local port on the server, and -D PORT starts a
SOCKS proxy. Auto-forward is off unless you pass --auto.`,
		Example:           "  pfwd fwd devbox 5432 8080:3000\n  pfwd fwd devbox -R 9000 -D 1080\n  pfwd fwd devbox 5432 --json",
		Args:              usageArgs(cobra.MinimumNArgs(1)),
		ValidArgsFunction: completeHosts(f, true),
		RunE: func(cmd *cobra.Command, args []string) error {
			specs, err := fwdSpecs(args[1:], ff)
			if err != nil {
				return &usageError{err}
			}
			return runFwd(cmd, f, args[0], specs)
		},
	}
	cmd.Flags().StringArrayVarP(&ff.remote, "remote", "R", nil, "expose a local port on the server: `PORT[:LOCAL]` (repeatable)")
	cmd.Flags().StringArrayVarP(&ff.dynamic, "dynamic", "D", nil, "start a SOCKS proxy on a local `PORT` (repeatable)")
	return cmd
}

// fwdSpecs parses the positional specs and the -R and -D values.
func fwdSpecs(args []string, ff fwdFlags) ([]engine.ForwardSpec, error) {
	var rules []config.ForwardRule
	add := func(spec string) error {
		r, err := config.ParseForward(spec)
		if err != nil {
			return err
		}
		rules = append(rules, r)
		return nil
	}
	for _, a := range args {
		if err := add(a); err != nil {
			return nil, err
		}
	}
	for _, r := range ff.remote {
		if err := add("-R " + r); err != nil {
			return nil, err
		}
	}
	for _, d := range ff.dynamic {
		if err := add("-D " + d); err != nil {
			return nil, err
		}
	}
	if len(rules) == 0 {
		return nil, errors.New("name at least one forward, like `pfwd fwd devbox 5432`, or use -R or -D")
	}
	return configForwards(rules), nil
}

// runFwd connects to target, creates specs, and reports what happens to
// them until interrupted.
func runFwd(cmd *cobra.Command, f *rootFlags, target string, specs []engine.ForwardSpec) error {
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
	auto := c.auto
	if auto == nil {
		off := false
		auto = &off
	}
	o := engineOptions(c.cfg, auto, nil)
	o.Settings = noForwards(o.Settings)
	o.Dial, o.Logger = d.Dial, logger
	eng := engine.New(o)
	defer func() { _ = eng.Close() }()

	eng.Connect(host)
	p := newTextPrompter(cmd.InOrStdin(), cmd.ErrOrStderr())
	if err := waitConnected(ctx, eng, p, host); err != nil {
		return err
	}
	out := &fwdPrinter{w: cmd.OutOrStdout(), errW: cmd.ErrOrStderr(), json: wantJSON(cmd), seen: map[string]engine.Forward{}}
	var failed []error
	for _, spec := range specs {
		fw, err := eng.ForwardOrNearby(host, spec)
		if err != nil {
			failed = append(failed, err)
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", safetext.Clean(err.Error()))
			continue
		}
		out.forwardChanged(fw)
	}
	switch {
	case len(failed) == len(specs) && len(failed) == 1:
		return &bindError{failed[0]}
	case len(failed) == len(specs):
		return &bindError{fmt.Errorf("none of the %d forwards could listen; see the errors above", len(failed))}
	}
	err = runEvents(ctx, eng, p, func(ev engine.Event) (bool, error) {
		switch ev := ev.(type) {
		case engine.ForwardChanged:
			if ev.Forward.Host == host {
				out.forwardChanged(ev.Forward)
			}
		case engine.SessionChanged:
			if ev.Host != host {
				return false, nil
			}
			out.sessionChanged(ev)
			if ev.State == engine.SessionFailed {
				return true, ev.Err
			}
		}
		return false, nil
	})
	if len(failed) > 0 && errors.Is(err, errInterrupted) {
		return &bindError{fmt.Errorf("%d of %d forwards could not listen; see the errors above", len(failed), len(specs))}
	}
	return err
}

// fwdPrinter reports forwards and the connection as they change: a line
// each, or one JSON object per line with --json.
type fwdPrinter struct {
	w, errW io.Writer
	json    bool
	seen    map[string]engine.Forward
}

func (p *fwdPrinter) emit(v any) {
	data, err := json.Marshal(v)
	if err == nil {
		fmt.Fprintf(p.w, "%s\n", data)
	}
}

// forwardChanged reports a new forward, or one whose state changed.
func (p *fwdPrinter) forwardChanged(fw engine.Forward) {
	prev, had := p.seen[fw.ID]
	p.seen[fw.ID] = fw
	if had && prev.State == fw.State && prev.Pause == fw.Pause {
		return
	}
	if p.json {
		p.emit(map[string]any{"event": "forward", "forward": toForwardJSON(fw)})
		return
	}
	name := forwardName(fw)
	switch {
	case !had && fw.State != engine.Failed:
		text := describeForward(fw)
		if fw.Spec.Auto {
			text += " (auto)"
		}
		fmt.Fprintln(p.w, text)
	case fw.State == engine.Paused:
		fmt.Fprintf(p.w, "%s paused: %s\n", name, fw.Pause)
	case fw.State == engine.Active:
		fmt.Fprintf(p.w, "%s active again\n", name)
	case fw.State == engine.Failed:
		reason := "it failed"
		if fw.Err != nil {
			reason = safetext.Clean(fw.Err.Error())
		}
		fmt.Fprintf(p.errW, "%s failed: %s\n", name, reason)
	case fw.State == engine.Stopped:
		fmt.Fprintf(p.w, "Stopped %s\n", name)
	}
}

// sessionChanged reports a lost connection and the reconnect.
func (p *fwdPrinter) sessionChanged(s engine.SessionChanged) {
	if p.json {
		v := map[string]any{"event": "session", "host": string(s.Host), "state": s.State.String(), "error": ""}
		if s.Err != nil {
			v["error"] = s.Err.Error()
		}
		p.emit(v)
		return
	}
	host := safetext.Clean(string(s.Host))
	switch {
	case s.State == engine.Reconnecting && s.Attempt == 1 && !s.RetryAt.IsZero():
		why := "the connection dropped"
		if s.Err != nil {
			why = safetext.Clean(s.Err.Error())
		}
		fmt.Fprintf(p.errW, "Lost the connection to %s: %s; reconnecting\n", host, why)
	case s.State == engine.Connected:
		fmt.Fprintf(p.w, "Reconnected to %s\n", host)
	}
}
