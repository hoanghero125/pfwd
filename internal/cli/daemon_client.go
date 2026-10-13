package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// spawnDaemon starts `pfwd daemon run` in the background, passing on the
// flags in pass that the command line set, and waits until it answers.
func spawnDaemon(cmd *cobra.Command, path string, pass []string) (statusJSON, error) {
	exe, err := os.Executable()
	if err != nil {
		return statusJSON{}, fmt.Errorf("find the pfwd binary: %w", err)
	}
	if base := strings.TrimSuffix(filepath.Base(exe), ".exe"); strings.HasSuffix(base, ".test") {
		// A test binary would run its tests again, detached, and start
		// daemons of its own.
		return statusJSON{}, errors.New("pfwd daemon start must run from the pfwd binary, not a test binary")
	}
	args := []string{"daemon", "run"}
	for _, name := range pass {
		if fl := cmd.Flags().Lookup(name); fl != nil && fl.Changed {
			args = append(args, "--"+name+"="+fl.Value.String())
		}
	}
	run := exec.Command(exe, args...)
	detach(run)
	if err := run.Start(); err != nil {
		return statusJSON{}, fmt.Errorf("start the daemon: %w", err)
	}
	pid := run.Process.Pid
	_ = run.Process.Release()
	st, err := waitStatus(cmd.Context(), path, daemonWait)
	if err != nil {
		return statusJSON{}, fmt.Errorf("the daemon (pid %d) didn't answer within %s: %w; its log says why (pfwd doctor --bundle collects it)", pid, daemonWait, err)
	}
	return st, nil
}

// remotePrompter answers, on this terminal, the daemon's prompts for one
// host. Without a terminal it declines them, and err then says why, with
// the fix. With acceptNew it trusts a new host key without asking.
type remotePrompter struct {
	t         *textPrompter
	path      string
	host      string
	acceptNew bool
	mu        sync.Mutex
	err       error
	// cancelAsk ends the question on screen, once another client answered
	// the prompt with this ID.
	askingID  string
	cancelAsk context.CancelFunc
}

// run watches the daemon and answers host's prompts until ctx ends. The
// terminal is read in its own goroutine, so the watch keeps going: a
// prompt another client answers ends the question here.
func (r *remotePrompter) run(ctx context.Context) {
	answers, err := ipc.Dial(ctx, r.path)
	if err != nil {
		return
	}
	defer answers.Close()
	prompts := make(chan promptJSON, 16)
	asked := make(chan struct{})
	go func() {
		defer close(asked)
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-prompts:
				r.answer(ctx, answers, p)
			}
		}
	}()
	_ = ipc.Watch(ctx, r.path, func(ev ipc.Event) {
		switch ev.Event {
		case "prompt":
			var p promptJSON
			if json.Unmarshal(ev.Data, &p) == nil && p.Session == r.host {
				select {
				case prompts <- p:
				default:
				}
			}
		case "prompt_done":
			var done struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(ev.Data, &done) == nil {
				r.mu.Lock()
				if r.askingID == done.ID && r.cancelAsk != nil {
					r.cancelAsk()
				}
				r.mu.Unlock()
			}
		}
	})
	<-asked
}

// answer asks one prompt and sends the answer, unless another client
// answered it first or ctx ended.
func (r *remotePrompter) answer(ctx context.Context, answers *ipc.Client, p promptJSON) {
	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	r.askingID, r.cancelAsk = p.ID, cancel
	r.mu.Unlock()
	a := r.ask(askCtx, p)
	r.mu.Lock()
	r.askingID, r.cancelAsk = "", nil
	answeredElsewhere := askCtx.Err() != nil && ctx.Err() == nil
	if answeredElsewhere && errors.Is(r.t.err, errInterrupted) {
		r.t.err = nil // not an interrupt: the prompt went away
	}
	if r.t.err != nil && r.err == nil {
		r.err = r.t.err
	}
	r.mu.Unlock()
	if answeredElsewhere {
		fmt.Fprintln(r.t.out, "Another pfwd answered that.")
		return
	}
	if ctx.Err() == nil {
		_ = answers.Call(ctx, "answer", a, nil)
	}
	if a.Secret != nil {
		*a.Secret = ""
	}
}

