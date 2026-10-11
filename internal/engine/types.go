package engine

import (
	"context"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// HostID names a host: the alias from the ssh config, or user@host.
type HostID string

// ForwardKind is the direction of a forward.
type ForwardKind int

// Forward kinds, matching ssh -L, -R and -D.
const (
	Local ForwardKind = iota
	Remote
	Dynamic
)

// String returns the kind as ssh's flag name.
func (k ForwardKind) String() string {
	switch k {
	case Remote:
		return "remote"
	case Dynamic:
		return "dynamic"
	default:
		return "local"
	}
}

// ForwardSpec describes a forward. Zero values get defaults: BindAddr
// 127.0.0.1, LocalPort = RemotePort, RemoteHost 127.0.0.1.
type ForwardSpec struct {
	Kind       ForwardKind
	BindAddr   string
	LocalPort  uint16
	RemoteHost string
	RemotePort uint16
	// Label names the service; empty takes the detected port's label.
	Label string
	// Auto marks a forward the engine created on its own.
	Auto bool
}

// ForwardState is where a forward is in its lifecycle.
type ForwardState int

// Forward states. Paused arrives with reconnects in v0.3.0.
const (
	Starting ForwardState = iota
	Active
	Paused
	Failed
	Stopped
)

// String returns the state in lowercase.
func (s ForwardState) String() string {
	switch s {
	case Active:
		return "active"
	case Paused:
		return "paused"
	case Failed:
		return "failed"
	case Stopped:
		return "stopped"
	default:
		return "starting"
	}
}

// Forward is a snapshot of one forward.
type Forward struct {
	ID        string
	Host      HostID
	Spec      ForwardSpec
	State     ForwardState
	Err       error
	Stats     forward.Stats
	StartedAt time.Time
	// Scheme is what the HTTP probe found; SchemeUnknown until it ran.
	Scheme forward.Scheme
}

// SessionState is where a host's connection is.
type SessionState int

// Session states.
const (
	Connecting SessionState = iota
	Connected
	SessionFailed
	Closed
)

// String returns the state in lowercase.
func (s SessionState) String() string {
	switch s {
	case Connected:
		return "connected"
	case SessionFailed:
		return "failed"
	case Closed:
		return "closed"
	default:
		return "connecting"
	}
}

// Event is something the engine reports: SessionChanged, PortsChanged,
// ForwardChanged or HostKeyPrompt.
type Event interface{ event() }

// SessionChanged reports a host's connection state. Err is set when the
// state is SessionFailed.
type SessionChanged struct {
	Host  HostID
	State SessionState
	Err   error
}

// PortsChanged reports the ports listening on a host. With Strategy None
// detection is unavailable; Err then says why, if it failed.
type PortsChanged struct {
	Host     HostID
	Strategy detect.Strategy
	OS       string
	Ports    []Port
	// Added are ports that just became stable and Removed are stable
	// ports that went away (see detect.Tracker).
	Added, Removed []uint16
	Err            error
}

// Port is a detected port with what the engine knows about it.
type Port struct {
	detect.RemotePort
	// Label names the service: the user's label, else a built-in one.
	Label string
	// Ignored ports are hidden unless the user shows them, and never
	// auto-forwarded.
	Ignored bool
}

// AutoChanged reports whether auto-forward is on for a host. It is sent
// when a session starts and whenever SetAuto changes it.
type AutoChanged struct {
	Host HostID
	On   bool
}

// AutoForwardFailed reports a new port that could not be auto-forwarded.
// The port is declined: pfwd won't try it again on its own this run.
type AutoForwardFailed struct {
	Host HostID
	Port uint16
	Err  error
}

// HostSettings are a host's auto-forward settings.
type HostSettings struct {
	// Auto turns auto-forward on when the session starts.
	Auto bool
	// Filter holds the ignore lists and auto_ports.
	Filter detect.Filter
}

// DefaultSettings is auto-forward on, with the default ignore lists.
func DefaultSettings() HostSettings {
	return HostSettings{Auto: true, Filter: detect.DefaultFilter()}
}

// Notifier shows a desktop notification.
type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// What an auto-forward does when its local port is taken.
const (
	// ConflictNext (and ConflictAsk, since nobody can be asked) uses the
	// free port Listen suggests.
	ConflictNext = "next"
	ConflictAsk  = "ask"
	// ConflictRandom uses any free port.
	ConflictRandom = "random"
	// ConflictFail gives up on that port.
	ConflictFail = "fail"
)

// ForwardChanged reports a forward that was created or changed state. A
// forward in state Stopped has been removed.
type ForwardChanged struct {
	Forward Forward
}

// HostKeyPrompt asks the user to trust an unknown host key. Exactly one
// Answer is used; the connection waits for it.
type HostKeyPrompt struct {
	Host     HostID
	Question sshx.HostKeyQuestion
	reply    chan<- bool
}

// NewHostKeyPrompt returns a prompt whose answer is sent on reply, which
// needs room for one value. Front ends use it in tests.
func NewHostKeyPrompt(host HostID, q sshx.HostKeyQuestion, reply chan<- bool) HostKeyPrompt {
	return HostKeyPrompt{Host: host, Question: q, reply: reply}
}

// Answer replies to the prompt. Calls after the first are ignored.
func (p HostKeyPrompt) Answer(ok bool) {
	select {
	case p.reply <- ok:
	default:
	}
}

func (SessionChanged) event()    {}
func (PortsChanged) event()      {}
func (ForwardChanged) event()    {}
func (HostKeyPrompt) event()     {}
func (AutoChanged) event()       {}
func (AutoForwardFailed) event() {}
