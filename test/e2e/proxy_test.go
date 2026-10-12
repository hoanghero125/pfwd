//go:build e2e

package e2e

import (
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// dropProxy forwards TCP connections to a target, and can cut them all
// and refuse new ones for a while, as a network failure would.
type dropProxy struct {
	ln     net.Listener
	target string
	wg     sync.WaitGroup

	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	downUntil time.Time
}

// startProxy starts a proxy to target on a free local port; it stops when
// the test ends.
func startProxy(t *testing.T, target string) *dropProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &dropProxy{ln: ln, target: target, conns: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.accept()
	t.Cleanup(p.close)
	return p
}

// port is the proxy's local port.
func (p *dropProxy) port() int {
	return int(netip.MustParseAddrPort(p.ln.Addr().String()).Port())
}

// drop closes every proxied connection, and closes new ones at once for
// outage.
func (p *dropProxy) drop(outage time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downUntil = time.Now().Add(outage)
	for c := range p.conns {
		c.Close()
	}
}

func (p *dropProxy) close() {
	p.ln.Close()
	p.drop(0)
	p.wg.Wait()
}

func (p *dropProxy) accept() {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		down := time.Now().Before(p.downUntil)
		p.mu.Unlock()
		if down {
			c.Close()
			continue
		}
		p.wg.Add(1)
		go p.pipe(c)
	}
}

func (p *dropProxy) pipe(client net.Conn) {
	defer p.wg.Done()
	server, err := net.DialTimeout("tcp", p.target, 5*time.Second)
	if err != nil {
		client.Close()
		return
	}
	if !p.track(client, server) {
		client.Close()
		server.Close()
		return
	}
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(server, client)
		server.Close()
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(client, server)
		client.Close()
	}()
	copies.Wait()
	p.mu.Lock()
	delete(p.conns, client)
	delete(p.conns, server)
	p.mu.Unlock()
}

// track records both ends so drop can close them; false during an outage.
func (p *dropProxy) track(cs ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.downUntil) {
		return false
	}
	for _, c := range cs {
		p.conns[c] = struct{}{}
	}
	return true
}
