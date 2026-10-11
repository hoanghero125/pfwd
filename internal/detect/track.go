package detect

import (
	"slices"
)

// Tracker decides when a port counts as new. A port is Added once it has
// been in debounce consecutive snapshots, so a short-lived test server is
// never auto-forwarded, and Removed when a stable port is gone. The first
// snapshot is the baseline: ports already listening then are known, not new.
type Tracker struct {
	debounce int
	started  bool
	runs     map[uint16]int
	stable   map[uint16]bool
}

// NewTracker returns a tracker that needs debounce consecutive sightings (at
// least 1) before a port counts as added.
func NewTracker(debounce int) *Tracker {
	return &Tracker{
		debounce: max(debounce, 1),
		runs:     make(map[uint16]int),
		stable:   make(map[uint16]bool),
	}
}

// Observe feeds the next merged snapshot and returns the ports that just
// became stable (sorted by port, copied) and the stable ports that went away.
func (t *Tracker) Observe(ports []RemotePort) (added []RemotePort, removed []uint16) {
	present := make(map[uint16]bool, len(ports))
	for _, p := range ports {
		present[p.Port] = true
	}
	if !t.started {
		t.started = true
		for p := range present {
			t.stable[p] = true
		}
		return nil, nil
	}
	for _, p := range ports {
		if t.stable[p.Port] {
			continue
		}
		t.runs[p.Port]++
		if t.runs[p.Port] >= t.debounce {
			delete(t.runs, p.Port)
			t.stable[p.Port] = true
			cp := p
			cp.Addrs = slices.Clone(p.Addrs)
			added = append(added, cp)
		}
	}
	for p := range t.runs {
		if !present[p] {
			delete(t.runs, p)
		}
	}
	for p := range t.stable {
		if !present[p] {
			delete(t.stable, p)
			removed = append(removed, p)
		}
	}
	slices.SortFunc(added, func(a, b RemotePort) int { return int(a.Port) - int(b.Port) })
	slices.Sort(removed)
	return added, removed
}
