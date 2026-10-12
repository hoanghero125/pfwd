package sshx

import (
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// waitEnds reports whether c.Wait returns within d, and its error.
func waitEnds(c *Conn, d time.Duration) (bool, error) {
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		return true, err
	case <-time.After(d):
		return false, nil
	}
}

func TestKeepaliveClosesSilentConnection(t *testing.T) {
	f := newFixture(t, sshtest.Options{IgnoreKeepalive: true})
	c := mustDial(t, f.hc, DialOptions{Keepalive: Keepalive{Interval: 50 * time.Millisecond, CountMax: 2}})
	ended, err := waitEnds(c, 5*time.Second)
	if !ended {
		t.Fatal("the connection to a server that stopped answering stayed open")
	}
	if err == nil || !strings.Contains(err.Error(), "keepalive") {
		t.Errorf("Wait = %v, want an error about unanswered keepalives", err)
	}
}

func TestKeepaliveKeepsLiveConnection(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	c := mustDial(t, f.hc, DialOptions{Keepalive: Keepalive{Interval: 30 * time.Millisecond, CountMax: 2}})
	if ended, err := waitEnds(c, 400*time.Millisecond); ended {
		t.Fatalf("a server that answers keepalives was dropped: %v", err)
	}
}

func TestKeepaliveNoticesServerGoingSilent(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	c := mustDial(t, f.hc, DialOptions{Keepalive: Keepalive{Interval: 50 * time.Millisecond, CountMax: 2}})
	if ended, err := waitEnds(c, 200*time.Millisecond); ended {
		t.Fatalf("dropped while the server answered: %v", err)
	}
	f.srv.SetIgnoreKeepalive(true)
	if ended, _ := waitEnds(c, 5*time.Second); !ended {
		t.Fatal("the connection stayed open after the server went silent")
	}
}

func TestKeepaliveOffByDefault(t *testing.T) {
	f := newFixture(t, sshtest.Options{IgnoreKeepalive: true})
	c := mustDial(t, f.hc, DialOptions{})
	if ended, err := waitEnds(c, 300*time.Millisecond); ended {
		t.Fatalf("without keepalives the connection must stay open: %v", err)
	}
}

func TestDropConnsEndsConnection(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	c := mustDial(t, f.hc, DialOptions{Keepalive: Keepalive{Interval: time.Hour, CountMax: 3}})
	f.srv.DropConns()
	if ended, _ := waitEnds(c, 5*time.Second); !ended {
		t.Fatal("Wait did not return after the server dropped the connection")
	}
}
