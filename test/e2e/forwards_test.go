//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/tui"
)

// freePort returns a local port that was free a moment ago.
func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := netip.MustParseAddrPort(ln.Addr().String()).Port()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// localPage serves a page on this machine until the test ends, and
// returns its port.
func localPage(t *testing.T, body string) uint16 {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}
	return uint16(port)
}

// wgetOnServer fetches a URL inside the container, retrying for up to
// timeout, and returns the last output and error.
func wgetOnServer(service, u string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		out, err := compose("exec", "-T", service, "wget", "-qO-", "-T", "3", u)
		if err == nil || time.Now().After(deadline) {
			return string(out), err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestE2ESOCKS: a SOCKS proxy through the server; a request for
// localhost:8130 through it is resolved on the server and reaches the
// server's own HTTP server.
func TestE2ESOCKS(t *testing.T) {
	eng := manualEngine(dialer(newHost(t, "ss")))
	defer eng.Close()
	host := engine.HostID("ss")
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)
	d.waitFor(t, "ignored hidden", 20*time.Second)
	startServer(t, "ss", "8130")
	d.waitFor(t, ":8130", 5*time.Second)

	port := freePort(t)
	f, err := eng.Forward(host, engine.ForwardSpec{Kind: engine.Dynamic, LocalPort: port})
	if err != nil {
		t.Fatalf("start the SOCKS proxy: %v", err)
	}
	d.waitFor(t, "SOCKS", 5*time.Second)
	proxy := &url.URL{Scheme: "socks5", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(f.Spec.LocalPort)))}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	resp, err := client.Get("http://localhost:8130/")
	if err != nil {
		t.Fatalf("GET through the SOCKS proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET through the SOCKS proxy: %s", resp.Status)
	}
	if resp, err := client.Get("http://localhost:8199/"); err == nil {
		resp.Body.Close()
		t.Error("a closed port on the server answered through the SOCKS proxy")
	}
}

// TestE2ERemoteForward: a page on this machine, exposed as port 9100 on
// the server, answers a request made on the server.
func TestE2ERemoteForward(t *testing.T) {
	eng := manualEngine(dialer(newHost(t, "ss")))
	defer eng.Close()
	host := engine.HostID("ss")
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)
	d.waitFor(t, "ignored hidden", 20*time.Second)

	local := localPage(t, "hello from the laptop")
	if _, err := eng.Forward(host, engine.ForwardSpec{Kind: engine.Remote, RemotePort: 9100, LocalPort: local}); err != nil {
		t.Fatalf("expose the page: %v", err)
	}
	d.waitFor(t, "rev", 5*time.Second)
	out, err := wgetOnServer("ss", "http://127.0.0.1:9100/", 5*time.Second)
	if err != nil || !strings.Contains(out, "hello from the laptop") {
		t.Fatalf("wget on the server: %v\n%s", err, out)
	}
	// pfwd's own listener shows up in the server's ports but is not
	// offered as a port to forward back.
	d.runFor(3 * time.Second)
	if _, rest, ok := strings.Cut(d.view(), "not forwarded"); ok && strings.Contains(rest, ":9100") {
		t.Errorf("the server's :9100, pfwd's own listener, is listed as a port to forward:\n%s", d.view())
	}
}

// TestE2ERemoteForwardSurvivesReconnect: the connection drops; after the
// reconnect the server-side port works again.
func TestE2ERemoteForwardSurvivesReconnect(t *testing.T) {
	proxy, dial := proxiedHost(t, "ss")
	eng := manualEngine(dial)
	defer eng.Close()
	host := engine.HostID("ss")
	d := newDriver(tui.New(eng, host, tui.Options{}))
	eng.Connect(host)
	d.waitFor(t, "ignored hidden", 20*time.Second)

	local := localPage(t, "still here")
	if _, err := eng.Forward(host, engine.ForwardSpec{Kind: engine.Remote, RemotePort: 9101, LocalPort: local}); err != nil {
		t.Fatalf("expose the page: %v", err)
	}
	if out, err := wgetOnServer("ss", "http://127.0.0.1:9101/", 5*time.Second); err != nil || !strings.Contains(out, "still here") {
		t.Fatalf("wget on the server before the drop: %v\n%s", err, out)
	}

	proxy.drop(3 * time.Second)
	d.waitFor(t, "reconnecting", 10*time.Second)
	d.waitFor(t, "Reconnected to ss", 30*time.Second)
	waitState(t, d, eng, 9101, engine.Active, 10*time.Second)
	out, err := wgetOnServer("ss", "http://127.0.0.1:9101/", 10*time.Second)
	if err != nil || !strings.Contains(out, "still here") {
		f, _ := forwardOf(eng, 9101)
		t.Fatalf("wget on the server after the reconnect: %v\n%s\nforward: %+v", err, out, f)
	}
}
