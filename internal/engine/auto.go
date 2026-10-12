package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/version"
)

// hostState is what the engine keeps per host across sessions.
type hostState struct {
	settings HostSettings
	auto     bool
	tracker  *detect.Tracker
	// ports is the latest snapshot, by port.
	ports map[uint16]detect.RemotePort
	// declined ports are never auto-forwarded again this run: the user
	// stopped them, or auto-forwarding them failed.
	declined map[uint16]bool
	// restored is set once the host's config and remembered forwards were
	// created this run; loaded once state.json was read for it, which must
	// happen before pfwd writes the host's entry.
	restored, loaded bool
}

// errAlreadyForwarded tells autoForward that a forward appeared meanwhile.
var errAlreadyForwarded = errors.New("already forwarded")

// notifyTimeout bounds one desktop notification.
const notifyTimeout = 5 * time.Second

// hostLocked returns the host's state, creating it; e.mu must be held.
func (e *Engine) hostLocked(host HostID) *hostState {
	hs, ok := e.hosts[host]
	if !ok {
		settings := DefaultSettings()
		if e.o.Settings != nil {
			settings = e.o.Settings(host)
		}
		hs = &hostState{
			settings: settings,
			auto:     settings.Auto,
			tracker:  detect.NewTracker(e.o.Debounce),
			ports:    map[uint16]detect.RemotePort{},
			declined: map[uint16]bool{},
		}
		e.hosts[host] = hs
	}
	return hs
}

// SetAuto turns auto-forward on or off for a host. Turning it on doesn't
// forward the ports already listed; only ports that appear later.
func (e *Engine) SetAuto(host HostID, on bool) {
	e.mu.Lock()
	hs := e.hostLocked(host)
	changed := hs.auto != on
	hs.auto = on
	e.mu.Unlock()
	if changed {
		e.log.Info("auto-forward", "host", host, "on", on)
		e.emit(AutoChanged{Host: host, On: on})
	}
}

// label is the user's label for the port, else a built-in one.
func (e *Engine) label(p detect.RemotePort) string {
	if l, ok := e.o.Labels[p.Port]; ok {
		return l
	}
	return detect.Label(p.Port, p.Process)
}

// portsUpdated handles one detection update: it records the snapshot,
// reports it, and auto-forwards ports that just became stable.
func (e *Engine) portsUpdated(host HostID, hs *hostState, u detect.Update) {
	ports := make([]Port, len(u.Ports))
	e.mu.Lock()
	clear(hs.ports)
	for i, p := range u.Ports {
		hs.ports[p.Port] = p
		ports[i] = Port{RemotePort: p, Label: e.label(p), Ignored: hs.settings.Filter.Ignored(p)}
	}
	e.mu.Unlock()
	added := make([]uint16, len(u.Added))
	for i, p := range u.Added {
		added[i] = p.Port
	}
	e.emit(PortsChanged{Host: host, Strategy: u.Strategy, OS: u.OS, Ports: ports, Added: added, Removed: u.Removed})
	if u.Strategy != detect.None {
		e.applyPorts(host, hs)
	}
	for _, p := range u.Added {
		e.autoForward(host, hs, p)
	}
}

// hasForwardLocked reports whether host already forwards remotePort, in
// any state; e.mu must be held.
func (e *Engine) hasForwardLocked(host HostID, remotePort uint16) bool {
	for _, f := range e.forwards {
		if f.Host == host && f.Spec.Kind == Local && f.Spec.RemotePort == remotePort {
			return true
		}
	}
	return false
}

