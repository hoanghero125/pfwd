package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/sshx"
	"github.com/hoanghero125/pfwd/internal/version"
)

// watchBuffer is how many events a watcher may fall behind before the
// daemon drops it.
const watchBuffer = 256

// daemonHandler serves the daemon's IPC calls with one engine.
type daemonHandler struct {
	eng     *engine.Engine
	d       *hostDialer
	log     *slog.Logger
	path    string
	started time.Time
	// stopping is closed when a client asks the daemon to shut down.
	stopping chan struct{}
	stopOnce sync.Once

	mu       sync.Mutex
	hosts    []engine.HostID
	sessions map[engine.HostID]engine.SessionChanged
	ports    map[engine.HostID]engine.PortsChanged
	auto     map[engine.HostID]bool
	// changed is closed and replaced whenever a session changes.
	changed  chan struct{}
	watchers map[chan ipc.Event]struct{}
	broker   promptBroker
}

func newDaemonHandler(eng *engine.Engine, d *hostDialer, path string, log *slog.Logger) *daemonHandler {
	return &daemonHandler{
		eng: eng, d: d, log: log, path: path, started: time.Now(),
		stopping: make(chan struct{}),
		sessions: make(map[engine.HostID]engine.SessionChanged),
		ports:    make(map[engine.HostID]engine.PortsChanged),
		auto:     make(map[engine.HostID]bool),
		changed:  make(chan struct{}),
		watchers: make(map[chan ipc.Event]struct{}),
		broker:   promptBroker{pending: make(map[string]pendingPrompt)},
	}
}

// run applies the engine's events until ctx ends.
func (h *daemonHandler) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-h.eng.Events():
			h.apply(ev)
		}
	}
}

// apply records an event and passes it to the watchers. Prompts get an ID
// and wait in the broker for a client's answer.
func (h *daemonHandler) apply(ev engine.Event) {
	h.mu.Lock()
	var out []ipc.Event
	switch ev := ev.(type) {
	case engine.SessionChanged:
		h.sessions[ev.Host] = ev
		close(h.changed)
		h.changed = make(chan struct{})
		if ev.State == engine.SessionFailed || ev.State == engine.Closed {
			for _, id := range h.broker.dropHost(ev.Host) {
				out = append(out, promptDone(id))
			}
		}
	case engine.PortsChanged:
		h.ports[ev.Host] = ev
	case engine.AutoChanged:
		h.auto[ev.Host] = ev.On
	case engine.HostKeyPrompt, engine.CredentialPrompt:
		if p, ok := h.broker.add(ev); ok {
			if data, err := json.Marshal(p); err == nil {
				out = append(out, ipc.Event{Event: "prompt", Data: data})
			}
		}
	}
	if e, ok := encodeEvent(ev); ok {
		out = append([]ipc.Event{e}, out...)
	}
	for _, e := range out {
		h.broadcastLocked(e)
	}
	h.mu.Unlock()
}

func promptDone(id string) ipc.Event {
	data, _ := json.Marshal(map[string]string{"id": id})
	return ipc.Event{Event: "prompt_done", Data: data}
}

// broadcastLocked sends e to every watcher. One that has fallen too far
// behind is dropped; its client sees the stream end.
func (h *daemonHandler) broadcastLocked(e ipc.Event) {
	for ch := range h.watchers {
		select {
		case ch <- e:
		default:
			delete(h.watchers, ch)
			close(ch)
		}
	}
}

