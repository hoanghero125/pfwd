package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
)

var (
	remoteFwd = engine.Forward{ID: "21", Host: "devbox", State: engine.Active,
		Spec: engine.ForwardSpec{Kind: engine.Remote, BindAddr: "127.0.0.1", RemotePort: 9000, RemoteHost: "127.0.0.1", LocalPort: 3000}}
	socksFwd = engine.Forward{ID: "22", Host: "devbox", State: engine.Active,
		Spec: engine.ForwardSpec{Kind: engine.Dynamic, BindAddr: "127.0.0.1", LocalPort: 1080}}
)

func TestKindRows(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(5173, "node"))
	m = apply(m, engine.ForwardChanged{Forward: remoteFwd}, engine.ForwardChanged{Forward: socksFwd})
	v := view(m)
	for _, want := range []string{"rev", ":9000", "localhost:3000", "socks", "localhost:1080", "SOCKS"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestOwnRemoteListenerIsHidden(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(9000, ""), rp(5173, "node"))
	m = apply(m, engine.ForwardChanged{Forward: remoteFwd})
	if n := strings.Count(view(m), ":9000"); n != 1 {
		t.Errorf(":9000 shows %d times; the server-side listener of a -R forward is pfwd's own:\n%s", n, view(m))
	}
}

// localPortsFake returns fixed local ports, or err.
func localPortsFake(ports []detect.RemotePort, err error) func(context.Context) ([]detect.RemotePort, error) {
	return func(context.Context) ([]detect.RemotePort, error) { return ports, err }
}

var localSample = []detect.RemotePort{
	{Port: 3000, Addrs: []string{"127.0.0.1"}, Process: "node"},
	{Port: 8080, Addrs: []string{"0.0.0.0"}, Process: "python3"},
}

func TestReverseTabExposesAPort(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModelWith(e, Options{LocalPorts: localPortsFake(localSample, nil)}), rp(5173, "node"))
	m, _ = press(t, m, "r")
	v := view(m)
	if !strings.Contains(v, ":3000") || !strings.Contains(v, "python3") || !strings.Contains(v, "machine's listening ports") {
		t.Fatalf("Reverse tab:\n%s", v)
	}
	m, _ = press(t, m, "enter")
	if len(e.specs) != 1 {
		t.Fatalf("Forward calls = %+v", e.specs)
	}
	if got := e.specs[0]; got.Kind != engine.Remote || got.RemotePort != 3000 || got.LocalPort != 3000 {
		t.Errorf("spec = %+v, want -R 3000:3000", got)
	}
	if m.reverseOpen {
		t.Error("the Reverse tab stayed open after exposing a port")
	}
	if v := view(m); !strings.Contains(v, "Exposed localhost:3000") {
		t.Errorf("status after exposing:\n%s", v)
	}
}

// TestReverseTabTargetsTheListeningAddress: a server that listens only on
// ::1, as Vite does on macOS, is exposed with ::1 as the target.
func TestReverseTabTargetsTheListeningAddress(t *testing.T) {
	e := newFakeEngine()
	ports := []detect.RemotePort{{Port: 5173, Addrs: []string{"::1"}, Process: "node"}}
	m := connectedWith(newModelWith(e, Options{LocalPorts: localPortsFake(ports, nil)}), rp(22, "sshd"))
	m, _ = press(t, m, "r", "enter")
	if len(e.specs) != 1 || e.specs[0].RemoteHost != "::1" {
		t.Errorf("Forward calls = %+v, want the target ::1", e.specs)
	}
	_, _ = press(t, m, "r", "F", "9", "0", "0", "0", "enter")
	if len(e.specs) != 2 || e.specs[1].RemoteHost != "::1" || e.specs[1].RemotePort != 9000 {
		t.Errorf("Forward calls = %+v, want :9000 to ::1", e.specs)
	}
}

// TestSOCKSRestoreFailedNamesTheProxy: a SOCKS proxy from the config that
// can't start is reported as that, not as an unreadable state file.
func TestSOCKSRestoreFailedNamesTheProxy(t *testing.T) {
	m := apply(mainScreen(t), engine.RestoreFailed{Host: "devbox", Spec: engine.ForwardSpec{Kind: engine.Dynamic, BindAddr: "0.0.0.0", LocalPort: 1080},
		Err: errors.New("a SOCKS proxy on 0.0.0.0 would let your network reach anything the server can")})
	v := view(m)
	if strings.Contains(v, "remembered forwards") || !strings.Contains(v, "Couldn't start the SOCKS proxy") {
		t.Errorf("status:\n%s", v)
	}
}

