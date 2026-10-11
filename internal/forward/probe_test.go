package forward

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var probeOpts = ProbeOptions{Host: "localhost:8000", UserAgent: "pfwd-test", Timeout: 300 * time.Millisecond}

func TestProbeHTTP(t *testing.T) {
	var gotUA, gotMethod, gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotMethod, gotHost = r.UserAgent(), r.Method, r.Host
		w.WriteHeader(http.StatusNotFound) // any HTTP response counts
	}))
	defer srv.Close()
	if s := Probe(t.Context(), &net.Dialer{}, srv.Listener.Addr().String(), probeOpts); s != SchemeHTTP {
		t.Errorf("Probe = %v, want http", s)
	}
	if gotMethod != http.MethodHead || gotUA != "pfwd-test" || gotHost != "localhost:8000" {
		t.Errorf("request: method %q, user agent %q, host %q", gotMethod, gotUA, gotHost)
	}
}

func TestProbeHTTPS(t *testing.T) {
	// Go's HTTPS server answers plaintext with a 400, then the TLS check runs.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if s := Probe(t.Context(), &net.Dialer{}, srv.Listener.Addr().String(), probeOpts); s != SchemeHTTPS {
		t.Errorf("Probe = %v, want https", s)
	}
}

// rawServer accepts connections and hands each to handle until the test ends.
func rawServer(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				handle(c)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestProbeTLSThatDropsPlaintext(t *testing.T) {
	cert := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cfg := cert.TLS.Clone()
	cert.Close()
	addr := rawServer(t, func(c net.Conn) {
		tc := tls.Server(c, cfg)
		_ = tc.Handshake() // a plaintext request fails here and the connection closes
		_, _ = io.Copy(io.Discard, tc)
	})
	if s := Probe(t.Context(), &net.Dialer{}, addr, probeOpts); s != SchemeHTTPS {
		t.Errorf("Probe = %v, want https", s)
	}
}

func TestProbeServerThatSpeaksFirst(t *testing.T) {
	addr := rawServer(t, func(c net.Conn) {
		_, _ = io.WriteString(c, "SSH-2.0-OpenSSH_9.6\r\n")
		_, _ = io.Copy(io.Discard, c)
	})
	if s := Probe(t.Context(), &net.Dialer{}, addr, probeOpts); s != SchemeNone {
		t.Errorf("Probe = %v, want none", s)
	}
}

// TestProbeSlowHTTPServer: a dev server compiling its first page answers
// HEAD after the timeout, but its reply to the TLS hello is plain HTTP.
func TestProbeSlowHTTPServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(2 * probeOpts.Timeout)
	}))
	defer srv.Close()
	if s := Probe(t.Context(), &net.Dialer{}, srv.Listener.Addr().String(), probeOpts); s != SchemeHTTP {
		t.Errorf("Probe = %v, want http", s)
	}
}

// TestProbeSilentServer: no answer at all is no proof of anything, so the
// scheme stays unknown rather than "not HTTP".
func TestProbeSilentServer(t *testing.T) {
	addr := rawServer(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	start := time.Now()
	if s := Probe(t.Context(), &net.Dialer{}, addr, probeOpts); s != SchemeUnknown {
		t.Errorf("Probe = %v, want unknown", s)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Probe took %v, want two timeouts at most", d)
	}
}

func TestProbeClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if s := Probe(t.Context(), &net.Dialer{}, addr, probeOpts); s != SchemeNone {
		t.Errorf("Probe = %v, want none", s)
	}
}

func TestProbeCanceled(t *testing.T) {
	addr := rawServer(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if s := Probe(ctx, &net.Dialer{}, addr, probeOpts); s != SchemeUnknown {
		t.Errorf("Probe = %v, want unknown after cancel", s)
	}
}

// noDeadlineDialer returns connections whose deadlines fail, like SSH
// channels, so Probe must stop on its context alone.
type noDeadlineDialer struct{ net.Dialer }

type noDeadlineConn struct{ net.Conn }

func (noDeadlineConn) SetDeadline(time.Time) error      { return errors.New("deadline not supported") }
func (noDeadlineConn) SetReadDeadline(time.Time) error  { return errors.New("deadline not supported") }
func (noDeadlineConn) SetWriteDeadline(time.Time) error { return errors.New("deadline not supported") }

func (d *noDeadlineDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := d.Dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return noDeadlineConn{c}, nil
}

func TestProbeWithoutDeadlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if s := Probe(t.Context(), &noDeadlineDialer{}, srv.Listener.Addr().String(), probeOpts); s != SchemeHTTP {
		t.Errorf("Probe = %v, want http", s)
	}
	silent := rawServer(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	if s := Probe(t.Context(), &noDeadlineDialer{}, silent, probeOpts); s != SchemeUnknown {
		t.Errorf("Probe of a silent server without deadlines = %v, want unknown", s)
	}
}

func TestSchemeString(t *testing.T) {
	for s, want := range map[Scheme]string{SchemeUnknown: "", SchemeNone: "none", SchemeHTTP: "http", SchemeHTTPS: "https"} {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}
