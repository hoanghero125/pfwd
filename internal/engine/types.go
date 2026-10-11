package engine

import (
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
	Ports    []detect.RemotePort
	Err      error
}

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

// Answer replies to the prompt. Calls after the first are ignored.
func (p HostKeyPrompt) Answer(ok bool) {
	select {
	case p.reply <- ok:
	default:
	}
}

func (SessionChanged) event() {}
func (PortsChanged) event()   {}
func (ForwardChanged) event() {}
func (HostKeyPrompt) event()  {}
