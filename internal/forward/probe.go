package forward

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"time"
)

// Scheme is what the HTTP probe found behind a forward.
type Scheme int

// Probe results. SchemeUnknown means the probe didn't run or was canceled.
const (
	SchemeUnknown Scheme = iota
	SchemeNone
	SchemeHTTP
	SchemeHTTPS
)

// String returns "http", "https", "none", or "" when unknown.
func (s Scheme) String() string {
	switch s {
	case SchemeNone:
		return "none"
	case SchemeHTTP:
		return "http"
	case SchemeHTTPS:
		return "https"
	default:
		return ""
	}
}

// ProbeOptions configures Probe.
type ProbeOptions struct {
	// Host is sent as the Host header and its name part as the TLS server
	// name, for example "localhost:5173", as a browser would.
	Host string
	// UserAgent identifies pfwd to the server.
	UserAgent string
	// Timeout bounds each attempt; 0 means 1 s.
	Timeout time.Duration
}

// Probe finds out whether target speaks HTTP or HTTPS. It sends one HEAD /
// on a fresh connection; any HTTP response other than 400 means http. After
// no response, or a 400 (what HTTPS servers send to plaintext), it tries a
// TLS handshake with an empty root pool: a handshake that only fails to
// verify the certificate still means https, so nothing is ever trusted and
// no data follows the handshake. Each attempt stops on its own timeout,
// without connection deadlines, because SSH channels have none.
func Probe(ctx context.Context, d Dialer, target string, o ProbeOptions) Scheme {
	if o.Timeout <= 0 {
		o.Timeout = time.Second
	}
	status, ok := probeHEAD(ctx, d, target, o)
	if ctx.Err() != nil {
		return SchemeUnknown
	}
	if ok && status != http.StatusBadRequest {
		return SchemeHTTP
	}
	if probeTLS(ctx, d, target, o) {
		return SchemeHTTPS
	}
	if ctx.Err() != nil {
		return SchemeUnknown
	}
	if ok {
		return SchemeHTTP // a plain HTTP server that answered 400
	}
	return SchemeNone
}

// attempt dials target and closes the connection when the attempt's
// context ends, which is what stops reads on connections without deadlines.
func attempt(ctx context.Context, d Dialer, target string, timeout time.Duration, run func(context.Context, net.Conn)) {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := d.DialContext(actx, "tcp", target)
	if err != nil {
		return
	}
	stop := context.AfterFunc(actx, func() { _ = c.Close() })
	defer func() {
		stop()
		_ = c.Close()
	}()
	run(actx, c)
}

func probeHEAD(ctx context.Context, d Dialer, target string, o ProbeOptions) (status int, ok bool) {
	attempt(ctx, d, target, o.Timeout, func(actx context.Context, c net.Conn) {
		req, err := http.NewRequestWithContext(actx, http.MethodHead, "http://"+o.Host+"/", nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", o.UserAgent)
		req.Close = true
		if err := req.Write(c); err != nil {
			return
		}
		resp, err := http.ReadResponse(bufio.NewReader(c), req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		status, ok = resp.StatusCode, true
	})
	return status, ok
}

func probeTLS(ctx context.Context, d Dialer, target string, o ProbeOptions) (https bool) {
	name, _, err := net.SplitHostPort(o.Host)
	if err != nil {
		name = o.Host
	}
	attempt(ctx, d, target, o.Timeout, func(actx context.Context, c net.Conn) {
		tc := tls.Client(c, &tls.Config{
			ServerName: name,
			MinVersion: tls.VersionTLS12,
			RootCAs:    x509.NewCertPool(), // trust nothing: only the handshake matters
		})
		err := tc.HandshakeContext(actx)
		var unverified *tls.CertificateVerificationError
		https = err == nil || errors.As(err, &unverified)
	})
	return https
}
