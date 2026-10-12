package forward

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// recordDialer dials target instead of what it is asked for, and records
// the requested addresses; err, when set, is returned instead.
type recordDialer struct {
	target string
	err    error
	mu     sync.Mutex
	asked  []string
}

func (d *recordDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.asked = append(d.asked, addr)
	d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	var nd net.Dialer
	return nd.DialContext(ctx, network, d.target)
}

func (d *recordDialer) addrs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.asked...)
}

// socksRequest is a CONNECT request for one address type.
func socksRequest(atyp byte, addr []byte, port uint16) []byte {
	b := []byte{5, 1, 0, atyp}
	if atyp == 3 {
		b = append(b, byte(len(addr)))
	}
	b = append(b, addr...)
	return binary.BigEndian.AppendUint16(b, port)
}

// socksConnect greets the proxy, sends req, and returns the reply code.
func socksConnect(t *testing.T, proxy string, req []byte) (*net.TCPConn, byte) {
	t.Helper()
	c := dialTCP(t, proxy)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	choice := make([]byte, 2)
	if _, err := io.ReadFull(c, choice); err != nil || !bytes.Equal(choice, []byte{5, 0}) {
		t.Fatalf("method choice = %v, %v", choice, err)
	}
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatalf("read the reply: %v", err)
	}
	return c, reply[1]
}

func TestSOCKSConnect(t *testing.T) {
	echo := echoServer(t)
	for _, tc := range []struct {
		name string
		req  []byte
		want string
	}{
		{"ipv4", socksRequest(1, []byte{10, 0, 0, 5}, 8080), "10.0.0.5:8080"},
		{"domain", socksRequest(3, []byte("db.internal"), 5432), "db.internal:5432"},
		{"ipv6", socksRequest(4, net.ParseIP("::1").To16(), 22), "[::1]:22"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &recordDialer{target: echo}
			_, addr, _, _ := serve(t, "", d, Options{Handshake: SOCKS5()})
			c, code := socksConnect(t, addr, tc.req)
			if code != 0 {
				t.Fatalf("reply code %#x, want success", code)
			}
			if _, err := c.Write([]byte("ping")); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 4)
			if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
				t.Fatalf("echo = %q, %v", buf, err)
			}
			if got := d.addrs(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("dialed %q, want %q (unresolved)", got, tc.want)
			}
		})
	}
}

func TestSOCKSReplyCodes(t *testing.T) {
	refused := &ssh.OpenChannelError{Reason: ssh.Prohibited, Message: "administratively prohibited"}
	connRefused := &ssh.OpenChannelError{Reason: ssh.ConnectionFailed, Message: "Connection refused"}
	for _, tc := range []struct {
		name string
		err  error
		req  []byte
		want byte
	}{
		{"server forbids forwarding", fmt.Errorf("devbox refuses TCP forwarding: %w", refused), socksRequest(1, []byte{127, 0, 0, 1}, 80), 0x02},
		{"connection refused", fmt.Errorf("could not connect: %w", connRefused), socksRequest(1, []byte{127, 0, 0, 1}, 80), 0x05},
		{"other failure", errors.New("boom"), socksRequest(1, []byte{127, 0, 0, 1}, 80), 0x01},
		{"bind is not supported", nil, []byte{5, 2, 0, 1, 127, 0, 0, 1, 0, 80}, 0x07},
		{"unknown address type", nil, []byte{5, 1, 0, 9, 0, 0}, 0x08},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &recordDialer{err: tc.err}
			_, addr, _, _ := serve(t, "", d, Options{Handshake: SOCKS5()})
			if _, code := socksConnect(t, addr, tc.req); code != tc.want {
				t.Errorf("reply code %#x, want %#x", code, tc.want)
			}
		})
	}
}

func TestSOCKSRejectsOtherVersions(t *testing.T) {
	_, addr, _, _ := serve(t, "", &recordDialer{}, Options{Handshake: SOCKS5()})
	c := dialTCP(t, addr)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte{4, 1, 0, 80, 127, 0, 0, 1, 0})
	// The proxy closes the connection (EOF, or a reset where unread bytes
	// remain); a timeout would mean it is still waiting.
	if _, err := io.ReadAll(c); errors.Is(err, os.ErrDeadlineExceeded) {
		t.Error("the proxy kept a SOCKS4 client's connection open")
	}
}

func TestSOCKSHandshakeTimesOut(t *testing.T) {
	_, addr, _, _ := serve(t, "", &recordDialer{}, Options{Handshake: SOCKS5(), HandshakeTimeout: 200 * time.Millisecond})
	c := dialTCP(t, addr)
	_, _ = c.Write([]byte{5}) // and nothing more
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, _ = io.ReadAll(c)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("a stalled handshake held the connection %v", took)
	}
}

func FuzzSOCKSHandshake(f *testing.F) {
	f.Add([]byte{5, 1, 0, 5, 1, 0, 1, 127, 0, 0, 1, 0, 80})
	f.Add([]byte{5, 1, 0, 5, 1, 0, 3, 3, 'a', 'b', 'c', 0, 80})
	f.Add([]byte{5, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			_, _ = client.Write(data)
			_, _ = io.Copy(io.Discard, client)
		}()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		target, reply, err := SOCKS5()(server)
		if err == nil {
			if _, _, serr := net.SplitHostPort(target); serr != nil {
				t.Fatalf("handshake gave target %q", target)
			}
			_ = reply(nil)
		}
		server.Close()
	})
}

// fakeListen refuses ports below 1024 the way Unix refuses non-root users.
func fakeListen(bind string, port uint16) (net.Listener, error) {
	if port < 1024 {
		return nil, fmt.Errorf("local port %d needs root: %w", port, os.ErrPermission)
	}
	return net.Listen("tcp", net.JoinHostPort(bind, "0"))
}

func TestListenPrivileged(t *testing.T) {
	ln, port, err := ListenPrivileged("127.0.0.1", 80, 10000, fakeListen)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if port != 10080 {
		t.Errorf("port = %d, want 10080", port)
	}
	ln, port, err = ListenPrivileged("127.0.0.1", 8080, 10000, fakeListen)
	if err != nil || port != 8080 {
		t.Errorf("an unprivileged port moved to %d, %v", port, err)
	}
	if ln != nil {
		ln.Close()
	}
	if _, _, err := ListenPrivileged("127.0.0.1", 80, 0, fakeListen); err == nil {
		t.Error("offset 0 still mapped the port")
	}
}

// slowDialer dials like recordDialer after a delay, as a dial that waits
// for a reconnect does.
type slowDialer struct {
	recordDialer
	delay time.Duration
}

func (d *slowDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	time.Sleep(d.delay)
	return d.recordDialer.DialContext(ctx, network, addr)
}

// TestSOCKSReplyAfterSlowDial: a dial that takes longer than the
// handshake timeout still sends its reply and pipes.
func TestSOCKSReplyAfterSlowDial(t *testing.T) {
	echo := echoServer(t)
	d := &slowDialer{recordDialer: recordDialer{target: echo}, delay: 400 * time.Millisecond}
	_, addr, _, _ := serve(t, "", d, Options{Handshake: SOCKS5(), HandshakeTimeout: 100 * time.Millisecond})
	c, code := socksConnect(t, addr, socksRequest(3, []byte("db.internal"), 5432))
	if code != 0 {
		t.Fatalf("reply code %#x, want success", code)
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
}
