package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	// go-winio runs one I/O completion goroutine for the whole process.
	goleak.VerifyTestMain(m, goleak.IgnoreAnyFunction("github.com/Microsoft/go-winio.ioCompletionProcessor"))
}

// fakeHandler echoes, fails on request, and streams a few events.
type fakeHandler struct {
	mu      sync.Mutex
	watched int
}

func (h *fakeHandler) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "echo":
		var v map[string]any
		if err := json.Unmarshal(params, &v); err != nil {
			return nil, Errorf(CodeUsage, "bad params")
		}
		return v, nil
	case "missing":
		return nil, Errorf(CodeNotFound, "no forward 7")
	case "boom":
		return nil, errors.New("something broke")
	}
	return nil, Errorf(CodeUsage, "unknown method %s", method)
}

func (h *fakeHandler) Watch(ctx context.Context, send func(Event) error) error {
	h.mu.Lock()
	h.watched++
	h.mu.Unlock()
	for i := range 3 {
		if err := send(Event{Event: "tick", Data: json.RawMessage(itoa(i))}); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

func itoa(i int) string { return string(rune('0' + i)) }

// serve runs a Server for h on a fresh path until the test ends.
func serve(t *testing.T, h Handler) string {
	t.Helper()
	path := testPath(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- NewServer(h, nil).Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path
}

func TestRoundTrip(t *testing.T) {
	path := serve(t, &fakeHandler{})
	c, err := Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := range 3 {
		var got map[string]any
		if err := c.Call(t.Context(), "echo", map[string]any{"n": i}, &got); err != nil {
			t.Fatal(err)
		}
		if got["n"] != float64(i) {
			t.Errorf("echo %d = %v", i, got)
		}
	}
}

func TestErrorCodes(t *testing.T) {
	path := serve(t, &fakeHandler{})
	c, err := Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for method, code := range map[string]string{"missing": CodeNotFound, "boom": CodeInternal, "nope": CodeUsage} {
		err := c.Call(t.Context(), method, nil, nil)
		var e *Error
		if !errors.As(err, &e) || e.Code != code {
			t.Errorf("%s: %v, want code %s", method, err, code)
		}
	}
	// The connection still works after errors.
	if err := c.Call(t.Context(), "echo", map[string]any{}, nil); err != nil {
		t.Error(err)
	}
}

// rawConn writes line to a fresh connection and returns the reply line.
func rawConn(t *testing.T, path, line string) string {
	t.Helper()
	c, err := dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, _ := c.Read(buf)
	return string(buf[:n])
}

func TestMalformedAndOldRequests(t *testing.T) {
	path := serve(t, &fakeHandler{})
	if got := rawConn(t, path, "not json\n"); !strings.Contains(got, `"code":"usage"`) || !strings.Contains(got, "malformed") {
		t.Errorf("malformed request: %q", got)
	}
	if got := rawConn(t, path, `{"v":99,"id":1,"method":"echo"}`+"\n"); !strings.Contains(got, "protocol") {
		t.Errorf("another protocol version: %q", got)
	}
}

func TestLineTooLong(t *testing.T) {
	path := serve(t, &fakeHandler{})
	long := `{"v":1,"id":1,"method":"echo","params":{"x":"` + strings.Repeat("a", maxLine) + `"}}` + "\n"
	c, err := dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	go func() { _, _ = c.Write([]byte(long)) }()
	buf := make([]byte, 4096)
	n, _ := c.Read(buf)
	if got := string(buf[:n]); !strings.Contains(got, "longer than 1 MiB") {
		t.Errorf("a request over the cap: %q", got)
	}
}

func TestWatchStream(t *testing.T) {
	h := &fakeHandler{}
	path := serve(t, h)
	ctx, cancel := context.WithCancel(t.Context())
	var got []string
	err := Watch(ctx, path, func(ev Event) {
		got = append(got, ev.Event+string(ev.Data))
		if len(got) == 3 {
			cancel()
		}
	})
	if err != nil || strings.Join(got, ",") != "tick0,tick1,tick2" {
		t.Errorf("Watch = %v, events %q", err, got)
	}
}

func TestDialNoDaemon(t *testing.T) {
	if _, err := Dial(t.Context(), testPath(t)); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Dial with no daemon = %v, want ErrNotRunning", err)
	}
}

func TestListenTwice(t *testing.T) {
	path := serve(t, &fakeHandler{})
	if ln, err := Listen(path); !errors.Is(err, ErrRunning) {
		if ln != nil {
			_ = ln.Close()
		}
		t.Errorf("a second Listen = %v, want ErrRunning", err)
	}
}

func TestPeerRefused(t *testing.T) {
	path := testPath(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(&fakeHandler{}, nil)
	s.checkPeer = func(net.Conn) error { return errors.New("another user") }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	defer func() { cancel(); <-done }()
	c, err := Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	callCtx, callCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer callCancel()
	if err := c.Call(callCtx, "echo", map[string]any{}, nil); err == nil {
		t.Error("a refused peer got an answer")
	}
}

// waitHandler blocks "wait" calls until their context ends, and reports
// that on canceled.
type waitHandler struct {
	fakeHandler
	canceled chan struct{}
}

func (h *waitHandler) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == "wait" {
		<-ctx.Done()
		h.canceled <- struct{}{}
		return nil, ctx.Err()
	}
	return h.fakeHandler.Handle(ctx, method, params)
}

// TestHangUpCancelsTheCall: a client that gives up ends the call on the
// server, and the same Client works for the next call.
func TestHangUpCancelsTheCall(t *testing.T) {
	h := &waitHandler{canceled: make(chan struct{}, 1)}
	path := serve(t, h)
	c, err := Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "wait", nil, nil); err == nil {
		t.Fatal("a call past its deadline succeeded")
	}
	select {
	case <-h.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("the server kept running the call the client gave up on")
	}
	var got map[string]any
	if err := c.Call(t.Context(), "echo", map[string]any{"n": 1}, &got); err != nil || got["n"] != float64(1) {
		t.Errorf("the next call = %v, %v", got, err)
	}
}
