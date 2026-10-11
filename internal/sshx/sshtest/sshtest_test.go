package sshtest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"
)

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func dial(t *testing.T, srv *Server, key ssh.Signer) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(key)},
		HostKeyCallback: ssh.FixedHostKey(srv.HostKey.PublicKey()),
		Timeout:         5 * time.Second,
	})
}

// echoServer accepts connections and writes back what it reads, until the test ends.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		ln.Close()
		<-done
	})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.Copy(c, c)
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestExecRunsHandler(t *testing.T) {
	key := newSigner(t)
	srv := Start(t, Options{
		AuthorizedKeys: []ssh.PublicKey{key.PublicKey()},
		Exec: func(_ context.Context, cmd string, stdout io.Writer) int {
			fmt.Fprintf(stdout, "ran %q\n", cmd)
			return 0
		},
	})
	client, err := dial(t, srv, key)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := sess.Output("uname -s")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got, want := string(out), "ran \"uname -s\"\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestExecExitStatus(t *testing.T) {
	key := newSigner(t)
	srv := Start(t, Options{
		AuthorizedKeys: []ssh.PublicKey{key.PublicKey()},
		Exec:           func(context.Context, string, io.Writer) int { return 3 },
	})
	client, err := dial(t, srv, key)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var exitErr *ssh.ExitError
	if err := sess.Run("false"); !errors.As(err, &exitErr) || exitErr.ExitStatus() != 3 {
		t.Fatalf("Run = %v, want exit status 3", err)
	}
}

func TestExecContextEndsWhenSessionCloses(t *testing.T) {
	key := newSigner(t)
	ended := make(chan struct{})
	srv := Start(t, Options{
		AuthorizedKeys: []ssh.PublicKey{key.PublicKey()},
		Exec: func(ctx context.Context, _ string, stdout io.Writer) int {
			_, _ = io.WriteString(stdout, "started\n")
			<-ctx.Done()
			close(ended)
			return 0
		},
	})
	client, err := dial(t, srv, key)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Start("loop"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("started\n"))
	if _, err := io.ReadFull(stdout, buf); err != nil {
		t.Fatal(err)
	}
	sess.Close()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("handler context was not canceled after the session closed")
	}
}

func TestDirectTCPIPPipes(t *testing.T) {
	key := newSigner(t)
	target := echoServer(t)
	srv := Start(t, Options{AuthorizedKeys: []ssh.PublicKey{key.PublicKey()}})
	client, err := dial(t, srv, key)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := client.Dial("tcp", target)
	if err != nil {
		t.Fatalf("Dial through ssh: %v", err)
	}
	defer conn.Close()
	msg := []byte("hello through the tunnel")
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Errorf("echo = %q, want %q", got, msg)
	}
}

func TestRejectsUnknownKey(t *testing.T) {
	srv := Start(t, Options{AuthorizedKeys: []ssh.PublicKey{newSigner(t).PublicKey()}})
	client, err := dial(t, srv, newSigner(t))
	if err == nil {
		client.Close()
		t.Fatal("dial with an unauthorized key succeeded")
	}
}

func TestPasswordAuth(t *testing.T) {
	srv := Start(t, Options{Password: "s3cret"})
	client, err := ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.Password("s3cret")},
		HostKeyCallback: ssh.FixedHostKey(srv.HostKey.PublicKey()),
	})
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
}

func TestDenyTCPForwarding(t *testing.T) {
	key := newSigner(t)
	target := echoServer(t)
	srv := Start(t, Options{AuthorizedKeys: []ssh.PublicKey{key.PublicKey()}, DenyTCPForwarding: true})
	client, err := dial(t, srv, key)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if conn, err := client.Dial("tcp", target); err == nil {
		conn.Close()
		t.Fatal("direct-tcpip succeeded with DenyTCPForwarding")
	}
}

func TestAddressHelpers(t *testing.T) {
	srv := Start(t, Options{})
	if srv.Host() != "127.0.0.1" {
		t.Errorf("Host = %q", srv.Host())
	}
	if want := net.JoinHostPort(srv.Host(), strconv.Itoa(srv.Port())); want != srv.Addr {
		t.Errorf("Addr = %q, want %q", srv.Addr, want)
	}
	line := srv.KnownHostsLine()
	if want := "[127.0.0.1]:" + strconv.Itoa(srv.Port()) + " ssh-ed25519 "; len(line) < len(want) || line[:len(want)] != want {
		t.Errorf("KnownHostsLine = %q, want prefix %q", line, want)
	}
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
