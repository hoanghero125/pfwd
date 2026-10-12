package cli

import (
	"encoding/json"
	"time"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/ipc"
)

// sessionJSON is a session_changed event.
type sessionJSON struct {
	Host    string     `json:"host"`
	State   string     `json:"state"`
	Error   string     `json:"error"`
	RetryAt *time.Time `json:"retry_at"`
	Attempt int        `json:"attempt"`
}

// portsJSON is a ports_changed event.
type portsJSON struct {
	Host     string     `json:"host"`
	Strategy string     `json:"strategy"`
	OS       string     `json:"os"`
	Ports    []portJSON `json:"ports"`
	Added    []uint16   `json:"added"`
	Removed  []uint16   `json:"removed"`
	Error    string     `json:"error"`
}

// promptJSON is a prompt event: a question a client must answer.
type promptJSON struct {
	ID          string `json:"id"`
	Host        string `json:"host"`
	Kind        string `json:"kind"` // host_key, passphrase, password or code
	KeyType     string `json:"key_type,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Saves       bool   `json:"saves,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Echo        bool   `json:"echo,omitempty"`
	Retry       bool   `json:"retry,omitempty"`
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// encodeEvent turns an engine event into a watch event. Prompts are
// encoded by the prompt broker, which gives them IDs.
func encodeEvent(ev engine.Event) (ipc.Event, bool) {
	var name string
	var data any
	switch ev := ev.(type) {
	case engine.SessionChanged:
		s := sessionJSON{Host: string(ev.Host), State: ev.State.String(), Error: errText(ev.Err), Attempt: ev.Attempt}
		if !ev.RetryAt.IsZero() {
			s.RetryAt = &ev.RetryAt
		}
		name, data = "session_changed", s
	case engine.PortsChanged:
		p := portsJSON{Host: string(ev.Host), Strategy: ev.Strategy.String(), OS: ev.OS, Added: ev.Added, Removed: ev.Removed, Error: errText(ev.Err)}
		for _, port := range ev.Ports {
			p.Ports = append(p.Ports, toPortJSON(port))
		}
		name, data = "ports_changed", p
	case engine.ForwardChanged:
		name, data = "forward_changed", toForwardJSON(ev.Forward)
	case engine.AutoChanged:
		name, data = "auto_changed", map[string]any{"host": string(ev.Host), "on": ev.On}
	case engine.AutoForwardFailed:
		name, data = "auto_forward_failed", map[string]any{"host": string(ev.Host), "port": ev.Port, "error": errText(ev.Err)}
	case engine.RestoreFailed:
		name, data = "restore_failed", map[string]any{"host": string(ev.Host), "spec": toSpecJSON(ev.Spec), "error": errText(ev.Err)}
	default:
		return ipc.Event{}, false
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ipc.Event{}, false
	}
	return ipc.Event{Event: name, Data: raw}, true
}

// specJSON is a forward spec in IPC calls.
type specJSON struct {
	Kind       string `json:"kind"`
	Bind       string `json:"bind"`
	Local      uint16 `json:"local"`
	RemoteHost string `json:"remote_host"`
	Remote     uint16 `json:"remote"`
	Label      string `json:"label"`
	Pinned     bool   `json:"pinned"`
}

func toSpecJSON(s engine.ForwardSpec) specJSON {
	return specJSON{Kind: s.Kind.String(), Bind: s.BindAddr, Local: s.LocalPort, RemoteHost: s.RemoteHost, Remote: s.RemotePort, Label: s.Label, Pinned: s.Pinned}
}

// kindOf parses a kind name; ok is false for an unknown one.
func kindOf(name string) (engine.ForwardKind, bool) {
	for _, k := range []engine.ForwardKind{engine.Local, engine.Remote, engine.Dynamic} {
		if k.String() == name {
			return k, true
		}
	}
	return 0, false
}

func (s specJSON) spec() (engine.ForwardSpec, bool) {
	kind, ok := kindOf(s.Kind)
	if !ok {
		return engine.ForwardSpec{}, false
	}
	return engine.ForwardSpec{Kind: kind, BindAddr: s.Bind, LocalPort: s.Local, RemoteHost: s.RemoteHost, RemotePort: s.Remote, Label: s.Label, Pinned: s.Pinned}, true
}
