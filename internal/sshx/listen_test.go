package sshx

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

func TestConnListenForwardsToClient(t *testing.T) {
	f := newFixture(t, sshtest.Options{AllowRemoteForward: true})
	c := mustDial(t, f.hc, DialOptions{})
	ln, err := c.Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	served := make(chan struct{})
	go func() {
		defer close(served)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	// The test server listens on this machine, so the server side's port
	// is reachable here.
	remote, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the server-side port %s: %v", ln.Addr(), err)
	}
	_ = remote.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := remote.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(remote, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
	remote.Close()
	ln.Close()
	<-served
}

func TestConnListenRefused(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	c := mustDial(t, f.hc, DialOptions{})
	_, err := c.Listen("127.0.0.1", 9000)
	if !errors.Is(err, ErrForwardingRefused) || !strings.Contains(err.Error(), "AllowTcpForwarding") {
		t.Errorf("err = %v, want ErrForwardingRefused with an AllowTcpForwarding hint", err)
	}
	_, err = c.Listen("0.0.0.0", 9000)
	if !errors.Is(err, ErrForwardingRefused) || !strings.Contains(err.Error(), "GatewayPorts") {
		t.Errorf("err = %v, want a GatewayPorts hint for a non-loopback bind", err)
	}
}
