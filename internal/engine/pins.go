package engine

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/state"
)

// SetPinned pins or unpins a forward. Pinned forwards are remembered when
// pfwd quits and restored on the next connection, and never expire.
// Forwards from config.toml are always pinned.
func (e *Engine) SetPinned(id string, pinned bool) error {
	e.mu.Lock()
	f, ok := e.forwards[id]
	switch {
	case !ok:
		e.mu.Unlock()
		return fmt.Errorf("no forward with id %q", id)
	case f.Spec.Config:
		e.mu.Unlock()
		return fmt.Errorf("the forward of port %d comes from config.toml (hosts.%s.forwards); edit the file to change it", f.Spec.RemotePort, f.Host)
	}
	changed := f.Spec.Pinned != pinned
	f.Spec.Pinned = pinned
	e.mu.Unlock()
	if changed {
		e.log.Info("forward pinned", "id", id, "pinned", pinned)
		e.emit(ForwardChanged{Forward: e.snapshot(f)})
		e.persist(f.Host)
	}
	return nil
}

// localTarget reports whether a forward's remote host is the server itself,
// so the detected ports say whether its target listens.
func localTarget(host string) bool {
	switch host {
	case "", "127.0.0.1", "::1", "localhost", "0.0.0.0", "::":
		return true
	}
	return false
}

// applyPorts pauses host's forwards whose port went away and resumes those
// whose port is back. It runs after each changed snapshot, while detection
// works.
func (e *Engine) applyPorts(host HostID, hs *hostState) {
	now := e.o.Now()
	e.mu.Lock()
	var changed []*fwd
	for _, f := range e.forwards {
		if f.Host != host || f.Spec.Kind != Local || !localTarget(f.Spec.RemoteHost) {
			continue
		}
		_, present := hs.ports[f.Spec.RemotePort]
		switch {
		case f.State == Active && !present:
			f.State, f.Pause, f.PausedAt = Paused, PortGone, now
			changed = append(changed, f)
		case f.State == Paused && f.Pause == PortGone && present:
			f.State, f.Pause, f.PausedAt, f.Err = Active, NotPaused, time.Time{}, nil
			changed = append(changed, f)
		}
	}
	e.mu.Unlock()
	for _, f := range changed {
		snap := e.snapshot(f)
		e.log.Info("forward changed by detection", "id", snap.ID, "state", snap.State, "reason", snap.Pause)
		e.emit(ForwardChanged{Forward: snap})
	}
	e.expireForwards(host)
}

// expireForwards removes host's auto-forwards that have been paused for a
// gone port longer than AutoExpire, unless pinned or from config. It runs
// after snapshots and on a timer, since a quiet server sends no changes.
func (e *Engine) expireForwards(host HostID) {
	now := e.o.Now()
	e.mu.Lock()
	var expired []*fwd
	for _, f := range e.forwards {
		if f.Host == host && f.State == Paused && f.Pause == PortGone && f.Spec.Auto && !f.Spec.Pinned && !f.Spec.Config && now.Sub(f.PausedAt) >= e.o.AutoExpire {
			delete(e.forwards, f.ID)
			expired = append(expired, f)
		}
	}
	e.mu.Unlock()
	for _, f := range expired {
		f.cancel()
		<-f.done
		e.mu.Lock()
		f.State = Stopped
		e.mu.Unlock()
		e.log.Info("auto-forward expired", "id", f.ID, "port", f.Spec.RemotePort)
		e.emit(ForwardChanged{Forward: e.snapshot(f)})
	}
	if len(expired) > 0 {
		e.persist(host)
	}
}

// forwardFollowingConflict creates a forward, and when its local port is
// taken tries again as OnConflict says: a free port nearby (next, and ask
// since nobody can be asked), any free port (random), or not (fail).
func (e *Engine) forwardFollowingConflict(host HostID, spec ForwardSpec) (Forward, error) {
	f, err := e.Forward(host, spec)
	var inUse *forward.PortInUseError
	if !errors.As(err, &inUse) {
		return f, err
	}
	switch e.o.OnConflict {
	case ConflictFail:
		return f, err
	case ConflictRandom:
		port, ferr := forward.FreePort(cmp.Or(spec.BindAddr, e.o.BindAddr))
		if ferr != nil {
			return f, err
		}
		spec.LocalPort = port
	default:
		if inUse.Suggested == 0 {
			return f, err
		}
		spec.LocalPort = inUse.Suggested
	}
	return e.Forward(host, spec)
}

// remembered runs once per host per run, on its first connection, before
// the Connected event: it reads state.json, so forwards made from then on
// are saved, and returns the forwards to restore: the host's config
// forwards, then the remembered ones that don't duplicate them.
func (e *Engine) remembered(host HostID, hs *hostState) []ForwardSpec {
	var specs []ForwardSpec
	for _, spec := range hs.settings.Forwards {
		spec.Config, spec.Pinned = true, true
		specs = append(specs, spec)
	}
	if e.o.State != nil {
		saved, err := e.o.State.Load(string(host))
		if err != nil {
			e.log.Warn("could not read remembered forwards", "host", host, "err", err)
			e.emit(RestoreFailed{Host: host, Err: err})
		} else {
			e.mu.Lock()
			hs.loaded = true
			e.mu.Unlock()
			for _, sf := range saved {
				spec, ok := fromState(sf)
				if !ok {
					continue
				}
				if !slices.ContainsFunc(specs, func(s ForwardSpec) bool { return e.sameTarget(s, spec) }) {
					specs = append(specs, spec)
				}
			}
		}
	}
	return specs
}

