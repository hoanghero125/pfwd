package detect

import "testing"

func TestParsePortRange(t *testing.T) {
	good := map[string]PortRange{
		"5173":      {5173, 5173},
		"3000-3999": {3000, 3999},
		" 80 ":      {80, 80},
		"1-65535":   {1, 65535},
		"8080-8080": {8080, 8080},
	}
	for in, want := range good {
		got, err := ParsePortRange(in)
		if err != nil || got != want {
			t.Errorf("ParsePortRange(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "65536", "abc", "3999-3000", "1-", "-5", "1-2-3", "80 - 90", "0x50"} {
		if r, err := ParsePortRange(bad); err == nil {
			t.Errorf("ParsePortRange(%q) = %v, want an error", bad, r)
		}
	}
}

func TestPortSetContains(t *testing.T) {
	s := PortSet{{22, 22}, {3000, 3999}}
	for p, want := range map[uint16]bool{22: true, 23: false, 2999: false, 3000: true, 3500: true, 3999: true, 4000: false} {
		if got := s.Contains(p); got != want {
			t.Errorf("Contains(%d) = %v, want %v", p, got, want)
		}
	}
	if (PortSet{}).Contains(22) {
		t.Error("an empty set contains nothing")
	}
}

func TestPortRangeString(t *testing.T) {
	if s := (PortRange{80, 80}).String(); s != "80" {
		t.Errorf("String = %q", s)
	}
	if s := (PortRange{3000, 3999}).String(); s != "3000-3999" {
		t.Errorf("String = %q", s)
	}
}

func TestDefaultFilterIgnored(t *testing.T) {
	f := DefaultFilter()
	cases := []struct {
		port    uint16
		process string
		want    bool
	}{
		{22, "", true},
		{22, "node", true},
		{2222, "sshd", true},
		{53, "systemd-resolve", true}, // the kernel truncates names to 15 characters
		{53, "systemd-resolved", true},
		{631, "cupsd", true},
		{5173, "node", false},
		{8000, "", false},
		{8000, "sshd-helper", false},
		{8000, "systemd", false}, // shorter than 15: not a truncation of systemd-resolved
	}
	for _, c := range cases {
		if got := f.Ignored(RemotePort{Port: c.port, Process: c.process}); got != c.want {
			t.Errorf("Ignored(%d, %q) = %v, want %v", c.port, c.process, got, c.want)
		}
	}
}

func TestDefaultFilterIsFresh(t *testing.T) {
	a := DefaultFilter()
	a.IgnoreProcesses[0] = "changed"
	a.IgnorePorts[0] = PortRange{1, 1}
	b := DefaultFilter()
	if b.IgnoreProcesses[0] == "changed" || b.IgnorePorts[0].Lo == 1 {
		t.Error("DefaultFilter shares slices between calls")
	}
}

func TestFilterAutoForward(t *testing.T) {
	f := DefaultFilter()
	if !f.AutoForward(RemotePort{Port: 5173, Process: "node"}) {
		t.Error("with no auto_ports, every port that isn't ignored is auto-forwarded")
	}
	if f.AutoForward(RemotePort{Port: 22}) {
		t.Error("an ignored port is never auto-forwarded")
	}
	f.AutoPorts = PortSet{{3000, 3999}, {8888, 8888}}
	for p, want := range map[uint16]bool{3000: true, 3456: true, 8888: true, 5173: false, 22: false} {
		if got := f.AutoForward(RemotePort{Port: p}); got != want {
			t.Errorf("AutoForward(%d) with auto_ports = %v, want %v", p, got, want)
		}
	}
}
