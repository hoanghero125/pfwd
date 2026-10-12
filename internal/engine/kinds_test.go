package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/sshx"
)

// echoVia writes msg to addr and reads it back.
func echoVia(t *testing.T, addr, msg string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("echo via %s = %q, %v", addr, buf, err)
	}
}

func TestRemoteForwardPipes(t *testing.T) {
	echo := echoServer(t)
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	server := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{Kind: Remote, RemotePort: server, LocalPort: echo})
	if err != nil {
		t.Fatal(err)
	}
	if f.State != Active || f.Spec.BindAddr != "127.0.0.1" {
		t.Errorf("forward = %+v, want Active on the server's 127.0.0.1", f)
	}
	echoVia(t, net.JoinHostPort("127.0.0.1", itoa(server)), "from the server")
}

func TestRemoteForwardListensAgainAfterReconnect(t *testing.T) {
	echo := echoServer(t)
	c1, c2 := newFakeConn(), newFakeConn()
	d := &dialPlan{steps: []any{c1, c2}}
	e := reconnectEngine(t, d, 10*time.Millisecond)
	connected(t, e, "devbox")
	server := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{Kind: Remote, RemotePort: server, LocalPort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c1.end(errors.New("broken pipe"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Paused })
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Active })
	addr := net.JoinHostPort("127.0.0.1", itoa(server))
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server-side port never came back: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	echoVia(t, addr, "after the reconnect")
}

// TestRemoteForwardWaitsForServerPort: after a reconnect the server
// refuses the port while the old connection's session still holds it. The
// forward waits, paused, and listens again once the port is free.
func TestRemoteForwardWaitsForServerPort(t *testing.T) {
	echo := echoServer(t)
	c1, c2 := newFakeConn(), newFakeConn()
	c2.refuse = 2
	d := &dialPlan{steps: []any{c1, c2}}
	e := reconnectEngine(t, d, 10*time.Millisecond)
	connected(t, e, "devbox")
	server := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{Kind: Remote, RemotePort: server, LocalPort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c1.end(errors.New("broken pipe"))
	busy := next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.Pause == ServerPortBusy })
	if busy.Forward.State != Paused {
		t.Errorf("state while the port is busy = %v, want Paused", busy.Forward.State)
	}
	back := next(t, e, func(ch ForwardChanged) bool {
		return ch.Forward.ID == f.ID && (ch.Forward.State == Active || ch.Forward.State == Failed)
	})
	if back.Forward.State != Active {
		t.Fatalf("forward = %+v, want Active once the port is free", back.Forward)
	}
	echoVia(t, net.JoinHostPort("127.0.0.1", itoa(server)), "after the wait")
}

// TestRemoteForwardPausedUntilListening: after a reconnect a remote forward
// shows Active only once the server listens again.
func TestRemoteForwardPausedUntilListening(t *testing.T) {
	echo := echoServer(t)
	c1, c2 := newFakeConn(), newFakeConn()
	c2.refuse = 1
	d := &dialPlan{steps: []any{c1, c2}}
	e := reconnectEngine(t, d, 200*time.Millisecond)
	connected(t, e, "devbox")
	server := freePort(t)
	f, err := e.Forward("devbox", ForwardSpec{Kind: Remote, RemotePort: server, LocalPort: echo})
	if err != nil {
		t.Fatal(err)
	}
	c1.end(errors.New("broken pipe"))
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Paused })
	next(t, e, func(ch ForwardChanged) bool { return ch.Forward.ID == f.ID && ch.Forward.State == Active })
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(server)), time.Second)
	if err != nil {
		t.Fatalf("the forward is Active but the server does not listen: %v", err)
	}
	c.Close()
}

func TestRemoteForwardRefused(t *testing.T) {
	c := newFakeConn()
	c.listenErr = &sshx.Error{Kind: sshx.ErrForwardingRefused, Msg: "devbox refused to listen on 127.0.0.1:9000; AllowTcpForwarding may be off"}
	e := newEngine(t, dialTo(c))
	connected(t, e, "devbox")
	_, err := e.Forward("devbox", ForwardSpec{Kind: Remote, RemotePort: 9000})
	if !errors.Is(err, sshx.ErrForwardingRefused) {
		t.Errorf("err = %v, want the server's refusal", err)
	}
	if fs := e.Forwards(); len(fs) != 0 {
		t.Errorf("a refused remote forward was kept: %+v", fs)
	}
}

