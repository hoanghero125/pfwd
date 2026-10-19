package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// twoHosts is a model with tabs for devbox and gpu.
func twoHosts(e Engine, opts Options) Model {
	m := NewHosts(e, []engine.HostID{"devbox", "gpu"}, opts)
	m.now = func() time.Time { return t0 }
	m.goos = "linux"
	m.every = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	return m
}

// bothConnected connects devbox (listening on 5173) and gpu (on 8888).
func bothConnected(m Model) Model {
	return apply(m,
		engine.SessionChanged{Host: "devbox", State: engine.Connected},
		engine.PortsChanged{Host: "devbox", Strategy: detect.SS, OS: "Linux", Ports: []engine.Port{rp(5173, "node")}},
		engine.SessionChanged{Host: "gpu", State: engine.Connected},
		engine.PortsChanged{Host: "gpu", Strategy: detect.SS, OS: "Linux", Ports: []engine.Port{rp(8888, "jupyter-lab")}},
	)
}

// selectedLine is the table line with the cursor.
func selectedLine(m Model) string {
	for _, l := range strings.Split(view(m), "\n") {
		if strings.HasPrefix(l, unicodeGlyphs.cursor) {
			return l
		}
	}
	return ""
}

// tableOf is the view below the host bar.
func tableOf(m Model) string {
	v := view(m)
	_, rest, _ := strings.Cut(v, "\n")
	return rest
}

func TestTabsSwitchHosts(t *testing.T) {
	m := bothConnected(twoHosts(newFakeEngine(), Options{}))
	bar := strings.SplitN(view(m), "\n", 2)[0]
	if !strings.Contains(bar, "devbox") || !strings.Contains(bar, "gpu") {
		t.Fatalf("host bar lacks a tab: %q", bar)
	}
	if tb := tableOf(m); !strings.Contains(tb, ":5173") || strings.Contains(tb, ":8888") {
		t.Errorf("devbox tab:\n%s", view(m))
	}
	m, _ = press(t, m, "tab")
	if tb := tableOf(m); !strings.Contains(tb, ":8888") || strings.Contains(tb, ":5173") {
		t.Errorf("gpu tab after tab:\n%s", view(m))
	}
	m, _ = press(t, m, "shift+tab")
	if tb := tableOf(m); !strings.Contains(tb, ":5173") {
		t.Errorf("back on devbox after shift+tab:\n%s", view(m))
	}
}

func TestEventsStayOnTheirHost(t *testing.T) {
	m := bothConnected(twoHosts(newFakeEngine(), Options{}))
	m = apply(m,
		engine.PortsChanged{Host: "gpu", Strategy: detect.SS, OS: "Linux", Ports: []engine.Port{rp(8888, "jupyter-lab"), rp(9999, "x")}},
		engine.SessionChanged{Host: "gpu", State: engine.Reconnecting, Attempt: 1, RetryAt: t0.Add(4 * time.Second), Err: errors.New("broken pipe")},
	)
	if v := view(m); strings.Contains(v, ":9999") || !strings.Contains(strings.SplitN(v, "\n", 2)[0], "devbox ● connected") {
		t.Errorf("gpu's events changed the devbox tab:\n%s", v)
	}
	m, _ = press(t, m, "tab")
	if bar := strings.SplitN(view(m), "\n", 2)[0]; !strings.Contains(bar, "reconnecting") {
		t.Errorf("gpu tab's bar: %q", bar)
	}
}

func TestKeysActOnTheTabsHost(t *testing.T) {
	e := newFakeEngine()
	m := bothConnected(twoHosts(e, Options{}))
	_, _ = press(t, m, "tab", "enter")
	if len(e.hosts) != 1 || e.hosts[0] != "gpu" || e.specs[0].RemotePort != 8888 {
		t.Errorf("enter on the gpu tab forwarded %v %+v", e.hosts, e.specs)
	}
}

func TestAllHostsView(t *testing.T) {
	e := newFakeEngine()
	m := bothConnected(twoHosts(e, Options{}))
	m, _ = press(t, m, "ctrl+a")
	v := view(m)
	for _, want := range []string{"all hosts", "host", ":5173", ":8888", "devbox", "gpu"} {
		if !strings.Contains(v, want) {
			t.Errorf("all-hosts view lacks %q:\n%s", want, v)
		}
	}
	for range 5 {
		if strings.Contains(selectedLine(m), ":8888") {
			break
		}
		m, _ = press(t, m, "down")
	}
	m, _ = press(t, m, "enter")
	if len(e.hosts) != 1 || e.hosts[0] != "gpu" {
		t.Errorf("enter on gpu's row in the all view forwarded on %v", e.hosts)
	}
	m, _ = press(t, m, "ctrl+a")
	if v := view(m); strings.Contains(v, "all hosts") || !strings.Contains(tableOf(m), ":5173") {
		t.Errorf("ctrl+a again should go back to the devbox tab:\n%s", v)
	}
}

