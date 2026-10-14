// Package web serves pfwd's web UI from the daemon: the embedded app, a
// JSON API and a WebSocket of live events. It listens on 127.0.0.1 only.
// A one-time login URL gives the browser a session cookie; every API call
// also carries the session's token, which the page receives once and
// keeps in sessionStorage, so the cookie alone (which browsers send to
// every port of 127.0.0.1) opens nothing. Host, Origin and Sec-Fetch-Site
// are checked on every request to block DNS rebinding and other sites.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hoanghero125/pfwd/internal/ipc"
)

// Backend is what the web UI drives: the daemon's IPC handler.
type Backend interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
	Watch(ctx context.Context, send func(ipc.Event) error) error
}

// Options configures a Server.
type Options struct {
	// Assets is the built web app, with index.html at its root.
	Assets fs.FS
	// Version is reported by /api/session.
	Version string
	Logger  *slog.Logger
	// Now is the clock; StatsEvery how often the event stream carries the
	// forwards' counters (default 1s).
	Now        func() time.Time
	StatsEvery time.Duration
}

// Server is the web UI's HTTP server.
type Server struct {
	b   Backend
	o   Options
	mux *http.ServeMux

	mu       sync.Mutex
	port     int
	logins   map[[32]byte]time.Time
	sessions map[[32]byte]*session
	// streams counts the event streams Serve waits for; closing stops new
	// ones once it waits.
	streams sync.WaitGroup
	closing bool
}

// csp allows nothing but the app's own files and its API.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// shutdownWait bounds how long Serve waits for requests after ctx ends.
const shutdownWait = 3 * time.Second

// New returns a server for b.
func New(b Backend, o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.StatsEvery == 0 {
		o.StatsEvery = time.Second
	}
	s := &Server{b: b, o: o, logins: map[[32]byte]time.Time{}, sessions: map[[32]byte]*session{}}
	s.mux = s.routes()
	return s
}

// Listen opens the server's port on 127.0.0.1; port 0 picks a free one.
func (s *Server) Listen(port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d: use a number from 1 to 65535, or 0 for any", port)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w; pick another port with --port", port, err)
	}
	s.mu.Lock()
	s.port = ln.Addr().(*net.TCPAddr).Port //nolint:errcheck,forcetypeassert // a tcp listener's address is a *net.TCPAddr
	s.mu.Unlock()
	return ln, nil
}

// Addr is the server's address, such as http://127.0.0.1:41234.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "http://127.0.0.1:" + strconv.Itoa(s.port)
}

// Serve serves ln until ctx ends, then shuts down within 3 seconds and
// waits for the event streams.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(s.o.Logger.Handler(), slog.LevelDebug),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	var err error
	select {
	case err = <-errc:
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownWait)
		err = srv.Shutdown(sctx)
		cancel()
		<-errc
	}
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.streams.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// ServeHTTP checks where a request comes from, then routes it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	s.mu.Lock()
	port := strconv.Itoa(s.port)
	s.mu.Unlock()
	if r.Host != "127.0.0.1:"+port && r.Host != "localhost:"+port {
		http.Error(w, "pfwd serves its web UI on 127.0.0.1:"+port+" only", http.StatusMisdirectedRequest)
		return
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "none", "same-origin":
	default:
		http.Error(w, "pfwd's web UI doesn't answer requests from other sites", http.StatusForbidden)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://127.0.0.1:"+port && o != "http://localhost:"+port {
		http.Error(w, "pfwd's web UI doesn't answer requests from other sites", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// static serves the app's files. index.html is never cached; the hashed
// assets are.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.o.Assets == nil {
		http.Error(w, "this pfwd was built without the web UI; install a release build", http.StatusNotFound)
		return
	}
	name := r.URL.Path[1:]
	if name == "" {
		name = "index.html"
	}
	if st, err := fs.Stat(s.o.Assets, name); err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, s.o.Assets, name) //nolint:gosec // an fs.FS refuses paths outside it
}
