package detect

import (
	"reflect"
	"testing"
)

func portsOf(ps ...uint16) []RemotePort {
	out := make([]RemotePort, 0, len(ps))
	for _, p := range ps {
		out = append(out, RemotePort{Port: p, Addrs: []string{"0.0.0.0"}})
	}
	return out
}

func addedPorts(added []RemotePort) []uint16 {
	var out []uint16
	for _, p := range added {
		out = append(out, p.Port)
	}
	return out
}

func TestTrackerBaselineIsNotAdded(t *testing.T) {
	tr := NewTracker(2)
	added, removed := tr.Observe(portsOf(22, 5173))
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("first frame: added %v removed %v, want nothing (it is the baseline)", addedPorts(added), removed)
	}
}

func TestTrackerDebounce(t *testing.T) {
	tr := NewTracker(2)
	tr.Observe(portsOf(22))
	steps := []struct {
		ports   []uint16
		added   []uint16
		removed []uint16
	}{
		{[]uint16{22, 8000}, nil, nil},            // seen once
		{[]uint16{22, 8000}, []uint16{8000}, nil}, // second consecutive frame
		{[]uint16{22, 8000}, nil, nil},            // already known
		{[]uint16{22}, nil, []uint16{8000}},       // gone
		{[]uint16{22, 8000}, nil, nil},            // back, counts again
		{[]uint16{22, 8000}, []uint16{8000}, nil},
	}
	for i, s := range steps {
		added, removed := tr.Observe(portsOf(s.ports...))
		if !reflect.DeepEqual(addedPorts(added), s.added) || !reflect.DeepEqual(removed, s.removed) {
			t.Errorf("step %d (%v): added %v removed %v, want %v %v", i, s.ports, addedPorts(added), removed, s.added, s.removed)
		}
	}
}

func TestTrackerFlapBeforeDebounceIsIgnored(t *testing.T) {
	tr := NewTracker(2)
	tr.Observe(nil)
	for i, ports := range [][]uint16{{9000}, {}, {9000}, {}} {
		if added, removed := tr.Observe(portsOf(ports...)); len(added) != 0 || len(removed) != 0 {
			t.Errorf("frame %d: added %v removed %v, want nothing for a port that never stayed", i, addedPorts(added), removed)
		}
	}
}

func TestTrackerDebounceOne(t *testing.T) {
	for _, d := range []int{1, 0, -3} {
		tr := NewTracker(d)
		tr.Observe(nil)
		if added, _ := tr.Observe(portsOf(3000)); !reflect.DeepEqual(addedPorts(added), []uint16{3000}) {
			t.Errorf("debounce %d: added %v, want [3000] on first sight", d, addedPorts(added))
		}
	}
}

func TestTrackerAddedIsSortedAndCopied(t *testing.T) {
	tr := NewTracker(1)
	tr.Observe(nil)
	in := portsOf(9090, 3000)
	added, _ := tr.Observe(in)
	if !reflect.DeepEqual(addedPorts(added), []uint16{3000, 9090}) {
		t.Errorf("added %v, want sorted", addedPorts(added))
	}
	added[0].Addrs[0] = "changed"
	if in[1].Addrs[0] != "0.0.0.0" {
		t.Error("Observe returned ports that share memory with its input")
	}
}