func TestHostPicker(t *testing.T) {
	var connected []string
	opts := Options{
		Hosts: func(context.Context) ([]HostChoice, error) {
			return []HostChoice{{Name: "staging", LastUsed: t0.Add(-2 * time.Hour)}, {Name: "devbox"}, {Name: "gpu"}, {Name: "web"}}, nil
		},
		Connect: func(_ context.Context, name string) (engine.HostID, error) {
			connected = append(connected, name)
			return engine.HostID(name), nil
		},
	}
	m := bothConnected(twoHosts(newFakeEngine(), opts))
	m, _ = press(t, m, "+")
	v := view(m)
	if !strings.Contains(v, "Connect another host") || !strings.Contains(v, "staging") || !strings.Contains(v, "2h ago") {
		t.Fatalf("picker:\n%s", v)
	}
	if strings.Contains(v, "▶ devbox") || strings.Contains(v, "│   devbox") {
		t.Errorf("the picker offers a host that is already open:\n%s", v)
	}
	m, _ = press(t, m, "w", "e", "enter")
	if len(connected) != 1 || connected[0] != "web" {
		t.Fatalf("Connect calls = %q, want web", connected)
	}
	if bar := strings.SplitN(view(m), "\n", 2)[0]; !strings.Contains(bar, "web") || m.host != "web" {
		t.Errorf("the new host is not the current tab: %q", bar)
	}
}

func TestHostPickerConnectError(t *testing.T) {
	opts := Options{
		Hosts: func(context.Context) ([]HostChoice, error) { return []HostChoice{{Name: "typo"}}, nil },
		Connect: func(context.Context, string) (engine.HostID, error) {
			return "", errors.New("no Host typo in ~/.ssh/config; check the name")
		},
	}
	m := bothConnected(twoHosts(newFakeEngine(), opts))
	m, _ = press(t, m, "+", "enter")
	if v := view(m); !strings.Contains(v, "no Host typo") || m.host != "devbox" {
		t.Errorf("a host that fails to resolve:\n%s", v)
	}
}

func TestPromptFromBackgroundHost(t *testing.T) {
	m := bothConnected(twoHosts(newFakeEngine(), Options{}))
	q := sshx.CredentialQuestion{Host: "gpu", Kind: sshx.Password, Prompt: "alice"}
	m = apply(m, engine.NewCredentialPrompt("gpu", q, make(chan engine.CredentialAnswer, 1)))
	if v := view(m); !strings.Contains(v, "Password for alice@gpu") {
		t.Errorf("a prompt from the gpu tab didn't show on devbox:\n%s", v)
	}
}

// TestHostKeyPromptsQueue: a second host's host-key prompt waits until the
// first is answered, so the modal never changes under the user's keys.
func TestHostKeyPromptsQueue(t *testing.T) {
	m := twoHosts(newFakeEngine(), Options{})
	devboxReply, gpuReply := make(chan bool, 1), make(chan bool, 1)
	q := sshx.HostKeyQuestion{Host: "devbox", KeyType: "ssh-ed25519", Fingerprint: "SHA256:devbox"}
	m = apply(m, engine.NewHostKeyPrompt("devbox", q, devboxReply))
	q2 := sshx.HostKeyQuestion{Host: "gpu", KeyType: "ssh-ed25519", Fingerprint: "SHA256:gpu"}
	m = apply(m, engine.NewHostKeyPrompt("gpu", q2, gpuReply))
	if v := view(m); !strings.Contains(v, "SHA256:devbox") || strings.Contains(v, "SHA256:gpu") {
		t.Fatalf("the second prompt replaced the first:\n%s", v)
	}
	m, _ = press(t, m, "y")
	select {
	case ok := <-devboxReply:
		if !ok {
			t.Error("y on devbox's key answered no")
		}
	default:
		t.Fatal("devbox's prompt got no answer")
	}
	select {
	case <-gpuReply:
		t.Fatal("gpu's prompt was answered without being shown")
	default:
	}
	if v := view(m); !strings.Contains(v, "SHA256:gpu") {
		t.Errorf("gpu's prompt didn't open next:\n%s", v)
	}
}

// TestHostKeyPromptWaitsBehindCredential: a host-key prompt doesn't
// replace a credential modal being typed into.
func TestHostKeyPromptWaitsBehindCredential(t *testing.T) {
	m := twoHosts(newFakeEngine(), Options{})
	cq := sshx.CredentialQuestion{Host: "devbox", Kind: sshx.Password, Prompt: "alice"}
	m = apply(m, engine.NewCredentialPrompt("devbox", cq, make(chan engine.CredentialAnswer, 1)))
	m, _ = press(t, m, "h", "i")
	q := sshx.HostKeyQuestion{Host: "gpu", KeyType: "ssh-ed25519", Fingerprint: "SHA256:gpu"}
	m = apply(m, engine.NewHostKeyPrompt("gpu", q, make(chan bool, 1)))
	if v := view(m); !strings.Contains(v, "Password for alice@devbox") || strings.Contains(v, "SHA256:gpu") {
		t.Errorf("the host-key prompt replaced the password modal:\n%s", v)
	}
}

func TestDetachWording(t *testing.T) {
	m := connectedWith(newModelWith(newFakeEngine(), Options{Detach: true}), rp(5173, "node"))
	if v := view(m); !strings.Contains(v, "q detach") {
		t.Errorf("hints:\n%s", v)
	}
	m, _ = press(t, m, "?")
	if v := view(sized(m, 140, 40)); !strings.Contains(v, "the daemon keeps the forwards") {
		t.Errorf("help:\n%s", v)
	}
}
