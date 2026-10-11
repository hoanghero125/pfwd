package forward

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// echoServer echoes each connection until the client half-closes, then
// closes it.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

// holdServer accepts connections, reports each on accepted and keeps them
// open, without answering, until the client closes its side.
func holdServer(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 16)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			accepted <- c
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = io.Copy(io.Discard, c)
				c.Close()
			}()
		}
	}()
	return ln.Addr().String(), accepted
}

func serve(t *testing.T, target string, d Dialer, o Options) (*Local, string, context.CancelFunc, <-chan error) {
	t.Helper()
	ln, err := Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	l := NewLocal(ln, d, target, o)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- l.Serve(ctx)
		close(done) // the test body may consume the result first
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return l, ln.Addr().String(), cancel, done
}

func dialTCP(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	raddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTCP("tcp", nil, raddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLocalRoundTrip1MB(t *testing.T) {
	l, addr, _, _ := serve(t, echoServer(t), &net.Dialer{}, Options{})
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	c := dialTCP(t, addr)
	go func() {
		_, _ = c.Write(payload)
		_ = c.CloseWrite()
	}()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echoed %d bytes, not byte-exact with the %d sent", len(got), len(payload))
	}
	c.Close()
	eventually(t, "the connection to close", func() bool { return l.Stats().Conns == 0 })
	s := l.Stats()
	if s.BytesOut != 1<<20 || s.BytesIn != 1<<20 || s.TotalConns != 1 {
		t.Errorf("stats = %+v, want 1 MiB each way and 1 connection", s)
	}
	if time.Since(s.LastActive) > 10*time.Second {
		t.Errorf("LastActive = %v", s.LastActive)
	}
}

func TestLocalHalfClose(t *testing.T) {
	_, addr, _, _ := serve(t, echoServer(t), &net.Dialer{}, Options{})
	c := dialTCP(t, addr)
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	// The echo server only closes after it sees EOF, so reading to EOF
	// proves the half-close crossed the forward.
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "ping" {
		t.Fatalf("ReadAll = %q, %v; want ping then EOF", got, err)
	}
}

func TestLocalMaxConns(t *testing.T) {
	target, accepted := holdServer(t)
	l, addr, _, _ := serve(t, target, &net.Dialer{}, Options{MaxConns: 2})
	var clients []net.Conn
	for range 3 {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		clients = append(clients, c)
	}
	for i := range 2 {
		select {
		case <-accepted:
		case <-time.After(5 * time.Second):
			t.Fatalf("connection %d never reached the target", i+1)
		}
	}
	select {
	case <-accepted:
		t.Fatal("a third connection was forwarded past MaxConns 2")
	case <-time.After(300 * time.Millisecond):
	}
	if n := l.Stats().Conns; n != 2 {
		t.Errorf("Conns = %d, want 2", n)
	}
	clients[0].Close()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting connection was not forwarded after a slot freed")
	}
}

type failDialer struct{}

func (failDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("administratively prohibited")
}

func TestLocalDialFailureClosesClient(t *testing.T) {
	l, addr, _, _ := serve(t, "127.0.0.1:9", failDialer{}, Options{})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatalf("read %d bytes from a forward whose dial failed", n)
	}
	eventually(t, "the failed connection to be released", func() bool { return l.Stats().Conns == 0 })
	if got := l.Stats().TotalConns; got != 1 {
		t.Errorf("TotalConns = %d, want 1", got)
	}
}

func TestServeStopsOnCancel(t *testing.T) {
	target, accepted := holdServer(t)
	_, addr, cancel, done := serve(t, target, &net.Dialer{}, Options{})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-accepted
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v, want nil after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("open connection survived Serve returning")
	}
	if c2, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c2.Close()
		t.Error("listener still accepts after Serve returned")
	}
}
