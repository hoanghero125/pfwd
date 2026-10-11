package detect

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// Runner runs commands on the server. *sshx.Conn implements it.
type Runner interface {
	// Output runs cmd and returns its stdout.
	Output(ctx context.Context, cmd string) ([]byte, error)
	// Start runs cmd and streams its stdout until the reader is closed or
	// ctx ends.
	Start(ctx context.Context, cmd string) (io.ReadCloser, error)
}

// Update is a detection result for one host.
type Update struct {
	Strategy Strategy
	// OS is what uname -s printed; empty when unknown.
	OS string
	// Ports is the merged snapshot, sorted by port.
	Ports []RemotePort
	// Added are ports that just became stable (see Tracker), and Removed
	// are stable ports that went away.
	Added   []RemotePort
	Removed []uint16
}

// WatchOptions configures Watch.
type WatchOptions struct {
	// Interval is the polling interval (at least 500 ms).
	Interval time.Duration
	// Logger receives debug details; nil discards them.
	Logger *slog.Logger
	// Tracker decides when ports count as added. Pass the same tracker on
	// every call for a host so its state survives restarts; nil means a
	// new tracker with a debounce of 2.
	Tracker *Tracker
	// ProbeTimeout bounds the one-time probe; 0 means 10 s.
	ProbeTimeout time.Duration
}

// defaultProbeTimeout bounds the probe command.
const defaultProbeTimeout = 10 * time.Second

// Frame markers around each snapshot in the loop output. \036 is the ASCII
// record separator, which never appears in ss or /proc output.
const (
	frameBegin = "\x1ePFWD"
	frameEnd   = "\x1ePFWD END"
	// maxFrame drops a frame that grows past this without an end marker.
	maxFrame = 4 << 20
)

// Restart backoff for the snapshot loop.
const (
	minBackoff = 500 * time.Millisecond
	maxBackoff = 30 * time.Second
)

// Watch probes the server once, then streams snapshots on one exec channel
// until ctx ends, and returns nil then. It calls emit for the first
// snapshot and after that only when the ports change. When the server has
// no supported tool, it emits one Update with Strategy None and returns.
// When the loop's stream ends, Watch restarts it after a backoff.
func Watch(ctx context.Context, r Runner, o WatchOptions, emit func(Update)) error {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	tracker := o.Tracker
	if tracker == nil {
		tracker = NewTracker(2)
	}
	probeTimeout := o.ProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = defaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	out, err := r.Output(probeCtx, ShellCommand(ProbeScript))
	cancel()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("probe the server for ss and /proc: %w", err)
	}
	probe := ParseProbe(string(out))
	strategy := probe.Strategy()
	log.Debug("detection probe", "os", probe.OS, "strategy", strategy.String())
	if strategy == None {
		emit(Update{Strategy: None, OS: probe.OS})
		return nil
	}
	parse := ParseSS
	if strategy == Proc {
		parse = ParseProcNetTCP
	}
	cmd := ShellCommand(LoopScript(strategy, o.Interval))

	var last []RemotePort
	emitted := false
	backoff := minBackoff
	for {
		stream, err := r.Start(ctx, cmd)
		if err == nil {
			err = readFrames(stream, parse, func(ports []RemotePort) {
				backoff = minBackoff
				merged := Merge(ports)
				added, removed := tracker.Observe(merged)
				if emitted && equalPorts(merged, last) && len(added) == 0 && len(removed) == 0 {
					return
				}
				emitted, last = true, merged
				emit(Update{Strategy: strategy, OS: probe.OS, Ports: slices.Clone(merged), Added: added, Removed: removed})
			})
			_ = stream.Close()
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Debug("detection loop ended; restarting", "err", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// readFrames splits the loop output into frames and calls frame with each
// parsed snapshot. Text outside frames and an unfinished last frame are
// ignored.
func readFrames(r io.Reader, parse func(io.Reader) ([]RemotePort, error), frame func([]RemotePort)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var buf bytes.Buffer
	in := false
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case line == frameBegin:
			in = true
			buf.Reset()
		case line == frameEnd:
			if in {
				if ports, err := parse(&buf); err == nil {
					frame(ports)
				}
			}
			in = false
		case in:
			if buf.Len()+len(line) > maxFrame {
				in = false
				buf.Reset()
				continue
			}
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	return sc.Err()
}

// equalPorts reports whether two merged snapshots are the same.
func equalPorts(a, b []RemotePort) bool {
	return slices.EqualFunc(a, b, func(x, y RemotePort) bool {
		return x.Port == y.Port && x.PID == y.PID && x.Process == y.Process && slices.Equal(x.Addrs, y.Addrs)
	})
}