// Watch sends what the daemon knows now (sessions, ports, forwards,
// waiting prompts), then every event, until ctx ends or send fails.
func (h *daemonHandler) Watch(ctx context.Context, send func(ipc.Event) error) error {
	ch := make(chan ipc.Event, watchBuffer)
	h.mu.Lock()
	var snapshot []ipc.Event
	for _, host := range h.hosts {
		if s, ok := h.sessions[host]; ok {
			if e, ok := encodeEvent(s); ok {
				snapshot = append(snapshot, e)
			}
		}
		if on, ok := h.auto[host]; ok {
			if e, ok := encodeEvent(engine.AutoChanged{Host: host, On: on}); ok {
				snapshot = append(snapshot, e)
			}
		}
		if p, ok := h.ports[host]; ok {
			if e, ok := encodeEvent(p); ok {
				snapshot = append(snapshot, e)
			}
		}
	}
	for _, p := range h.broker.list() {
		if data, err := json.Marshal(p); err == nil {
			snapshot = append(snapshot, ipc.Event{Event: "prompt", Data: data})
		}
	}
	h.watchers[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if _, ok := h.watchers[ch]; ok {
			delete(h.watchers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}()
	for _, f := range h.eng.Forwards() {
		if e, ok := encodeEvent(engine.ForwardChanged{Forward: f}); ok {
			snapshot = append(snapshot, e)
		}
	}
	for _, e := range snapshot {
		if err := send(e); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-ch:
			if !ok {
				return errors.New("the client fell behind")
			}
			if err := send(e); err != nil {
				return err
			}
		}
	}
}

// statusJSON is what pfwd daemon status prints.
type statusJSON struct {
	Running   bool             `json:"running"`
	PID       int              `json:"pid"`
	Version   string           `json:"version"`
	Socket    string           `json:"socket"`
	StartedAt time.Time        `json:"started_at"`
	Hosts     []hostStatusJSON `json:"hosts"`
	Forwards  int              `json:"forwards"`
}

type hostStatusJSON struct {
	Host  string `json:"host"`
	State string `json:"state"`
}

// Calls' parameters.
type (
	hostParams struct {
		Host string `json:"host"`
	}
	connectParams struct {
		Target string `json:"target"`
	}
	forwardParams struct {
		Host string   `json:"host"`
		Spec specJSON `json:"spec"`
	}
	stopParams struct {
		IDs  []string `json:"ids"`
		Host string   `json:"host"`
		Port uint16   `json:"port"`
		All  bool     `json:"all"`
	}
	autoParams struct {
		Host string `json:"host"`
		On   bool   `json:"on"`
	}
	pinParams struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
	}
)

func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return ipc.Errorf(ipc.CodeUsage, "bad parameters: %v", err)
	}
	return nil
}

// Handle serves one call.
func (h *daemonHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "status":
		return h.status(), nil
	case "connect":
		var p connectParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.connect(ctx, p.Target)
	case "forward":
		var p forwardParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		spec, ok := p.Spec.spec()
		if !ok {
			return nil, ipc.Errorf(ipc.CodeUsage, "unknown forward kind %q", p.Spec.Kind)
		}
		host := engine.HostID(p.Host)
		if err := h.waitConnected(ctx, host); err != nil {
			return nil, err
		}
		f, err := h.eng.ForwardOrNearby(host, spec)
		if err != nil {
			return nil, ipc.Errorf(ipc.CodeBind, "%s", err.Error())
		}
		return toForwardJSON(f), nil
	case "stop":
		var p stopParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return h.stop(p)
	case "list":
		var p hostParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{"hosts": h.list(engine.HostID(p.Host))}, nil
	case "set_auto":
		var p autoParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		h.eng.SetAuto(engine.HostID(p.Host), p.On)
		return map[string]bool{"ok": true}, nil
	case "set_pinned":
		var p pinParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if err := h.eng.SetPinned(p.ID, p.Pinned); err != nil {
			return nil, ipc.Errorf(ipc.CodeNotFound, "%s", err.Error())
		}
		return map[string]bool{"ok": true}, nil
	case "reconnect", "stop_retrying":
		var p hostParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if method == "reconnect" {
			h.eng.Reconnect(engine.HostID(p.Host))
		} else {
			h.eng.StopRetrying(engine.HostID(p.Host))
		}
		return map[string]bool{"ok": true}, nil
	case "answer":
		var a answerJSON
		if err := decode(params, &a); err != nil {
			return nil, err
		}
		h.mu.Lock()
		err := h.broker.answer(a)
		if err == nil {
			h.broadcastLocked(promptDone(a.ID))
		}
		h.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case "shutdown":
		h.stopOnce.Do(func() { close(h.stopping) })
		return map[string]bool{"stopping": true}, nil
	}
	return nil, ipc.Errorf(ipc.CodeUsage, "unknown method %q; is this pfwd older than the daemon?", method)
}