// ask asks the user one prompt, with the same questions as pfwd fwd.
func (r *remotePrompter) ask(ctx context.Context, p promptJSON) answerJSON {
	a := answerJSON{ID: p.ID}
	host := engine.HostID(p.Session)
	if p.Kind == "host_key" {
		if r.acceptNew {
			fmt.Fprintf(r.t.out, "Trusting %s's new host key (%s %s): --accept-new.\n", safetext.Clean(p.Host), p.KeyType, p.Fingerprint)
			a.Accept = true
			return a
		}
		reply := make(chan bool, 1)
		r.t.hostKey(ctx, engine.NewHostKeyPrompt(host, sshx.HostKeyQuestion{Host: p.Host, KeyType: p.KeyType, Fingerprint: p.Fingerprint, Saves: p.Saves}, reply))
		a.Accept = <-reply
		return a
	}
	kind := map[string]sshx.CredentialKind{"passphrase": sshx.Passphrase, "password": sshx.Password}[p.Kind]
	if p.Kind == "code" {
		kind = sshx.Code
	}
	reply := make(chan engine.CredentialAnswer, 1)
	r.t.credential(ctx, engine.NewCredentialPrompt(host, sshx.CredentialQuestion{Host: p.Host, Kind: kind, Prompt: p.Prompt,
		Instruction: p.Instruction, Echo: p.Echo, Retry: p.Retry}, reply))
	ans := <-reply
	if ans.Canceled {
		a.Cancel = true
	} else {
		secret := string(ans.Secret)
		clear(ans.Secret)
		a.Secret = &secret
	}
	return a
}

func (r *remotePrompter) declined() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// detachRefused are the flags pfwd fwd --detach can't pass on: the daemon
// reads hosts from the ssh config, and its LAN SOCKS setting from the
// config file.
var detachRefused = []string{"port", "login", "identity", "jump", "ssh-config", "allow-lan-socks"}

// fwdDetached hands the forwards to the daemon, starting it if needed,
// and returns once each one listens or failed.
func fwdDetached(cmd *cobra.Command, f *rootFlags, target string, specs []engine.ForwardSpec) error {
	for _, name := range detachRefused {
		if fl := cmd.Flags().Lookup(name); fl != nil && fl.Changed {
			return &usageError{fmt.Errorf("--%s doesn't work with --detach: the daemon reads hosts from your ssh config (and LAN SOCKS from config.toml); set it there, or run pfwd fwd without --detach", name)}
		}
	}
	c, err := loadCommon(cmd, f)
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("bind") {
		for i := range specs {
			if specs[i].Kind != engine.Remote && specs[i].BindAddr == "" {
				specs[i].BindAddr = c.cfg.Forward.Bind
			}
		}
	}
	ctx, stop := interruptible(cmd.Context())
	defer stop()
	path := ipc.DefaultPath()
	client, err := ipc.Dial(ctx, path)
	if errors.Is(err, ipc.ErrNotRunning) {
		// The daemon serves later commands too, so it gets only the
		// settings every one of them shares.
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
	var res struct {
		Host string `json:"host"`
	}
	if err := client.Call(ctx, "connect", connectParams{Target: target}, &res); err != nil {
		return interrupted(ctx, err)
	}
	if c.auto != nil {
		if err := client.Call(ctx, "set_auto", autoParams{Host: res.Host, On: *c.auto}, nil); err != nil {
			return interrupted(ctx, err)
		}
	}
	rp := &remotePrompter{t: newTextPrompter(cmd.InOrStdin(), cmd.ErrOrStderr()), path: path, host: res.Host, acceptNew: f.acceptNew}
	watchCtx, cancelWatch := context.WithCancel(ctx)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		rp.run(watchCtx)
	}()
	defer func() {
		cancelWatch()
		<-watched
	}()

	out := &fwdPrinter{w: cmd.OutOrStdout(), errW: cmd.ErrOrStderr(), json: wantJSON(cmd), seen: map[string]engine.Forward{}}
	var failed []error
	for _, spec := range specs {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		var fj forwardJSON
		err := client.Call(callCtx, "forward", forwardParams{Host: res.Host, Spec: toSpecJSON(spec)}, &fj)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return errInterrupted
			}
			if declined := rp.declined(); declined != nil {
				return declined
			}
			var ie *ipc.Error
			if !errors.As(err, &ie) || ie.Code != ipc.CodeBind {
				return err // the connection failed: the other forwards would too
			}
			failed = append(failed, err)
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", safetext.Clean(err.Error()))
			continue
		}
		out.forwardChanged(fromForwardJSON(fj))
	}
	if len(failed) > 0 {
		return &bindError{fmt.Errorf("%d of %d forwards could not listen; see the errors above", len(failed), len(specs))}
	}
	if !wantJSON(cmd) {
		fmt.Fprintln(cmd.ErrOrStderr(), "The daemon keeps them; pfwd ls lists them and pfwd stop ends them.")
	}
	return nil
}

// interrupted is errInterrupted when ctx was interrupted, else err.
func interrupted(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return errInterrupted
	}
	return err
}

