package forward

import (
	"errors"
	"net"
	"net/netip"
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