func (h *daemonHandler) status() statusJSON {
	s := statusJSON{Running: true, PID: os.Getpid(), Version: version.Get().Version, Socket: h.path, StartedAt: h.started, Hosts: []hostStatusJSON{}}
	h.mu.Lock()
	for _, host := range h.hosts {
		s.Hosts = append(s.Hosts, hostStatusJSON{Host: string(host), State: h.sessions[host].State.String()})
	}
	h.mu.Unlock()
	s.Forwards = len(h.eng.Forwards())
	return s
}

// connect resolves target, if it is new, and connects the engine to it.
func (h *daemonHandler) connect(ctx context.Context, target string) (any, error) {
	if _, _, err := sshx.SplitTarget(target); err != nil {
		return nil, ipc.Errorf(ipc.CodeUsage, "%s", err.Error())
	}
	host, err := h.d.add(ctx, target, false)
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeConnect, "%s", err.Error())
	}
	h.mu.Lock()
	if !slices.Contains(h.hosts, host) {
		h.hosts = append(h.hosts, host)
		h.sessions[host] = engine.SessionChanged{Host: host, State: engine.Connecting}
	}
	h.mu.Unlock()
	h.eng.Connect(host)
	return map[string]string{"host": string(host)}, nil
}

// waitConnected waits until host is connected, or reports why it can't
// be.
func (h *daemonHandler) waitConnected(ctx context.Context, host engine.HostID) error {
	for {
		h.mu.Lock()
		s, known := h.sessions[host]
		changed := h.changed
		h.mu.Unlock()
		switch {
		case !known:
			return ipc.Errorf(ipc.CodeNotFound, "the daemon isn't connected to %s; pfwd fwd connects it", host)
		case s.State == engine.Connected:
			return nil
		case s.State == engine.SessionFailed || s.State == engine.Closed:
			return sessionError(host, s.Err)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ipc.Errorf(ipc.CodeConnect, "%s is not connected yet: %v", host, ctx.Err())
		}
	}
}

// sessionError turns a failed session's error into an IPC error with the
// code its exit code needs.
func sessionError(host engine.HostID, err error) error {
	if err == nil {
		return ipc.Errorf(ipc.CodeConnect, "the connection to %s ended", host)
	}
	code := ipc.CodeConnect
	switch {
	case errors.Is(err, sshx.ErrHostKey):
		code = ipc.CodeHostKey
	case errors.Is(err, sshx.ErrAuth):
		code = ipc.CodeAuth
	}
	return ipc.Errorf(code, "%s", err.Error())
}

// stop stops forwards by ID, by host and port (the local port first, then
// the server one), or all of them (of one host, when named).
func (h *daemonHandler) stop(p stopParams) (any, error) {
	fws := h.eng.Forwards()
	var ids []string
	switch {
	case len(p.IDs) > 0:
		for _, id := range p.IDs {
			if !slices.ContainsFunc(fws, func(f engine.Forward) bool { return f.ID == id }) {
				return nil, ipc.Errorf(ipc.CodeNotFound, "no forward %s; pfwd ls lists them", id)
			}
		}
		ids = p.IDs
	case p.All:
		for _, f := range fws {
			if p.Host == "" || f.Host == engine.HostID(p.Host) {
				ids = append(ids, f.ID)
			}
		}
	default:
		for _, match := range []func(engine.Forward) bool{
			func(f engine.Forward) bool { return f.Spec.LocalPort == p.Port },
			func(f engine.Forward) bool { return f.Spec.RemotePort == p.Port },
		} {
			for _, f := range fws {
				if f.Host == engine.HostID(p.Host) && match(f) {
					ids = append(ids, f.ID)
				}
			}
			if len(ids) > 0 {
				break
			}
		}
		if len(ids) == 0 {
			return nil, ipc.Errorf(ipc.CodeNotFound, "no forward of %s:%d; pfwd ls lists them", p.Host, p.Port)
		}
	}
	for _, id := range ids {
		if err := h.eng.Stop(id); err != nil {
			return nil, ipc.Errorf(ipc.CodeNotFound, "%s", err.Error())
		}
	}
	if ids == nil {
		ids = []string{}
	}
	return map[string][]string{"stopped": ids}, nil
}