// socksGet connects through a SOCKS5 proxy to host:port and echoes msg.
func socksGet(t *testing.T, proxy, host string, port uint16, msg string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	req := append([]byte{5, 1, 0, 5, 1, 0, 3, byte(len(host))}, host...)
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 12)
	if _, err := io.ReadFull(c, reply); err != nil || reply[3] != 0 {
		t.Fatalf("SOCKS reply = %v, %v", reply, err)
	}
	if _, err := io.WriteString(c, msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("echo through SOCKS = %q, %v", buf, err)
	}
}

func TestDynamicForwardServesSOCKS(t *testing.T) {
	echo := echoServer(t)
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{Kind: Dynamic, LocalPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	socksGet(t, net.JoinHostPort("127.0.0.1", itoa(f.Spec.LocalPort)), "localhost", echo, "socks")
}

func TestLANSOCKSNeedsOptIn(t *testing.T) {
	e := newEngine(t, dialTo(newFakeConn()))
	connected(t, e, "devbox")
	_, err := e.Forward("devbox", ForwardSpec{Kind: Dynamic, BindAddr: "0.0.0.0", LocalPort: freePort(t)})
	if err == nil || !strings.Contains(err.Error(), "--allow-lan-socks") {
		t.Errorf("err = %v, want a refusal naming --allow-lan-socks", err)
	}
}

func TestPrivilegedPortIsMapped(t *testing.T) {
	echo := echoServer(t)
	o := manualOptions(newFakeConn())
	o.PrivilegedOffset = 10000
	o.listen = func(bind string, port uint16) (net.Listener, error) {
		if port < 1024 {
			return nil, fmt.Errorf("local port %d needs root: %w", port, os.ErrPermission)
		}
		return net.Listen("tcp", net.JoinHostPort(bind, "0"))
	}
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	f, err := e.Forward("devbox", ForwardSpec{LocalPort: 80, RemotePort: echo})
	if err != nil {
		t.Fatal(err)
	}
	if f.Spec.LocalPort != 10080 || f.MappedFrom != 80 {
		t.Errorf("forward = local %d mapped from %d, want 10080 from 80", f.Spec.LocalPort, f.MappedFrom)
	}
}

func TestAllKindsAreRemembered(t *testing.T) {
	dir := t.TempDir()
	c := newFakeConn(listen(22, "sshd"))
	o := manualOptions(c)
	o.State = storeAt(dir)
	e := newEngineWith(t, o)
	connected(t, e, "devbox")
	if _, err := e.Forward("devbox", ForwardSpec{Kind: Remote, RemotePort: freePort(t), LocalPort: 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Forward("devbox", ForwardSpec{Kind: Dynamic, LocalPort: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	saved, err := storeAt(dir).Load("devbox")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, f := range saved {
		kinds[f.Kind] = true
	}
	if !kinds["remote"] || !kinds["dynamic"] {
		t.Errorf("saved = %+v, want a remote and a dynamic forward", saved)
	}
	for _, f := range saved {
		if spec, ok := fromState(f); !ok || spec.Kind.String() != f.Kind {
			t.Errorf("%+v doesn't restore as its kind: %+v %v", f, spec, ok)
		}
	}
}

func TestOwnRemoteListenerIsNotAutoForwarded(t *testing.T) {
	server := freePort(t)
	c := newFakeConn("")
	e := newEngineWith(t, autoOptions(dialTo(c)))
	connected(t, e, "devbox")
	if _, err := e.Forward("devbox", ForwardSpec{Kind: Remote, RemotePort: server, LocalPort: 3000}); err != nil {
		t.Fatal(err)
	}
	// The server now lists pfwd's own -R listener as a new port, without
	// a process name, as ss shows root's sockets to other users.
	line := fmt.Sprintf("LISTEN 0 128 127.0.0.1:%d 0.0.0.0:*\n", server)
	c.send(line)
	c.send(line)
	noEvent(t, e, 300*time.Millisecond, isAutoForwardOf(server))
}
