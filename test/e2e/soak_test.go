//go:build e2e && soak

package e2e

import (
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/hoanghero125/pfwd/internal/engine"
)

// soakSetting reads a duration from the environment, with a default.
func soakSetting(t *testing.T, name string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		t.Fatalf("%s=%q: use a duration like 5m", name, v)
	}
	return d
}

// TestSoak keeps five forwards up for PFWD_SOAK_DURATION (1h) while the
// connection fails every PFWD_SOAK_DROP_EVERY (5m) for a few seconds:
// closed outright on odd rounds, and gone silent on even ones, which only
// keepalives notice. After every reconnect each forward must serve again.
// The goroutine count may not grow from round to round, and at the end
// all forwards are Active and no goroutine is left behind. Run it with
// make soak.
func TestSoak(t *testing.T) {
	duration := soakSetting(t, "PFWD_SOAK_DURATION", time.Hour)
	every := soakSetting(t, "PFWD_SOAK_DROP_EVERY", 5*time.Minute)
	const outage = 5 * time.Second
	ignore := goleak.IgnoreCurrent()

	proxy, dial := proxiedHost(t, "ss")
	eng := manualEngine(dial)
	var connected atomic.Bool
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range eng.Events() {
			if s, ok := ev.(engine.SessionChanged); ok {
				connected.Store(s.State == engine.Connected)
			}
		}
	}()
	waitConnected := func(timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for !connected.Load() {
			if time.Now().After(deadline) {
				t.Fatalf("not connected after %v", timeout)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	eng.Connect("ss")
	waitConnected(30 * time.Second)
	ports := []uint16{8201, 8202, 8203, 8204, 8205}
	for _, p := range ports {
		startServer(t, "ss", strconv.Itoa(int(p)))
	}
	time.Sleep(3 * time.Second) // let detection see them, so they don't pause
	for _, p := range ports {
		if _, err := eng.Forward("ss", engine.ForwardSpec{RemotePort: p}); err != nil {
			t.Fatalf("forward :%d: %v", p, err)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	checkAll := func(round int) {
		t.Helper()
		for _, p := range ports {
			var err error
			for try := 0; try < 20; try++ {
				var resp *http.Response
				if resp, err = client.Get("http://127.0.0.1:" + strconv.Itoa(int(p)) + "/"); err == nil {
					resp.Body.Close()
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if err != nil {
				t.Errorf("round %d: GET :%d: %v", round, p, err)
			}
		}
	}

	checkAll(0)
	end := time.Now().Add(duration)
	round, baseline := 0, 0
	for time.Now().Add(every).Before(end) || round == 0 {
		time.Sleep(every)
		round++
		how := "closed"
		if round%2 == 0 {
			how = "silent"
			proxy.blackhole(outage)
		} else {
			proxy.drop(outage)
		}
		start := time.Now()
		// A silent path takes the keepalives (1 s, 2 missed) to notice.
		deadline := time.Now().Add(10 * time.Second)
		for connected.Load() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if connected.Load() {
			t.Fatalf("round %d: the %s connection was never noticed as lost", round, how)
		}
		waitConnected(2 * time.Minute)
		checkAll(round)
		goroutines := runtime.NumGoroutine()
		if round == 1 {
			baseline = goroutines
		}
		t.Logf("round %d (%s): reconnected %v after the failure; %d goroutines", round, how, time.Since(start).Round(time.Millisecond), goroutines)
	}
	if grew := runtime.NumGoroutine() - baseline; round > 2 && grew >= max(5, round-1) {
		t.Errorf("goroutines grew by %d over %d reconnects: something leaks per reconnect", grew, round)
	}

	for _, f := range eng.Forwards() {
		if f.State != engine.Active || f.Stats.TotalConns == 0 || f.Stats.BytesIn == 0 {
			t.Errorf("at the end :%d is %v with %+v, want Active with traffic", f.Spec.RemotePort, f.State, f.Stats)
		}
	}
	if n := len(eng.Forwards()); n != len(ports) {
		t.Errorf("%d forwards at the end, want %d", n, len(ports))
	}
	client.CloseIdleConnections()
	if err := eng.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		t.Fatal("the engine's events never closed after Close")
	}
	proxy.close() // its goroutines started after IgnoreCurrent
	goleak.VerifyNone(t, ignore)
	t.Logf("%d drops survived in %v", round, duration)
}
