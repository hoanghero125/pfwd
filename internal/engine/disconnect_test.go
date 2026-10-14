package engine

import (
	"context"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/sshx"
)

// freshDial hands out a new fake connection, serving frame, on each dial.
func freshDial(frame string) DialFunc {
	return func(context.Context, HostID, Prompts) (Conn, error) { return newFakeConn(frame), nil }
}

func TestDisconnectClosesAndRestoresPinned(t *testing.T) {
	echo := echoServer(t)
	dir := t.TempDir()
	o := manualOptions(nil)
	o.Dial, o.State = freshDial(listen(echo, "python3")), storeAt(dir)
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	keep, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	drop, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo + 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetPinned(keep.ID, true); err != nil {
		t.Fatal(err)
	}

	e.Disconnect("devbox")
	closed := next(t, e, func(s SessionChanged) bool { return s.State == Closed || s.State == SessionFailed })
	if closed.State != Closed || closed.Err != nil {
		t.Fatalf("session after Disconnect = %v (%v), want closed with no error", closed.State, closed.Err)
	}
	if fs := e.Forwards(); len(fs) != 0 {
		t.Fatalf("forwards after Disconnect = %+v, want none", fs)
	}
	saved, err := storeAt(dir).Load("devbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Remote != echo || !saved[0].Pinned {
		t.Fatalf("remembered after Disconnect = %+v, want only the pinned :%d", saved, echo)
	}

	e.Connect("devbox")
	back := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Active })
	if back.Forward.Spec.RemotePort != echo || back.Forward.Spec.LocalPort != keep.Spec.LocalPort {
		t.Errorf("restored %+v, want the pinned forward", back.Forward.Spec)
	}
	echoThrough(t, keep.Spec.LocalPort, "restored")
	noEvent(t, e, 300*time.Millisecond, func(ch ForwardChanged) bool { return ch.Forward.Spec.RemotePort == drop.Spec.RemotePort })
}

func TestDisconnectFailedHost(t *testing.T) {
	e := newEngine(t, func(context.Context, HostID, Prompts) (Conn, error) {
		return nil, &sshx.Error{Kind: sshx.ErrAuth, Msg: "authentication to devbox failed"}
	})
	e.Connect("devbox")
	next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	e.Disconnect("devbox")
	next(t, e, func(s SessionChanged) bool { return s.State == Closed })
}

func TestDisconnectUnknownHostIsANoOp(t *testing.T) {
	e := newEngine(t, freshDial(""))
	e.Disconnect("nowhere")
	noEvent(t, e, 200*time.Millisecond, func(SessionChanged) bool { return true })
}

func TestSetLabel(t *testing.T) {
	echo := echoServer(t)
	dir := t.TempDir()
	c := newFakeConn(listen(echo, "python3"))
	o := manualOptions(c)
	o.State = storeAt(dir)
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetLabel(f.ID, "API"); err != nil {
		t.Fatal(err)
	}
	ev := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.Spec.Label == "API" })
	if ev.Forward.State != Active {
		t.Errorf("state after relabel = %v", ev.Forward.State)
	}
	saved, err := storeAt(dir).Load("devbox")
	if err != nil || len(saved) != 1 || saved[0].Label != "API" {
		t.Errorf("saved = %+v, %v; want the label remembered", saved, err)
	}
	if err := e.SetLabel("nope", "x"); err == nil {
		t.Error("SetLabel of an unknown forward succeeded")
	}
}

func TestSetLabelRefusesConfigForward(t *testing.T) {
	echo := echoServer(t)
	c := newFakeConn(listen(echo, "python3"))
	o := manualOptions(c)
	o.Settings = func(HostID) HostSettings {
		return HostSettings{Forwards: []ForwardSpec{{LocalPort: freePort(t), RemotePort: echo}}}
	}
	e := newEngineWith(t, o)
	e.Connect("devbox")
	f := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Active })
	if err := e.SetLabel(f.Forward.ID, "x"); err == nil {
		t.Error("relabeled a forward from config.toml")
	}
}
