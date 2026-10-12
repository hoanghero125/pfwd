//go:build windows

package detect

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// frameWriter signals once it has seen two whole frames, so the loop went
// round while its input was open.
type frameWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	once sync.Once
	seen chan struct{}
}

func (w *frameWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if bytes.Count(w.buf.Bytes(), []byte("\x1ePFWD END")) >= 2 {
		w.once.Do(func() { close(w.seen) })
	}
	return len(p), nil
}

// TestWindowsLoopExitsWhenInputCloses: the PowerShell loop ends when its
// stdin closes, as it does when the SSH channel goes away. Writing to a
// closed stdout doesn't end it: .NET swallows that error.
func TestWindowsLoopExitsWhenInputCloses(t *testing.T) {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		t.Skip("no powershell")
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(fmt.Sprintf(windowsLoop, 200)))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out := &frameWriter{seen: make(chan struct{})}
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-out.seen:
	case err := <-done:
		t.Fatalf("the loop ended while its input was open: %v", err)
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("no two frames from the loop in 30 s")
	}
	_ = stdin.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("the loop kept running after its input closed")
	}
}
