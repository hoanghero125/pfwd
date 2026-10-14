package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/tui"
)

// attachEngine is the daemon's engine as the TUI sees it: calls go over
// IPC, and the watch stream turns back into engine events. Answers to
// prompts go back to the daemon.
type attachEngine struct {
	ctx    context.Context
	path   string
	c      *ipc.Client
	events chan engine.Event
	// wg owns the goroutines that wait for the TUI's answers to prompts.
	wg sync.WaitGroup
}

// newAttachEngine connects to the daemon at path.
func newAttachEngine(ctx context.Context, path string) (*attachEngine, error) {
	c, err := ipc.Dial(ctx, path)
	if err != nil {
		return nil, err
	}
	return &attachEngine{ctx: ctx, path: path, c: c, events: make(chan engine.Event, 256)}, nil
}

// watch turns the daemon's events into engine events until ctx ends or the
// daemon goes; then it closes Events, which ends the TUI.
func (a *attachEngine) watch(ctx context.Context) error {
	defer close(a.events)
	return ipc.Watch(ctx, a.path, func(ev ipc.Event) {
		if e, ok := a.decode(ev); ok {
			select {
			case a.events <- e:
			case <-ctx.Done():
			}
		}
	})
}

// close waits for the prompt answers in flight and closes the connection.
func (a *attachEngine) close() {
	a.wg.Wait()
	_ = a.c.Close()
}

// decode turns a watch event into an engine event.
func (a *attachEngine) decode(ev ipc.Event) (engine.Event, bool) {
	switch ev.Event {
	case "session_changed":
		var s sessionJSON
		if json.Unmarshal(ev.Data, &s) != nil {
			return nil, false
		}
		e := engine.SessionChanged{Host: engine.HostID(s.Host), Attempt: s.Attempt, Err: errOrNil(s.Error)}
		for _, st := range []engine.SessionState{engine.Connecting, engine.Connected, engine.SessionFailed, engine.Closed, engine.Reconnecting} {
			if st.String() == s.State {
				e.State = st
			}
		}
		if s.RetryAt != nil {
			e.RetryAt = *s.RetryAt
		}
		return e, true
	case "ports_changed":
		var p portsJSON
		if json.Unmarshal(ev.Data, &p) != nil {
			return nil, false
		}
		e := engine.PortsChanged{Host: engine.HostID(p.Host), OS: p.OS, Added: p.Added, Removed: p.Removed, Err: errOrNil(p.Error)}
		for _, st := range []detect.Strategy{detect.None, detect.SS, detect.Proc, detect.Netstat, detect.Lsof, detect.Sockstat, detect.Windows} {
			if st.String() == p.Strategy {
				e.Strategy = st
			}
		}
		for _, port := range p.Ports {
			e.Ports = append(e.Ports, engine.Port{
				RemotePort: detect.RemotePort{Port: port.Port, Addrs: port.Addrs, PID: port.PID, Process: port.Process},
				Label:      port.Label, Ignored: port.Ignored,
			})
		}
		return e, true
	case "forward_changed":
		var f forwardJSON
		if json.Unmarshal(ev.Data, &f) != nil {
			return nil, false
		}
		return engine.ForwardChanged{Forward: fromForwardJSON(f)}, true
	case "auto_changed":
		var v struct {
			Host string `json:"host"`
			On   bool   `json:"on"`
		}
		if json.Unmarshal(ev.Data, &v) != nil {
			return nil, false
		}
		return engine.AutoChanged{Host: engine.HostID(v.Host), On: v.On}, true
	case "auto_forward_failed":
		var v struct {
			Host  string `json:"host"`
			Port  uint16 `json:"port"`
			Error string `json:"error"`
		}
		if json.Unmarshal(ev.Data, &v) != nil {
			return nil, false
		}
		return engine.AutoForwardFailed{Host: engine.HostID(v.Host), Port: v.Port, Err: errOrNil(v.Error)}, true
	case "restore_failed":
		var v struct {
			Host  string   `json:"host"`
			Spec  specJSON `json:"spec"`
			Error string   `json:"error"`
		}
		if json.Unmarshal(ev.Data, &v) != nil {
			return nil, false
		}
		spec, _ := v.Spec.spec()
		if v.Spec.Kind == "" {
			spec = engine.ForwardSpec{}
		}
		return engine.RestoreFailed{Host: engine.HostID(v.Host), Spec: spec, Err: errOrNil(v.Error)}, true
	case "prompt":
		var p promptJSON
		if json.Unmarshal(ev.Data, &p) != nil {
			return nil, false
		}
		return a.prompt(p), true
	}
	return nil, false
}

// prompt makes an engine prompt whose answer goes to the daemon.
func (a *attachEngine) prompt(p promptJSON) engine.Event {
	host := engine.HostID(p.Session)
	if p.Kind == "host_key" {
		reply := make(chan bool, 1)
		a.answerLater(func() (answerJSON, bool) {
			select {
			case ok := <-reply:
				return answerJSON{ID: p.ID, Accept: ok}, true
			case <-a.ctx.Done():
				return answerJSON{}, false
			}
		})
		return engine.NewHostKeyPrompt(host, sshx.HostKeyQuestion{Host: p.Host, KeyType: p.KeyType, Fingerprint: p.Fingerprint, Saves: p.Saves}, reply)
	}
	kind := sshx.Code
	switch p.Kind {
	case "passphrase":
		kind = sshx.Passphrase
	case "password":
		kind = sshx.Password
	}
	reply := make(chan engine.CredentialAnswer, 1)
	a.answerLater(func() (answerJSON, bool) {
		select {
		case ans := <-reply:
			if ans.Canceled {
				return answerJSON{ID: p.ID, Cancel: true}, true
			}
			secret := string(ans.Secret)
			clear(ans.Secret)
			return answerJSON{ID: p.ID, Secret: &secret}, true
		case <-a.ctx.Done():
			return answerJSON{}, false
		}
	})
	return engine.NewCredentialPrompt(host, sshx.CredentialQuestion{Host: p.Host, Kind: kind, Prompt: p.Prompt,
		Instruction: p.Instruction, Echo: p.Echo, Retry: p.Retry}, reply)
}

