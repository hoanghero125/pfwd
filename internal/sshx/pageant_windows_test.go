package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh/agent"
)

// keyringExchange answers Pageant exchanges with an in-memory agent, the
// way Pageant answers WM_COPYDATA requests.
func keyringExchange(t *testing.T) func([]byte) ([]byte, error) {
	t.Helper()
	keyring := agent.NewKeyring()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv, Comment: "test key"}); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = agent.ServeAgent(keyring, server)
	}()
	t.Cleanup(func() {
		client.Close()
		server.Close()
		wg.Wait()
	})
	return func(req []byte) ([]byte, error) {
		if _, err := client.Write(req); err != nil {
			return nil, err
		}
		head := make([]byte, 4)
		if _, err := io.ReadFull(client, head); err != nil {
			return nil, err
		}
		body := make([]byte, binary.BigEndian.Uint32(head))
		if _, err := io.ReadFull(client, body); err != nil {
			return nil, err
		}
		return append(head, body...), nil
	}
}

func TestPageantConnFraming(t *testing.T) {
	conn := newPageantConn(keyringExchange(t))
	defer conn.Close()
	c := agent.NewClient(conn)
	keys, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Comment != "test key" {
		t.Fatalf("List = %+v, want the one test key", keys)
	}
	signers, err := c.Signers()
	if err != nil || len(signers) != 1 {
		t.Fatalf("Signers = %d, %v", len(signers), err)
	}
	if _, err := signers[0].Sign(rand.Reader, []byte("data")); err != nil {
		t.Errorf("Sign through Pageant framing: %v", err)
	}
}

func TestPageantConnRejectsOversizedMessage(t *testing.T) {
	p := newPageantConn(func([]byte) ([]byte, error) { return nil, nil })
	msg := make([]byte, 4)
	binary.BigEndian.PutUint32(msg, pageantMaxMsg)
	if _, err := p.Write(msg); err == nil {
		t.Error("a request larger than Pageant's buffer was accepted")
	}
}

func TestPageantNotRunning(t *testing.T) {
	if pageantRunning() {
		t.Skip("Pageant is running on this machine")
	}
	if _, err := openPageant(); err == nil {
		t.Error("openPageant succeeded without Pageant")
	}
}