// list is pfwd ls for the daemon's hosts (or one of them).
func (h *daemonHandler) list(only engine.HostID) []hostJSON {
	fws := h.eng.Forwards()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []hostJSON{}
	for _, host := range h.hosts {
		if only != "" && host != only {
			continue
		}
		j := hostJSON{Host: string(host), State: h.sessions[host].State.String(), OS: h.ports[host].OS, Ports: []portJSON{}, Forwards: []forwardJSON{}}
		for _, p := range h.ports[host].Ports {
			j.Ports = append(j.Ports, toPortJSON(p))
		}
		for _, f := range fws {
			if f.Host == host {
				j.Forwards = append(j.Forwards, toForwardJSON(f))
			}
		}
		out = append(out, j)
	}
	return out
}

// pendingPrompt is a prompt waiting for a client's answer.
type pendingPrompt struct {
	key  *engine.HostKeyPrompt
	cred *engine.CredentialPrompt
	json promptJSON
}

// answerJSON answers a prompt: accept for a host key, secret (or cancel)
// for a credential.
type answerJSON struct {
	ID     string  `json:"id"`
	Accept bool    `json:"accept"`
	Secret *string `json:"secret"`
	Cancel bool    `json:"cancel"`
}

// promptBroker holds the prompts no client has answered yet. The first
// answer wins. The handler's mutex guards it.
type promptBroker struct {
	next    int
	pending map[string]pendingPrompt
}

// add gives a prompt an ID and keeps it.
func (b *promptBroker) add(ev engine.Event) (promptJSON, bool) {
	b.next++
	id := strconv.Itoa(b.next)
	var p pendingPrompt
	switch ev := ev.(type) {
	case engine.HostKeyPrompt:
		q := ev.Question
		p = pendingPrompt{key: &ev, json: promptJSON{ID: id, Session: string(ev.Host), Host: cmp.Or(q.Host, string(ev.Host)), Kind: "host_key", KeyType: q.KeyType, Fingerprint: q.Fingerprint, Saves: q.Saves}}
	case engine.CredentialPrompt:
		q := ev.Question
		kind := map[sshx.CredentialKind]string{sshx.Passphrase: "passphrase", sshx.Password: "password", sshx.Code: "code"}[q.Kind]
		p = pendingPrompt{cred: &ev, json: promptJSON{ID: id, Session: string(ev.Host), Host: q.Host, Kind: kind, Prompt: q.Prompt, Instruction: q.Instruction, Echo: q.Echo, Retry: q.Retry}}
	default:
		return promptJSON{}, false
	}
	b.pending[id] = p
	return p.json, true
}

// answer answers the prompt a.ID and forgets it.
func (b *promptBroker) answer(a answerJSON) error {
	p, ok := b.pending[a.ID]
	if !ok {
		return ipc.Errorf(ipc.CodeNotFound, "no prompt %s waits for an answer; another client may have answered it", a.ID)
	}
	delete(b.pending, a.ID)
	switch {
	case p.key != nil:
		p.key.Answer(a.Accept)
	case a.Cancel || a.Secret == nil:
		p.cred.Cancel()
	default:
		p.cred.Answer([]byte(*a.Secret))
	}
	return nil
}

// dropHost forgets host's prompts, whose session ended, and returns their
// IDs.
func (b *promptBroker) dropHost(host engine.HostID) []string {
	var ids []string
	for id, p := range b.pending {
		if (p.key != nil && p.key.Host == host) || (p.cred != nil && p.cred.Host == host) {
			ids = append(ids, id)
			delete(b.pending, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// list returns the waiting prompts, oldest first.
func (b *promptBroker) list() []promptJSON {
	out := make([]promptJSON, 0, len(b.pending))
	for _, p := range b.pending {
		out = append(out, p.json)
	}
	slices.SortFunc(out, func(a, b promptJSON) int {
		ai, _ := strconv.Atoi(a.ID)
		bi, _ := strconv.Atoi(b.ID)
		return ai - bi
	})
	return out
}