// answerLater waits for the TUI's answer and sends it to the daemon.
func (a *attachEngine) answerLater(wait func() (answerJSON, bool)) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ans, ok := wait()
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()
		// Another client may have answered first; that's fine.
		_ = a.c.Call(ctx, "answer", ans, nil)
	}()
}

func errOrNil(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}

// The tui.Engine methods.

func (a *attachEngine) Events() <-chan engine.Event { return a.events }

func (a *attachEngine) Forward(host engine.HostID, spec engine.ForwardSpec) (engine.Forward, error) {
	var fj forwardJSON
	if err := a.c.Call(a.ctx, "forward", forwardParams{Host: string(host), Spec: toSpecJSON(spec)}, &fj); err != nil {
		return engine.Forward{}, err
	}
	return fromForwardJSON(fj), nil
}

func (a *attachEngine) Stop(id string) error {
	return a.c.Call(a.ctx, "stop", stopParams{IDs: []string{id}}, nil)
}

func (a *attachEngine) Forwards() []engine.Forward {
	var res struct {
		Hosts []hostJSON `json:"hosts"`
	}
	if err := a.c.Call(a.ctx, "list", hostParams{}, &res); err != nil {
		return nil
	}
	var out []engine.Forward
	for _, h := range res.Hosts {
		for _, f := range h.Forwards {
			out = append(out, fromForwardJSON(f))
		}
	}
	return out
}

func (a *attachEngine) SetAuto(host engine.HostID, on bool) {
	_ = a.c.Call(a.ctx, "set_auto", autoParams{Host: string(host), On: on}, nil)
}

func (a *attachEngine) Reconnect(host engine.HostID) {
	_ = a.c.Call(a.ctx, "reconnect", hostParams{Host: string(host)}, nil)
}

func (a *attachEngine) StopRetrying(host engine.HostID) {
	_ = a.c.Call(a.ctx, "stop_retrying", hostParams{Host: string(host)}, nil)
}

func (a *attachEngine) SetPinned(id string, pinned bool) error {
	return a.c.Call(a.ctx, "set_pinned", pinParams{ID: id, Pinned: pinned}, nil)
}

// connect asks the daemon to connect a host picked with +.
func (a *attachEngine) connect(ctx context.Context, name string) (engine.HostID, error) {
	var res struct {
		Host string `json:"host"`
	}
	if err := a.c.Call(ctx, "connect", connectParams{Target: name}, &res); err != nil {
		return "", err
	}
	return engine.HostID(res.Host), nil
}

func newAttachCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "attach",
		Short: "Open the TUI on the running daemon",
		Long: `pfwd attach opens the terminal UI on the daemon's hosts and forwards. q
detaches: the daemon keeps the forwards; pfwd daemon stop ends them.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error { return runAttach(cmd, f) },
	}
}

// runAttach runs the TUI against the daemon until the user detaches or the
// daemon stops.
func runAttach(cmd *cobra.Command, f *rootFlags) error {
	c, err := loadCommon(cmd, f)
	if err != nil {
		return err
	}
	in, out, ok := terminal(cmd.InOrStdin(), cmd.OutOrStdout())
	if !ok {
		return errors.New("pfwd attach needs an interactive terminal; in scripts, use pfwd ls and pfwd stop")
	}
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	path := ipc.DefaultPath()
	eng, err := newAttachEngine(ctx, path)
	if err != nil {
		return err
	}
	defer eng.close()
	warnOtherRelease(ctx, cmd, eng.c)
	var st statusJSON
	if err := eng.c.Call(ctx, "status", nil, &st); err != nil {
		return err
	}
	if len(st.Hosts) == 0 {
		return errors.New("the daemon serves no host yet; give it one with `pfwd fwd --detach <host> <port>`")
	}
	var hosts []engine.HostID
	for _, h := range st.Hosts {
		hosts = append(hosts, engine.HostID(h.Host))
	}

	watched := make(chan error, 1)
	go func() { watched <- eng.watch(ctx) }()

	topts := tuiOptions(c.cfg, os.Getenv, runtime.GOOS)
	topts.Detach, topts.SkipQuitConfirm = true, true
	topts.Hosts = func(context.Context) ([]tui.HostChoice, error) {
		rows, err := listHosts(f.sshConfig)
		choices := make([]tui.HostChoice, 0, len(rows))
		for _, r := range rows {
			choices = append(choices, tui.HostChoice{Name: r.Host, LastUsed: r.LastUsed})
		}
		return choices, err
	}
	topts.Connect = eng.connect
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)}
	if noColor(os.Getenv("NO_COLOR"), f.noColor) {
		opts = append(opts, tea.WithColorProfile(colorprofile.Ascii))
	}
	_, runErr := tea.NewProgram(tui.NewHosts(eng, hosts, topts), opts...).Run()
	cancel()
	watchErr := <-watched
	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		return fmt.Errorf("run the terminal UI: %w", runErr)
	}
	if watchErr != nil && !errors.Is(watchErr, context.Canceled) {
		return fmt.Errorf("lost the daemon: %w", watchErr)
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "Detached; the daemon keeps the forwards. pfwd daemon stop ends them.")
	return nil
}
