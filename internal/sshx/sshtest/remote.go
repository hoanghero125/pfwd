package sshtest

import (
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// remoteForwards are one client connection's tcpip-forward listeners, by
// port. The server owns the goroutines that serve them.
type remoteForwards struct {
	mu  sync.Mutex
	lns map[uint32]net.Listener
}

// handleRemoteForward answers a tcpip-forward request: it listens on
// 127.0.0.1 (whatever bind address was asked for) and opens a
// forwarded-tcpip channel back to the client for every connection.
func (s *Server) handleRemoteForward(sconn *ssh.ServerConn, req *ssh.Request, fwds *remoteForwards) {
	var p struct {
		Addr string
		Port uint32
	}
	if !s.opts.AllowRemoteForward || ssh.Unmarshal(req.Payload, &p) != nil {
		_ = req.Reply(false, nil)
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(p.Port))))
	if err != nil {
		_ = req.Reply(false, nil)
		return
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close()
		_ = req.Reply(false, nil)
		return
	}
	port := uint32(tcp.Port) //nolint:gosec // a TCP port
	fwds.mu.Lock()
	fwds.lns[port] = ln
	fwds.mu.Unlock()
	var reply []byte
	if p.Port == 0 {
		reply = ssh.Marshal(struct{ Port uint32 }{port})
	}
	_ = req.Reply(true, reply)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go s.forwardToClient(sconn, c, p.Addr, port)
		}
	}()
}

// cancelRemoteForward answers cancel-tcpip-forward.
func cancelRemoteForward(req *ssh.Request, fwds *remoteForwards) {
	var p struct {
		Addr string
		Port uint32
	}
	if ssh.Unmarshal(req.Payload, &p) != nil {
		_ = req.Reply(false, nil)
		return
	}
	fwds.mu.Lock()
	ln, ok := fwds.lns[p.Port]
	delete(fwds.lns, p.Port)
	fwds.mu.Unlock()
	if ok {
		ln.Close()
	}
	_ = req.Reply(ok, nil)
}

// closeAll stops every listener of a connection that ended.
func (f *remoteForwards) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for port, ln := range f.lns {
		ln.Close()
		delete(f.lns, port)
	}
}

// forwardToClient opens a forwarded-tcpip channel for c and pipes it.
func (s *Server) forwardToClient(sconn *ssh.ServerConn, c net.Conn, addr string, port uint32) {
	defer s.wg.Done()
	defer c.Close()
	origin, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return
	}
	payload := ssh.Marshal(struct {
		Addr       string
		Port       uint32
		OriginAddr string
		OriginPort uint32
	}{addr, port, origin.IP.String(), uint32(origin.Port)}) //nolint:gosec // a TCP port
	ch, reqs, err := sconn.OpenChannel("forwarded-tcpip", payload)
	if err != nil {
		return
	}
	defer ch.Close()
	discarded := make(chan struct{})
	go func() {
		defer close(discarded)
		ssh.DiscardRequests(reqs)
	}()
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(ch, c)
		_ = ch.CloseWrite()
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(c, ch)
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	copies.Wait()
	ch.Close()
	<-discarded
}