// restore creates the forwards remembered returned, following OnConflict
// for busy local ports. One that fails stays remembered, and one moved to
// another local port keeps asking for its own next time.
func (e *Engine) restore(host HostID, specs []ForwardSpec) {
	for _, spec := range specs {
		f, err := e.forwardFollowingConflict(host, spec)
		switch {
		case errors.Is(err, errAlreadyForwarded):
		case err != nil:
			e.log.Info("restore failed", "host", host, "port", spec.RemotePort, "err", err)
			if !spec.Config {
				e.mu.Lock()
				e.hostLocked(host).unrestored = append(e.hostLocked(host).unrestored, spec)
				e.mu.Unlock()
			}
			e.emit(RestoreFailed{Host: host, Spec: spec, Err: err})
		case spec.LocalPort != 0 && f.Spec.LocalPort != spec.LocalPort:
			e.mu.Lock()
			if ff, ok := e.forwards[f.ID]; ok {
				ff.want = spec.LocalPort
			}
			e.mu.Unlock()
		}
	}
	e.persist(host)
}

// sameTarget reports whether two specs forward the same local address to
// the same remote one, after defaults.
func (e *Engine) sameTarget(a, b ForwardSpec) bool {
	norm := func(s ForwardSpec) ForwardSpec {
		s.BindAddr = cmp.Or(s.BindAddr, e.o.BindAddr)
		s.RemoteHost = cmp.Or(s.RemoteHost, "127.0.0.1")
		if s.LocalPort == 0 {
			s.LocalPort = s.RemotePort
		}
		return s
	}
	a, b = norm(a), norm(b)
	return a.Kind == b.Kind && a.BindAddr == b.BindAddr && a.LocalPort == b.LocalPort && a.RemoteHost == b.RemoteHost && a.RemotePort == b.RemotePort
}

// persist saves host's local forwards that aren't from config to
// state.json, once that host's remembered forwards were read this run, so
// a host that never connected keeps what it had. Errors are logged: a
// state file that can't be written must not stop forwarding.
func (e *Engine) persist(host HostID) {
	e.persistOnly(host, false)
}

// persistOnly saves host's forwards; pinnedOnly keeps just the pinned ones,
// as on quit.
func (e *Engine) persistOnly(host HostID, pinnedOnly bool) {
	if e.o.State == nil {
		return
	}
	// The snapshot is taken under persistMu, so the last save wins with the
	// newest list.
	e.persistMu.Lock()
	defer e.persistMu.Unlock()
	e.mu.Lock()
	hs, ok := e.hosts[host]
	if !ok || !hs.loaded {
		e.mu.Unlock()
		return
	}
	var list []state.Forward
	var running []ForwardSpec
	for _, f := range e.forwards {
		if f.Host != host || f.Spec.Kind != Local || f.Spec.Config {
			continue
		}
		spec := f.Spec
		if f.want != 0 {
			spec.LocalPort = f.want
		}
		running = append(running, spec)
		if !pinnedOnly || spec.Pinned {
			list = append(list, toState(spec))
		}
	}
	for _, spec := range hs.unrestored {
		if (!pinnedOnly || spec.Pinned) && !slices.ContainsFunc(running, func(r ForwardSpec) bool { return e.sameTarget(r, spec) }) {
			list = append(list, toState(spec))
		}
	}
	e.mu.Unlock()
	slices.SortFunc(list, func(a, b state.Forward) int {
		return cmp.Or(cmp.Compare(a.Remote, b.Remote), cmp.Compare(a.Local, b.Local))
	})
	if err := e.o.State.Save(string(host), list); err != nil {
		e.log.Warn("could not save forwards", "host", host, "err", err)
	}
}

func toState(s ForwardSpec) state.Forward {
	return state.Forward{Kind: s.Kind.String(), Bind: s.BindAddr, Local: s.LocalPort, RemoteHost: s.RemoteHost,
		Remote: s.RemotePort, Label: s.Label, Pinned: s.Pinned, Auto: s.Auto}
}

// fromState turns a remembered forward back into a spec; false for kinds
// this pfwd can't run.
func fromState(f state.Forward) (ForwardSpec, bool) {
	if f.Kind != Local.String() || f.Remote == 0 {
		return ForwardSpec{}, false
	}
	return ForwardSpec{Kind: Local, BindAddr: f.Bind, LocalPort: f.Local, RemoteHost: f.RemoteHost,
		RemotePort: f.Remote, Label: f.Label, Pinned: f.Pinned, Auto: f.Auto}, true
}
