package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
)

// Handler serves the calls a Server receives.
type Handler interface {
	// Handle answers one call. An *Error keeps its code; any other error
	// is reported as internal.
	Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
	// Watch sends events until ctx ends or send fails.
	Watch(ctx context.Context, send func(Event) error) error
}

// Server serves a Handler on a listener.
type Server struct {
	h   Handler
	log *slog.Logger
	// checkPeer rejects a peer that isn't this user; tests replace it.
	checkPeer func(net.Conn) error
}

// NewServer returns a Server for h. A nil logger discards.
func NewServer(h Handler, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{h: h, log: log, checkPeer: checkPeer}
}

// Serve accepts connections on ln until ctx ends, then closes ln, waits
// for every connection to finish and returns nil.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, c)
		}()
	}
}

// serveConn answers one connection's calls in order. A watch call turns
// the connection into an event stream until either side ends it.
func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	defer func() { _ = c.Close() }()
	if err := s.checkPeer(c); err != nil {
		s.log.Warn("ipc peer refused", "err", err)
		return
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	enc := json.NewEncoder(c)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(Response{Error: Errorf(CodeUsage, "malformed request: %v", err)})
			return
		}
		if req.V != Version {
			_ = enc.Encode(Response{ID: req.ID, Error: Errorf(CodeUsage,
				"this pfwd daemon speaks protocol %d, the client %d; restart the daemon with `pfwd daemon stop` and `pfwd daemon start`", Version, req.V)})
			return
		}
		if req.Method == "watch" {
			if err := enc.Encode(Response{ID: req.ID, Result: json.RawMessage(`{"watching":true}`)}); err == nil {
				s.watch(ctx, c, enc)
			}
			return
		}
		resp := Response{ID: req.ID}
		result, err := s.h.Handle(ctx, req.Method, req.Params)
		if err == nil {
			resp.Result, err = json.Marshal(result)
		}
		if err != nil {
			resp.Error = asError(err)
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) {
		_ = enc.Encode(Response{Error: Errorf(CodeUsage, "a request is longer than 1 MiB")})
	}
}

// watch streams the handler's events on c until the handler stops, ctx
// ends, or the client hangs up.
func (s *Server) watch(ctx context.Context, c net.Conn, enc *json.Encoder) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		defer cancel()
		_, _ = io.Copy(io.Discard, c) // ends when the client closes
	}()
	if err := s.h.Watch(ctx, func(ev Event) error { return enc.Encode(ev) }); err != nil && ctx.Err() == nil {
		s.log.Debug("ipc watch ended", "err", err)
	}
	_ = c.Close()
	<-gone
}