func TestReverseTabOtherServerPort(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModelWith(e, Options{LocalPorts: localPortsFake(localSample, nil)}))
	m, _ = press(t, m, "r", "down", "F")
	if v := view(m); !strings.Contains(v, "server port") {
		t.Fatalf("modal for another server port:\n%s", v)
	}
	_, _ = press(t, m, "9", "0", "9", "0", "enter")
	if len(e.specs) != 1 || e.specs[0].Kind != engine.Remote || e.specs[0].RemotePort != 9090 || e.specs[0].LocalPort != 8080 {
		t.Errorf("Forward calls = %+v, want -R 9090:8080", e.specs)
	}
}

func TestReverseTabErrorsAndEmpty(t *testing.T) {
	m := connectedWith(newModelWith(newFakeEngine(), Options{LocalPorts: localPortsFake(nil, errors.New("list this machine's listening ports: lsof: not found; install lsof"))}))
	m, _ = press(t, m, "r")
	if v := view(m); !strings.Contains(v, "install lsof") {
		t.Errorf("error view:\n%s", v)
	}
	m = connectedWith(newModelWith(newFakeEngine(), Options{LocalPorts: localPortsFake(nil, nil)}))
	m, _ = press(t, m, "r")
	if v := view(m); !strings.Contains(v, "Nothing listens on this machine") {
		t.Errorf("empty view:\n%s", v)
	}
	m, _ = press(t, m, "esc")
	if m.reverseOpen {
		t.Error("esc didn't close the Reverse tab")
	}
}

func TestSOCKSToggle(t *testing.T) {
	e := newFakeEngine()
	m := connectedWith(newModel(e), rp(5173, "node"))
	m, _ = press(t, m, "d")
	if len(e.specs) != 1 || e.specs[0].Kind != engine.Dynamic || e.specs[0].LocalPort != 1080 {
		t.Fatalf("d forwarded %+v, want a SOCKS proxy on 1080", e.specs)
	}
	proxy := e.Forwards()[0]
	_, _ = press(t, m, "d")
	if len(e.stopped) != 1 || e.stopped[0] != proxy.ID {
		t.Errorf("the second d stopped %v, want the proxy %s", e.stopped, proxy.ID)
	}
}

func TestRemoteForwardWaitsForServerPort(t *testing.T) {
	m := apply(connectedWith(newModel(newFakeEngine()), rp(5173, "node")), engine.ForwardChanged{Forward: remoteFwd})
	disconnected, busy := remoteFwd, remoteFwd
	disconnected.State, disconnected.Pause = engine.Paused, engine.Disconnected
	busy.State, busy.Pause = engine.Paused, engine.ServerPortBusy
	m = apply(m, engine.ForwardChanged{Forward: disconnected}, engine.ForwardChanged{Forward: busy})
	if v := view(m); !strings.Contains(v, "port busy") {
		t.Errorf("the row lacks its reason:\n%s", v)
	}
	m = apply(m, engine.ForwardChanged{Forward: remoteFwd})
	var lines []string
	for _, e := range m.log {
		lines = append(lines, e.text)
	}
	log := strings.Join(lines, "\n")
	for _, want := range []string{"Waiting for :9000 on devbox", "Resumed :9000"} {
		if !strings.Contains(log, want) {
			t.Errorf("event log lacks %q:\n%s", want, log)
		}
	}
}

func TestMappedPortIsExplained(t *testing.T) {
	f := engine.Forward{ID: "30", Host: "devbox", State: engine.Active, MappedFrom: 80,
		Spec: engine.ForwardSpec{Kind: engine.Local, BindAddr: "127.0.0.1", LocalPort: 10080, RemoteHost: "127.0.0.1", RemotePort: 80, Auto: true}}
	m := apply(connectedWith(newModel(newFakeEngine()), rp(80, "nginx")), engine.ForwardChanged{Forward: f})
	if v := view(m); !strings.Contains(v, "80 needs privileges") || !strings.Contains(v, "10080") {
		t.Errorf("view:\n%s", v)
	}
}

func TestUnavailableNamesTheTools(t *testing.T) {
	m := apply(newModel(newFakeEngine()),
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.None, OS: "SunOS"})
	v := view(m)
	if strings.Contains(v, "v0.4.0") || !strings.Contains(v, "lsof") {
		t.Errorf("unavailable view:\n%s", v)
	}
}
