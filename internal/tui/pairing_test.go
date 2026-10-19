package tui

import (
	"strings"
	"testing"

	"github.com/hoanghero125/pfwd/internal/engine"
)

// TestForwardToAnotherMachineLeavesThePortDetected: a forward to
// db.internal:5432 doesn't stand for the server's own :5432, which stays in
// the detected group, and its row doesn't borrow that port's process.
func TestForwardToAnotherMachineLeavesThePortDetected(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(5432, "postgres", "127.0.0.1"))
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "1", Host: "devbox", State: engine.Active,
		Spec: engine.ForwardSpec{LocalPort: 15432, RemoteHost: "db.internal", RemotePort: 5432}}})
	v := view(m)
	before, after, ok := strings.Cut(v, "not forwarded")
	if !ok || !strings.Contains(after, ":5432") || !strings.Contains(after, "postgres") {
		t.Errorf("the server's :5432 left the detected group:\n%s", v)
	}
	if !strings.Contains(before, "db.internal:5432") || strings.Contains(before, "postgres") {
		t.Errorf("the forward row borrowed the local port's process:\n%s", v)
	}
}

// TestForwardOfAPortBoundToOneAddress: a forward to the address a port
// listens on stands for it; one to localhost doesn't.
func TestForwardOfAPortBoundToOneAddress(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(9000, "api", "10.0.0.5"))
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "1", Host: "devbox", State: engine.Active,
		Spec: engine.ForwardSpec{LocalPort: 9000, RemoteHost: "10.0.0.5", RemotePort: 9000}}})
	if v := view(m); strings.Contains(v, "not forwarded") {
		t.Errorf("the forwarded port is still listed as detected:\n%s", v)
	}
	m = connectedWith(newModel(newFakeEngine()), rp(9000, "api", "10.0.0.5"))
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "1", Host: "devbox", State: engine.Active,
		Spec: engine.ForwardSpec{LocalPort: 9000, RemoteHost: "127.0.0.1", RemotePort: 9000}}})
	if v := view(m); !strings.Contains(v, "not forwarded") {
		t.Errorf("a forward to localhost hid the port bound to 10.0.0.5:\n%s", v)
	}
}
