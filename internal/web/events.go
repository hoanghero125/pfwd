package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/hoanghero125/pfwd/internal/ipc"
)

// writeWait bounds one message's write; a browser that stops reading is
// dropped.
const writeWait = 5 * time.Second

// events streams the daemon's events to the page: what it knows now, then
// every change, and every second the forwards' counters as a stats event.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") == "" {
		http.Error(w, "a WebSocket needs an Origin", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	port, closing := s.port, s.closing
	if !closing {
		s.streams.Add(1)
	}
	s.mu.Unlock()
	if closing {
		http.Error(w, "pfwd is stopping", http.StatusServiceUnavailable)
		return
	}
	defer s.streams.Done()
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"127.0.0.1:" + strconv.Itoa(port), "localhost:" + strconv.Itoa(port)},
	})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(4096)
	// The page sends nothing; CloseRead ends ctx when it goes away.
	ctx := c.CloseRead(r.Context())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	send := func(e ipc.Event) error {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, writeWait)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, data)
	}
	stats := make(chan struct{})
	go func() {
		defer close(stats)
		defer cancel()
		t := time.NewTicker(s.o.StatsEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				res, err := s.b.Handle(ctx, "stats", nil)
				if err != nil {
					continue
				}
				data, err := json.Marshal(res)
				if err != nil {
					continue
				}
				if send(ipc.Event{Event: "stats", Data: data}) != nil {
					return
				}
			}
		}
	}()
	err = s.b.Watch(ctx, send)
	cancel()
	<-stats
	if err != nil && ctx.Err() == nil {
		s.o.Logger.Debug("web UI event stream ended", "err", err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}
