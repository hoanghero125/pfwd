package cli

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/safetext"
)

// forwardJSON is a forward in --json output. Keys are stable and always
// present.
type forwardJSON struct {
	ID         string    `json:"id"`
	Host       string    `json:"host"`
	Kind       string    `json:"kind"`
	Bind       string    `json:"bind"`
	Local      uint16    `json:"local"`
	RemoteHost string    `json:"remote_host"`
	Remote     uint16    `json:"remote"`
	State      string    `json:"state"`
	Pause      string    `json:"pause"`
	Error      string    `json:"error"`
	Pinned     bool      `json:"pinned"`
	Auto       bool      `json:"auto"`
	Conns      int64     `json:"conns"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
	StartedAt  time.Time `json:"started_at"`
}

func toForwardJSON(f engine.Forward) forwardJSON {
	j := forwardJSON{
		ID: f.ID, Host: string(f.Host), Kind: f.Spec.Kind.String(), Bind: f.Spec.BindAddr,
		Local: f.Spec.LocalPort, RemoteHost: f.Spec.RemoteHost, Remote: f.Spec.RemotePort,
		State: f.State.String(), Pause: f.Pause.String(), Pinned: f.Spec.Pinned, Auto: f.Spec.Auto,
		Conns: f.Stats.Conns, BytesIn: f.Stats.BytesIn, BytesOut: f.Stats.BytesOut, StartedAt: f.StartedAt,
	}
	if f.Err != nil {
		j.Error = f.Err.Error()
	}
	return j
}

// portJSON is a detected port in --json output.
type portJSON struct {
	Port    uint16   `json:"port"`
	Addrs   []string `json:"addrs"`
	Process string   `json:"process"`
	PID     int      `json:"pid"`
	Label   string   `json:"label"`
	Ignored bool     `json:"ignored"`
}

func toPortJSON(p engine.Port) portJSON {
	addrs := p.Addrs
	if addrs == nil {
		addrs = []string{}
	}
	return portJSON{Port: p.Port, Addrs: addrs, Process: p.Process, PID: p.PID, Label: p.Label, Ignored: p.Ignored}
}

// hostJSON is one host in pfwd ls --json.
type hostJSON struct {
	Host     string        `json:"host"`
	State    string        `json:"state"`
	OS       string        `json:"os"`
	Ports    []portJSON    `json:"ports"`
	Forwards []forwardJSON `json:"forwards"`
}

// localAddr is a forward's address on this machine: where a local or SOCKS
// forward listens, or where a remote forward delivers.
func localAddr(s engine.ForwardSpec) string {
	host := s.BindAddr
	if s.Kind == engine.Remote {
		host = s.RemoteHost
	}
	switch host {
	case "", "127.0.0.1", "::1", "localhost":
		return "localhost:" + strconv.Itoa(int(s.LocalPort))
	}
	return net.JoinHostPort(host, strconv.Itoa(int(s.LocalPort)))
}

// describeForward says what a new forward does, as the TUI's status line
// does.
func describeForward(f engine.Forward) string {
	host := safetext.Clean(string(f.Host))
	switch f.Spec.Kind {
	case engine.Remote:
		return fmt.Sprintf("Exposed %s on :%d of %s", localAddr(f.Spec), f.Spec.RemotePort, host)
	case engine.Dynamic:
		return "SOCKS proxy on " + localAddr(f.Spec)
	}
	text := fmt.Sprintf("Forwarded :%d to %s", f.Spec.RemotePort, localAddr(f.Spec))
	if f.MappedFrom != 0 {
		text += fmt.Sprintf(" (%d needs privileges here; using %d)", f.MappedFrom, f.Spec.LocalPort)
	}
	return text
}

// forwardName names a forward in a log line: its server port, or the SOCKS
// proxy.
func forwardName(f engine.Forward) string {
	if f.Spec.Kind == engine.Dynamic {
		return "the SOCKS proxy on " + localAddr(f.Spec)
	}
	return ":" + strconv.Itoa(int(f.Spec.RemotePort))
}
