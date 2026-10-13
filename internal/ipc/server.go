package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"slices"
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
// the connection into an event stream until either side ends it. When the
// client hangs up, the call it is waiting for is canceled.
func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	if err := s.checkPeer(c); err != nil {
		s.log.Warn("ipc peer refused", "err", err)
		_ = c.Close()
		return
	}
	lines := make(chan []byte)
	var tooLong bool
	read := make(chan struct{})
	go func() {
		defer close(read)
		defer close(lines)
		sc := bufio.NewScanner(c)
		sc.Buffer(make([]byte, 0, 64<<10), maxLine)
		for sc.Scan() {
			select {
			case lines <- slices.Clone(sc.Bytes()):
			case <-ctx.Done():
				return
			}
		}
		// Closing lines tells the loop below; a request that is too long
		// gets an answer first.
		tooLong = errors.Is(sc.Err(), bufio.ErrTooLong)
		if !tooLong {
			cancel() // the client hung up: end what it asked for
		}
	}()
	defer func() {
		cancel()
		_ = c.Close()
		<-read
	}()

	enc := json.NewEncoder(c)
	for {
		var line []byte
		var ok bool
		select {
		case line, ok = <-lines:
		case <-ctx.Done():
			return
		}
		if !ok {
			if tooLong {
				_ = enc.Encode(Response{Error: Errorf(CodeUsage, "a request is longer than 1 MiB")})
			}
			return
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
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
				// Lines the client sends now are read and dropped; its
				// hanging up ends the watch.
				drained := make(chan struct{})
				go func() {
					defer close(drained)
					for range lines {
						continue
					}
				}()
				if err := s.h.Watch(ctx, func(ev Event) error { return enc.Encode(ev) }); err != nil && ctx.Err() == nil {
					s.log.Debug("ipc watch ended", "err", err)
				}
				cancel()
				_ = c.Close()
				<-read
				<-drained
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
}
