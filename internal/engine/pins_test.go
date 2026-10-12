package engine

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/state"
)

// fakeClock is a settable clock for expiry tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// manualOptions is auto-forward off, one-scan debounce.
func manualOptions(c *fakeConn) Options {
	return Options{Dial: dialTo(c), Interval: time.Second, Debounce: 1,
		Settings: func(HostID) HostSettings { return HostSettings{Filter: detect.DefaultFilter()} }}
}

func TestPortGonePausesAndBackResumes(t *testing.T) {
	echo := echoServer(t)
	c := newFakeConn(listen(echo, "python3"))
	e := newEngineWith(t, manualOptions(c))
	connected(t, e, "devbox")
	next[PortsChanged](t, e, nil)
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c.send(listen(22, "sshd"))
	paused := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Paused })
	if paused.Forward.Pause != PortGone {
		t.Errorf("Pause = %v, want PortGone", paused.Forward.Pause)
	}
	// New connections close at once instead of waiting.
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(f.Spec.LocalPort)))
	if err != nil {
		t.Fatalf("the listener of a paused forward is gone: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, rerr := conn.Read(make([]byte, 1))
	conn.Close()
	if rerr == nil || time.Since(start) > time.Second {
		t.Errorf("read on a paused forward = %v after %v, want a quick close", rerr, time.Since(start))
	}
	c.send(listen(echo, "python3"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Active })
	echoThrough(t, f.Spec.LocalPort, "back again")
}

func TestRemoteTargetNeverPausesByPort(t *testing.T) {
	c := newFakeConn(listen(22, "sshd"))
	e := newEngineWith(t, manualOptions(c))
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemoteHost: "db.internal", RemotePort: 5432})
	if err != nil {
		t.Fatal(err)
	}
	c.send(listen(22, "sshd"))
	noEvent(t, e, 300*time.Millisecond, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Paused })
}

func TestAutoForwardExpires(t *testing.T) {
	p := freePort(t)
	clock := &fakeClock{now: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}
	c := newFakeConn(listen(22, "sshd"))
	o := autoOptions(dialTo(c))
	o.Debounce, o.Now, o.AutoExpire = 1, clock.Now, 10*time.Minute
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	c.send(listen(22, "sshd"), listen(p, "node"))
	auto := next(t, e, isAutoForwardOf(p))
	c.send(listen(22, "sshd"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == auto.Forward.ID && ch.Forward.State == Paused })
	clock.advance(9 * time.Minute)
	c.send(listen(22, "sshd"))
	noEvent(t, e, 200*time.Millisecond, func(ch ForwardChanged) bool { return ch.Forward.State == Stopped })
	clock.advance(2 * time.Minute)
	c.send(listen(22, "sshd"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == auto.Forward.ID && ch.Forward.State == Stopped })
	if fs := e.Forwards(); len(fs) != 0 {
		t.Errorf("Forwards = %+v, want the expired forward gone", fs)
	}
	// The port coming back is auto-forwarded again: expiry isn't a stop.
	c.send(listen(22, "sshd"), listen(p, "node"))
	next(t, e, isAutoForwardOf(p))
}

func TestOnlyUnpinnedAutoForwardsExpire(t *testing.T) {
	echo := echoServer(t)
	clock := &fakeClock{now: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}
	c := newFakeConn(listen(echo, "python3") + listen(echo+1, "python3"))
	o := manualOptions(c)
	o.Now, o.AutoExpire = clock.Now, time.Minute
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	manual, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo + 1, Auto: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	c.send(listen(22, "sshd"))
	clock.advance(time.Hour)
	c.send(listen(22, "sshd"))
	noEvent(t, e, 300*time.Millisecond, func(ch ForwardChanged) bool { return ch.Forward.State == Stopped })
	if fs := e.Forwards(); len(fs) != 2 {
		t.Errorf("Forwards = %+v, want the manual and the pinned forward kept", fs)
	}
	_ = manual
}