// fromForwardJSON is the engine forward a forwardJSON describes.
func fromForwardJSON(j forwardJSON) engine.Forward {
	kind, _ := kindOf(j.Kind)
	f := engine.Forward{ID: j.ID, Host: engine.HostID(j.Host), StartedAt: j.StartedAt, MappedFrom: j.MappedFrom,
		Spec: engine.ForwardSpec{Kind: kind, BindAddr: j.Bind, LocalPort: j.Local, RemoteHost: j.RemoteHost, RemotePort: j.Remote,
			Label: j.Label, Pinned: j.Pinned, Auto: j.Auto, Config: j.Config},
		Stats: forward.Stats{Conns: j.Conns, BytesIn: j.BytesIn, BytesOut: j.BytesOut}}
	for _, st := range []engine.ForwardState{engine.Starting, engine.Active, engine.Paused, engine.Failed, engine.Stopped} {
		if st.String() == j.State {
			f.State = st
		}
	}
	for _, p := range []engine.PauseReason{engine.PortGone, engine.Disconnected, engine.ServerPortBusy} {
		if p.String() == j.Pause {
			f.Pause = p
		}
	}
	for _, sc := range []forward.Scheme{forward.SchemeNone, forward.SchemeHTTP, forward.SchemeHTTPS} {
		if sc.String() == j.Scheme {
			f.Scheme = sc
		}
	}
	if j.Error != "" {
		f.Err = errors.New(j.Error)
	}
	return f
}

// lsFromDaemon prints the daemon's hosts, or host's when named. ok is false
// when no daemon runs, or it doesn't know host.
func lsFromDaemon(cmd *cobra.Command, host string) (bool, error) {
	c, err := ipc.Dial(cmd.Context(), ipc.DefaultPath())
	if errors.Is(err, ipc.ErrNotRunning) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer c.Close()
	var res struct {
		Hosts []hostJSON `json:"hosts"`
	}
	if err := c.Call(cmd.Context(), "list", hostParams{Host: host}, &res); err != nil {
		return false, err
	}
	if host != "" && len(res.Hosts) == 0 {
		return false, nil
	}
	return true, printHosts(cmd, res.Hosts)
}

func newStopCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "stop <id|host:port>...",
		Short: "Stop forwards in the daemon",
		Long: `pfwd stop ends forwards the daemon keeps: by ID, or by host and port (the
local port first, then the server one), as pfwd ls shows them. --all ends
every forward, or every forward of the host named after it.`,
		Example: "  pfwd stop devbox:5432\n  pfwd stop 3 4\n  pfwd stop --all devbox",
		Args: usageArgs(func(cmd *cobra.Command, args []string) error {
			switch {
			case all && len(args) > 1:
				return errors.New("--all takes at most one host")
			case !all && len(args) == 0:
				return errors.New("name a forward, like `pfwd stop devbox:5432`, or pass --all")
			}
			return nil
		}),
		ValidArgsFunction: completeForwards,
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopForwards(cmd, args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop every forward (of one host, when named)")
	return cmd
}

// stopForwards stops what args name in the daemon.
func stopForwards(cmd *cobra.Command, args []string, all bool) error {
	ctx := cmd.Context()
	c, err := ipc.Dial(ctx, ipc.DefaultPath())
	if errors.Is(err, ipc.ErrNotRunning) {
		return errors.New("no pfwd daemon is running; forwards in the TUI stop with x, and pfwd fwd's with Ctrl+C")
	}
	if err != nil {
		return err
	}
	defer c.Close()
	var calls []stopParams
	if all {
		p := stopParams{All: true}
		if len(args) == 1 {
			p.Host = args[0]
		}
		calls = append(calls, p)
	} else {
		var ids []string
		for _, a := range args {
			i := strings.LastIndexByte(a, ':')
			if i < 0 {
				ids = append(ids, a)
				continue
			}
			port, err := strconv.ParseUint(a[i+1:], 10, 16)
			if err != nil || port == 0 || i == 0 {
				return &usageError{fmt.Errorf("invalid forward %q: use an ID or host:port, as pfwd ls shows", a)}
			}
			calls = append(calls, stopParams{Host: a[:i], Port: uint16(port)})
		}
		if len(ids) > 0 {
			calls = append(calls, stopParams{IDs: ids})
		}
	}
	stopped := []string{}
	for _, p := range calls {
		var res struct {
			Stopped []string `json:"stopped"`
		}
		if err := c.Call(ctx, "stop", p, &res); err != nil {
			return err
		}
		stopped = append(stopped, res.Stopped...)
	}
	if wantJSON(cmd) {
		return writeJSON(cmd.OutOrStdout(), map[string][]string{"stopped": stopped})
	}
	if len(stopped) == 1 {
		fmt.Fprintln(cmd.OutOrStdout(), "Stopped 1 forward.")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Stopped %d forwards.\n", len(stopped))
	}
	return nil
}

// completeForwards offers the daemon's forwards as host:port.
func completeForwards(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, err := ipc.Dial(cmd.Context(), ipc.DefaultPath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer c.Close()
	var res struct {
		Hosts []hostJSON `json:"hosts"`
	}
	if err := c.Call(cmd.Context(), "list", hostParams{}, &res); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, h := range res.Hosts {
		for _, f := range h.Forwards {
			if name := h.Host + ":" + strconv.Itoa(int(f.Local)); strings.HasPrefix(name, toComplete) {
				out = append(out, name)
			}
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
