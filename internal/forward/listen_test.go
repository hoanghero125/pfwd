package forward

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func port(t *testing.T, ln net.Listener) uint16 {
	t.Helper()
	return netip.MustParseAddrPort(ln.Addr().String()).Port()
}

func TestListenPortInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	p := port(t, busy)

	_, err = Listen("127.0.0.1", p)
	var inUse *PortInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("Listen on a busy port = %v, want *PortInUseError", err)
	}
	if inUse.Port != p || inUse.Addr != "127.0.0.1" {
		t.Errorf("error = %+v", inUse)
	}
	if inUse.Suggested == 0 || inUse.Suggested == p {
		t.Fatalf("Suggested = %d, want a different free port", inUse.Suggested)
	}
	if !strings.Contains(inUse.Error(), "in use") {
		t.Errorf("message = %q", inUse.Error())
	}
	ln, err := Listen("127.0.0.1", inUse.Suggested)
	if err != nil {
		t.Fatalf("the suggested port %d is not free: %v", inUse.Suggested, err)
	}
	ln.Close()
}

func TestListenDefaultsLoopback(t *testing.T) {
	ln, err := Listen("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ip := netip.MustParseAddrPort(ln.Addr().String()).Addr(); ip.String() != "127.0.0.1" {
		t.Errorf("bound %v, want 127.0.0.1", ip)
	}
}

func TestPortInUseErrorWithoutSuggestion(t *testing.T) {
	e := &PortInUseError{Addr: "127.0.0.1", Port: 65535}
	if msg := e.Error(); !strings.Contains(msg, "65535") || strings.Contains(msg, "instead") {
		t.Errorf("message = %q", msg)
	}
}

// hold occupies port p on network/host for the rest of the test, or skips
// when the machine can't (no IPv6).
func hold(t *testing.T, network, host string, p uint16) {
	t.Helper()
	ln, err := net.Listen(network, net.JoinHostPort(host, strconv.Itoa(int(p))))
	if err != nil {
		t.Skipf("can't hold %s %s:%d here: %v", network, host, p, err)
	}
	t.Cleanup(func() { ln.Close() })
}

// freeLoopbackPort returns a port that nothing holds right now.
func freeLoopbackPort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := port(t, ln)
	ln.Close()
	return p
}

// TestListenSeesOtherLocalHolders pins the fix for silent shadowing: on
// Windows and macOS, binding 127.0.0.1:P succeeds while another process
// listens on 0.0.0.0:P or [::]:P, and everywhere while one listens on
// [::1]:P. Browsers try ::1 first for localhost, so they would reach the
// local service instead of the forward, with no conflict shown.
func TestListenSeesOtherLocalHolders(t *testing.T) {
	cases := []struct{ name, network, host string }{
		{"ipv4 wildcard", "tcp4", "0.0.0.0"},
		{"ipv6 wildcard", "tcp6", "::"},
		{"ipv6 loopback", "tcp6", "::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := freeLoopbackPort(t)
			hold(t, c.network, c.host, p)
			ln, err := Listen("127.0.0.1", p)
			if err == nil {
				ln.Close()
				t.Fatalf("Listen(127.0.0.1, %d) succeeded while %s %s held the port", p, c.network, c.host)
			}
			var inUse *PortInUseError
			if !errors.As(err, &inUse) || inUse.Suggested == p {
				t.Fatalf("Listen = %v, want *PortInUseError suggesting another port", err)
			}
		})
	}
}

func TestSuggestedPortIsFreeEverywhere(t *testing.T) {
	p := freeLoopbackPort(t)
	hold(t, "tcp4", "127.0.0.1", p)
	hold(t, "tcp6", "::1", p+1)
	_, err := Listen("127.0.0.1", p)
	var inUse *PortInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("Listen = %v, want *PortInUseError", err)
	}
	if inUse.Suggested == p+1 {
		t.Errorf("suggested %d, which is held on ::1", p+1)
	}
}