// storeAt opens a state store in dir.
func storeAt(dir string) *state.Store {
	return state.Open(filepath.Join(dir, "state.json"), state.Options{})
}

func TestPinSurvivesQuitAndIsRestored(t *testing.T) {
	echo := echoServer(t)
	dir := t.TempDir()
	c := newFakeConn(listen(echo, "python3"))
	o := manualOptions(c)
	o.State = storeAt(dir)
	e := New(o)
	connected(t, e, "devbox")
	keep, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo, Label: "API"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: echo + 7}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetPinned(keep.ID, true); err != nil {
		t.Fatal(err)
	}
	pinnedEv := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == keep.ID && ch.Forward.Spec.Pinned })
	if !pinnedEv.Forward.Spec.Pinned {
		t.Fatal("SetPinned emitted no pinned forward")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	saved, err := storeAt(dir).Load("devbox")
	if err != nil || len(saved) != 1 || saved[0].Local != keep.Spec.LocalPort || !saved[0].Pinned || saved[0].Label != "API" {
		t.Fatalf("state after quit = %+v, %v; want only the pinned forward", saved, err)
	}

	c2 := newFakeConn(listen(echo, "python3"))
	o2 := manualOptions(c2)
	o2.State = storeAt(dir)
	e2 := newEngineWith(t, o2)
	e2.Connect("devbox")
	restored := next(t, e2, func(ch ForwardChanged) bool { return ch.Forward.State == Active })
	if restored.Forward.Spec.LocalPort != keep.Spec.LocalPort || !restored.Forward.Spec.Pinned {
		t.Errorf("restored = %+v, want the pinned forward on its local port", restored.Forward.Spec)
	}
	echoThrough(t, keep.Spec.LocalPort, "restored")
}

func TestUnpinnedForwardIsSavedWhileRunning(t *testing.T) {
	dir := t.TempDir()
	c := newFakeConn(listen(22, "sshd"))
	o := manualOptions(c)
	o.State = storeAt(dir)
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: 8080, Auto: true})
	if err != nil {
		t.Fatal(err)
	}
	// A crash now would leave this in state.json, to restore next time.
	saved, err := storeAt(dir).Load("devbox")
	if err != nil || len(saved) != 1 || saved[0].Local != f.Spec.LocalPort || !saved[0].Auto || saved[0].Pinned {
		t.Fatalf("state while running = %+v, %v", saved, err)
	}
	if err := e.Stop(f.ID); err != nil {
		t.Fatal(err)
	}
	if saved, _ := storeAt(dir).Load("devbox"); len(saved) != 0 {
		t.Errorf("state after Stop = %+v, want empty", saved)
	}
}

func TestConfigForwardsAreCreatedNotSaved(t *testing.T) {
	echo := echoServer(t)
	dir := t.TempDir()
	local := freePort(t)
	c := newFakeConn(listen(echo, "python3"))
	o := manualOptions(c)
	o.State = storeAt(dir)
	o.Settings = func(HostID) HostSettings {
		return HostSettings{Filter: detect.DefaultFilter(), Forwards: []ForwardSpec{{LocalPort: local, RemotePort: echo}}}
	}
	e := newEngineWith(t, o)
	e.Connect("devbox")
	got := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Active })
	if !got.Forward.Spec.Config || !got.Forward.Spec.Pinned || got.Forward.Spec.LocalPort != local {
		t.Errorf("config forward = %+v, want Config and Pinned on port %d", got.Forward.Spec, local)
	}
	if err := e.SetPinned(got.Forward.ID, false); err == nil || !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("unpinning a config forward: %v, want an error pointing at config.toml", err)
	}
	if saved, _ := storeAt(dir).Load("devbox"); len(saved) != 0 {
		t.Errorf("state = %+v, want config forwards left out", saved)
	}
}