// sameForward returns host's forward of spec's remote port on the same
// local address, for a manual spec; nil otherwise.
func (e *Engine) sameForward(host HostID, spec ForwardSpec) *fwd {
	if spec.Auto {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range e.forwards {
		s := f.Spec
		if f.Host == host && s.Kind == spec.Kind && s.RemotePort == spec.RemotePort && s.LocalPort == spec.LocalPort && s.BindAddr == spec.BindAddr {
			return f
		}
	}
	return nil
}

// autoForward forwards a new port unless auto is off, the port is
// declined or filtered, or it is already forwarded. A taken local port
// follows OnConflict; any failure declines the port for this run, so a
// flapping port can't loop.
func (e *Engine) autoForward(host HostID, hs *hostState, p detect.RemotePort) {
	e.mu.Lock()
	skip := !hs.auto || hs.declined[p.Port] || !hs.settings.Filter.AutoForward(p) || e.hasForwardLocked(host, p.Port)
	e.mu.Unlock()
	if skip {
		return
	}
	spec := ForwardSpec{RemotePort: p.Port, LocalPort: p.Port, RemoteHost: detect.Target(p.Addrs), Label: e.label(p), Auto: true}
	f, err := e.forwardFollowingConflict(host, spec)
	switch {
	case errors.Is(err, errAlreadyForwarded):
		return
	case err != nil:
		e.mu.Lock()
		if e.hasForwardLocked(host, p.Port) {
			// A manual forward of the port won the race for the local port.
			e.mu.Unlock()
			return
		}
		hs.declined[p.Port] = true
		e.mu.Unlock()
		e.log.Info("auto-forward failed", "host", host, "port", p.Port, "err", err)
		e.emit(AutoForwardFailed{Host: host, Port: p.Port, Err: err})
		return
	}
	e.notify(host, p, f)
}

// goOwned runs fn in a goroutine the engine waits for, unless Close has
// started.
func (e *Engine) goOwned(fn func()) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		fn()
	}()
}

// maxNotifyText caps text from the server in notifications.
const maxNotifyText = 40

// notify tells the desktop about an auto-forward. Process names come from
// the server, so they are cleaned and cut short.
func (e *Engine) notify(host HostID, p detect.RemotePort, f Forward) {
	if e.o.Notifier == nil {
		return
	}
	what := f.Spec.Label
	if what == "" {
		what = p.Process
	}
	what = safetext.Clean(what)
	if r := []rune(what); len(r) > maxNotifyText {
		what = string(r[:maxNotifyText-1]) + "…"
	}
	title := fmt.Sprintf("Forwarded :%d from %s", p.Port, safetext.Clean(string(host)))
	body := fmt.Sprintf("localhost:%d now reaches port %d on %s", f.Spec.LocalPort, p.Port, safetext.Clean(string(host)))
	if what != "" {
		body = what + ": " + body
	}
	n := e.o.Notifier
	e.goOwned(func() {
		ctx, cancel := context.WithTimeout(e.ctx, notifyTimeout)
		defer cancel()
		if err := n.Notify(ctx, title, body); err != nil {
			e.log.Debug("notification failed", "err", err)
		}
	})
}

// startProbe checks whether a new forward speaks HTTP or HTTPS, unless
// probing is off or the port is a database.
func (e *Engine) startProbe(ctx context.Context, f *fwd, conn Conn, target string) {
	if !e.o.HTTPProbe {
		return
	}
	e.mu.Lock()
	process := ""
	if hs, ok := e.hosts[f.Host]; ok {
		process = hs.ports[f.Spec.RemotePort].Process
	}
	e.mu.Unlock()
	if detect.SkipHTTPProbe(f.Spec.RemotePort, process) {
		return
	}
	host := net.JoinHostPort("localhost", itoa(f.Spec.LocalPort))
	e.goOwned(func() {
		scheme := forward.Probe(ctx, conn, target, forward.ProbeOptions{
			Host: host, UserAgent: "pfwd/" + version.Get().Version, Timeout: time.Second,
		})
		if scheme == forward.SchemeUnknown {
			return
		}
		e.mu.Lock()
		current := e.forwards[f.ID] == f
		if current {
			f.Scheme = scheme
		}
		e.mu.Unlock()
		if current {
			e.emit(ForwardChanged{Forward: e.snapshot(f)})
		}
	})
}
