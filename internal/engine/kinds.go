package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/hoanghero125/pfwd/internal/forward"
)

// defaultPrivilegedOffset moves a local port below 1024 that this user
// can't bind: 80 becomes 10080.
const defaultPrivilegedOffset = 10000

// normalize fills a spec's defaults for its kind and checks it.
//
//   - Local (-L): BindAddr and LocalPort are this machine's; RemoteHost and
//     RemotePort are the target as the server sees it.
//   - Remote (-R): BindAddr and RemotePort are where the server listens;
//     RemoteHost and LocalPort are the target as this machine sees it.
//   - Dynamic (-D): a SOCKS5 proxy on BindAddr:LocalPort.
func (e *Engine) normalize(spec ForwardSpec) (ForwardSpec, error) {
	switch spec.Kind {
	case Local:
		if spec.RemotePort == 0 {
			return spec, errors.New("remote port 0 is not a port; choose a port from 1 to 65535")
		}
		spec.BindAddr = cmp.Or(spec.BindAddr, e.o.BindAddr)
		spec.LocalPort = cmp.Or(spec.LocalPort, spec.RemotePort)
		spec.RemoteHost = cmp.Or(spec.RemoteHost, "127.0.0.1")
	case Remote:
		if spec.RemotePort == 0 {
			return spec, errors.New("server port 0 is not a port; choose a port from 1 to 65535")
		}
		spec.BindAddr = cmp.Or(spec.BindAddr, "127.0.0.1")
		spec.LocalPort = cmp.Or(spec.LocalPort, spec.RemotePort)
		spec.RemoteHost = cmp.Or(spec.RemoteHost, "127.0.0.1")
	case Dynamic:
		if spec.LocalPort == 0 {
			return spec, errors.New("a SOCKS proxy needs a local port, such as 1080")
		}
		spec.BindAddr = cmp.Or(spec.BindAddr, e.o.BindAddr)
		if !isLoopback(spec.BindAddr) && !e.o.AllowLANSocks {
			return spec, fmt.Errorf("a SOCKS proxy on %s would let your network reach anything the server can; bind it to 127.0.0.1, or pass --allow-lan-socks", spec.BindAddr)
		}
		spec.RemoteHost, spec.RemotePort = "", 0
	default:
		return spec, fmt.Errorf("unknown forward kind %d", spec.Kind)
	}
	return spec, nil
}

// isLoopback reports whether a bind address only accepts this machine.
func isLoopback(bind string) bool {
	if bind == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(bind)
	return err == nil && ip.IsLoopback()
}

// privilegedOffset is the offset for ports below 1024; 0 turns it off.
func (e *Engine) privilegedOffset() int {
	switch {
	case e.o.PrivilegedOffset < 0:
		return 0
	case e.o.PrivilegedOffset == 0:
		return defaultPrivilegedOffset
	}
	return e.o.PrivilegedOffset
}

// listener opens a forward's listener: on the server for Remote, here for
// Local and Dynamic, where a privileged port may move. port is the local
// port bound (the spec's, or the moved one).
func (e *Engine) listener(conn Conn, spec ForwardSpec) (net.Listener, uint16, error) {
	if spec.Kind == Remote {
		ln, err := conn.Listen(spec.BindAddr, spec.RemotePort)
		return ln, spec.LocalPort, err
	}
	return forward.ListenPrivileged(spec.BindAddr, spec.LocalPort, e.privilegedOffset(), e.o.listen)
}

// newLocal builds what serves a forward's listener. Remote forwards dial
// this machine; the others dial through the host's connection, as d does.
func (e *Engine) newLocal(spec ForwardSpec, ln net.Listener, d *fwdDialer, onDialError func(error)) *forward.Local {
	o := forward.Options{Logger: e.log, MaxConns: e.o.MaxConns, OnDialError: onDialError}
	switch spec.Kind {
	case Remote:
		target := net.JoinHostPort(spec.RemoteHost, itoa(spec.LocalPort))
		return forward.NewLocal(ln, &net.Dialer{Timeout: 10 * time.Second}, target, o)
	case Dynamic:
		o.Handshake = forward.SOCKS5()
		return forward.NewLocal(ln, d, "", o)
	default:
		return forward.NewLocal(ln, d, net.JoinHostPort(spec.RemoteHost, itoa(spec.RemotePort)), o)
	}
}

// serveRemote runs a remote forward. The server's listener goes away with
// the connection; after a reconnect the forward listens again on the new
// one, keeping its ID and adding up its counters. A server that refuses
// the new listen fails the forward.
func (e *Engine) serveRemote(ctx context.Context, f *fwd) {
	defer e.wg.Done()
	defer close(f.done)
	for {
		e.mu.Lock()
		local := f.local
		e.mu.Unlock()
		err := local.Serve(ctx)
		if ctx.Err() != nil {
			return
		}
		e.log.Info("remote forward's listener ended", "id", f.ID, "err", err)
		conn, err := e.waitConnected(ctx, f.Host)
		if err != nil {
			return
		}
		ln, _, err := e.listener(conn, f.Spec)
		if err != nil {
			e.log.Info("remote forward failed to listen again", "id", f.ID, "err", err)
			e.mu.Lock()
			f.State, f.Err = Failed, err
			e.mu.Unlock()
			e.emit(ForwardChanged{Forward: e.snapshot(f)})
			return
		}
		next := e.newLocal(f.Spec, ln, nil, func(err error) { e.dialFailed(f, err) })
		e.mu.Lock()
		f.base = addStats(f.base, f.local.Stats())
		f.local = next
		e.mu.Unlock()
	}
}

// waitConnected returns the host's connection once it is up, or ctx's
// error.
func (e *Engine) waitConnected(ctx context.Context, host HostID) (Conn, error) {
	for {
		e.mu.Lock()
		hs := e.hostLocked(host)
		if s := e.sessions[host]; s != nil && s.state == Connected && s.conn != nil {
			c := s.conn
			e.mu.Unlock()
			return c, nil
		}
		ready := hs.ready
		e.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// addStats adds counters from a listener that ended to the current ones.
func addStats(base, s forward.Stats) forward.Stats {
	base.TotalConns += s.TotalConns
	base.BytesIn += s.BytesIn
	base.BytesOut += s.BytesOut
	if s.LastActive.After(base.LastActive) {
		base.LastActive = s.LastActive
	}
	return base
}