func TestRestoreSkipsDuplicateOfConfigForward(t *testing.T) {
	echo := echoServer(t)
	dir := t.TempDir()
	local := freePort(t)
	if err := storeAt(dir).Save("devbox", []state.Forward{{Kind: "local", Local: local, RemoteHost: "127.0.0.1", Remote: echo, Pinned: true}}); err != nil {
		t.Fatal(err)
	}
	c := newFakeConn(listen(echo, "python3"))
	o := manualOptions(c)
	o.State = storeAt(dir)
	o.Settings = func(HostID) HostSettings {
		return HostSettings{Filter: detect.DefaultFilter(), Forwards: []ForwardSpec{{LocalPort: local, RemotePort: echo}}}
	}
	e := newEngineWith(t, o)
	e.Connect("devbox")
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Active })
	noEvent(t, e, 300*time.Millisecond, func(r RestoreFailed) bool { return true })
	if fs := e.Forwards(); len(fs) != 1 {
		t.Errorf("Forwards = %+v, want one", fs)
	}
}

func TestRestoreBusyPortFollowsOnConflict(t *testing.T) {
	echo := echoServer(t)
	dir := t.TempDir()
	busy := freePort(t)
	holdLocal(t, busy)
	if err := storeAt(dir).Save("devbox", []state.Forward{{Kind: "local", Local: busy, RemoteHost: "127.0.0.1", Remote: echo, Pinned: true}}); err != nil {
		t.Fatal(err)
	}
	c := newFakeConn(listen(echo, "python3"))
	o := manualOptions(c)
	o.State, o.OnConflict = storeAt(dir), ConflictNext
	e := newEngineWith(t, o)
	e.Connect("devbox")
	got := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.State == Active })
	if got.Forward.Spec.LocalPort == busy || got.Forward.Spec.LocalPort == 0 {
		t.Errorf("restored on port %d, want a free one near %d", got.Forward.Spec.LocalPort, busy)
	}

	// A separate state file: the first engine still saves to its own.
	dir3 := t.TempDir()
	o.OnConflict, o.State = ConflictFail, storeAt(dir3)
	c3 := newFakeConn(listen(echo, "python3"))
	o.Dial = dialTo(c3)
	if err := storeAt(dir3).Save("devbox", []state.Forward{{Kind: "local", Local: busy, RemoteHost: "127.0.0.1", Remote: echo, Pinned: true}}); err != nil {
		t.Fatal(err)
	}
	e3 := newEngineWith(t, o)
	e3.Connect("devbox")
	failed := next[RestoreFailed](t, e3, nil)
	if failed.Spec.LocalPort != busy || failed.Err == nil {
		t.Errorf("RestoreFailed = %+v", failed)
	}
}

func TestStateErrorsAreNotFatal(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := newFakeConn(listen(22, "sshd"))
	o := manualOptions(c)
	o.State = state.Open(filepath.Join(blocker, "state.json"), state.Options{})
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	if _, err := e.Forward("devbox", ForwardSpec{LocalPort: freePort(t), RemotePort: 8080}); err != nil {
		t.Fatalf("Forward failed because state.json can't be written: %v", err)
	}
}

func TestUnreachedHostKeepsItsState(t *testing.T) {
	dir := t.TempDir()
	remembered := []state.Forward{{Kind: "local", Local: 5432, RemoteHost: "127.0.0.1", Remote: 5432, Pinned: true}}
	if err := storeAt(dir).Save("devbox", remembered); err != nil {
		t.Fatal(err)
	}
	o := manualOptions(nil)
	o.State = storeAt(dir)
	o.Dial = (&dialPlan{steps: []any{errUnreachable}}).dial
	e := New(o)
	e.Connect("devbox")
	next(t, e, func(s SessionChanged) bool { return s.State == SessionFailed })
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if saved, _ := storeAt(dir).Load("devbox"); len(saved) != 1 {
		t.Errorf("state = %+v, want the remembered forward kept for a host that never connected", saved)
	}
}

func TestSetPinnedUnknownForward(t *testing.T) {
	e := newEngineWith(t, manualOptions(newFakeConn()))
	if err := e.SetPinned("nope", true); err == nil {
		t.Error("pinning an unknown forward succeeded")
	}
}
