package engine

import "fmt"

// Disconnect ends host's connection and closes its forwards the way
// quitting does: pinned forwards are remembered and come back on the next
// Connect, the rest are forgotten. The session ends Closed. A host that
// isn't known does nothing.
func (e *Engine) Disconnect(host HostID) {
	e.mu.Lock()
	s, ok := e.sessions[host]
	if !ok || e.closed {
		e.mu.Unlock()
		return
	}
	s.disconnecting = true
	ended := s.state == SessionFailed || s.state == Closed
	e.mu.Unlock()

	// End the session loop first, so nothing it does (a restore, an
	// auto-forward) adds a forward after the sweep below.
	s.cancel()
	<-s.done

	// Remember the pinned forwards before they go.
	e.persistOnly(host, true)
	e.mu.Lock()
	var fws []*fwd
	for id, f := range e.forwards {
		if f.Host == host {
			fws = append(fws, f)
			delete(e.forwards, id)
		}
	}
	hs := e.hostLocked(host)
	// The next connection reads state.json again and restores from it;
	// until then nothing writes the host's entry, so quitting keeps it.
	hs.restored, hs.loaded, hs.unrestored = false, false, nil
	e.mu.Unlock()

	for _, f := range fws {
		f.cancel()
		<-f.done
		e.mu.Lock()
		f.State = Stopped
		e.mu.Unlock()
		e.emit(ForwardChanged{Forward: e.snapshot(f)})
	}
	if ended {
		// The session loop had returned already; say it is closed now.
		e.setState(s, Closed)
		e.emit(SessionChanged{Host: host, State: Closed})
	}
	e.log.Info("disconnected", "host", host, "forwards", len(fws))
}

// SetLabel names a forward. The label is remembered with the forward.
// Forwards from config.toml keep theirs.
func (e *Engine) SetLabel(id, label string) error {
	e.mu.Lock()
	f, ok := e.forwards[id]
	switch {
	case !ok:
		e.mu.Unlock()
		return fmt.Errorf("no forward with id %q", id)
	case f.Spec.Config:
		e.mu.Unlock()
		return &ConfigForwardError{Port: f.Spec.RemotePort, Host: f.Host}
	}
	changed := f.Spec.Label != label
	f.Spec.Label = label
	e.mu.Unlock()
	if changed {
		e.emit(ForwardChanged{Forward: e.snapshot(f)})
		e.persist(f.Host)
	}
	return nil
}

// ConfigForwardError refuses to change a forward that config.toml defines.
type ConfigForwardError struct {
	Port uint16
	Host HostID
}

func (e *ConfigForwardError) Error() string {
	return fmt.Sprintf("the forward of port %d comes from config.toml (hosts.%s.forwards); edit the file to change it", e.Port, e.Host)
}
