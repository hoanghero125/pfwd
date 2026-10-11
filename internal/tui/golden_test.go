package tui

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// mainScreen is the full main screen: three forwards (one whose
// port went away), three detected ports and a hidden one.
func mainScreen(t *testing.T) Model {
	t.Helper()
	return mainScreenWith(t, Options{})
}

func mainScreenWith(t *testing.T, opts Options) Model {
	t.Helper()
	e := newFakeEngine()
	m := connectedWith(newModelWith(e, opts),
		ignored(22, "sshd"), withLabel(5173, "node", "Vite"), withLabel(6379, "redis-server", "Redis"),
		withLabel(8888, "jupyter-lab", "Jupyter"), withLabel(9090, "", "Prometheus"))
	m = apply(m, engine.AutoChanged{Host: "devbox", On: true})
	m, _ = press(t, m, "enter")            // 5173
	m, _ = press(t, m, "G", "up", "enter") // 8888
	for _, fw := range e.Forwards() {
		fw.Scheme = forward.SchemeHTTP
		fw.Spec.Label = map[uint16]string{5173: "Vite", 8888: "Jupyter"}[fw.Spec.RemotePort]
		m = apply(m, engine.ForwardChanged{Forward: fw})
	}
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{
		ID: "9", Host: "devbox", State: engine.Failed, Err: errors.New("disconnected"),
		Spec: engine.ForwardSpec{BindAddr: "127.0.0.1", LocalPort: 8000, RemoteHost: "127.0.0.1", RemotePort: 8000},
	}})
	e.mu.Lock()
	e.forwards[0].Stats = forward.Stats{Conns: 2}
	e.mu.Unlock()
	m, _ = update(t, m, tickMsg(t0))
	e.mu.Lock()
	e.forwards[0].Stats = forward.Stats{Conns: 2, BytesIn: 12 * 1024, BytesOut: 1024}
	e.mu.Unlock()
	m, _ = update(t, m, tickMsg(t0.Add(time.Second)))
	m, _ = press(t, m, "g")
	// Show the forward of 5173 as the last event.
	m.status = status{text: "Forwarded :5173 to localhost:5173", kind: statusOK, at: t0}
	return m
}

func connecting(*testing.T) Model {
	return apply(newModel(newFakeEngine()), engine.SessionChanged{Host: "devbox", State: engine.Connecting})
}

func goldenCases() map[string]func(*testing.T) Model {
	return map[string]func(*testing.T) Model{
		"main":       mainScreen,
		"connecting": connecting,
		"scanning": func(*testing.T) Model {
			return apply(newModel(newFakeEngine()), engine.SessionChanged{Host: "devbox", State: engine.Connected})
		},
		"empty": func(*testing.T) Model { return connectedWith(newModel(newFakeEngine())) },
		"unavailable": func(*testing.T) Model {
			return apply(newModel(newFakeEngine()),
				engine.SessionChanged{Host: "devbox", State: engine.Connected},
				engine.PortsChanged{Host: "devbox", Strategy: detect.None, OS: "Darwin"})
		},
		"unavailable-error": func(*testing.T) Model {
			return apply(newModel(newFakeEngine()),
				engine.SessionChanged{Host: "devbox", State: engine.Connected},
				engine.PortsChanged{Host: "devbox", Strategy: detect.None, Err: errors.New("probe the server for ss and /proc: exec request denied")})
		},
		"failed": func(*testing.T) Model {
			return apply(newModel(newFakeEngine()), engine.SessionChanged{Host: "devbox", State: engine.SessionFailed, Err: &sshx.Error{
				Kind: sshx.ErrAuth,
				Msg:  "authentication to devbox as alice failed: the server accepted none of the 2 keys offered. Add your key to ssh-agent (`ssh-add <key>`) or set IdentityFile for devbox in ~/.ssh/config, and check that `ssh devbox` works",
			}})
		},
		"modal-hostkey": func(*testing.T) Model {
			return apply(connecting(nil), engine.NewHostKeyPrompt("devbox", sshx.HostKeyQuestion{
				Host: "devbox", KeyType: "ssh-ed25519", Fingerprint: "SHA256:KkzXxjJZNedqGLrx+HOOCWJgJimyaMj/kxgCfnf8M7A",
			}, make(chan bool, 1)))
		},
		"modal-custom": func(t *testing.T) Model {
			m, _ := press(t, connectedWith(newModel(newFakeEngine()), rp(5173, "node")), "F", "5", "1", "7", "4")
			return m
		},
		"modal-custom-invalid": func(t *testing.T) Model {
			m, _ := press(t, connectedWith(newModel(newFakeEngine()), rp(5173, "node")), "F", "0", "enter")
			return m
		},
		"modal-conflict": func(t *testing.T) Model {
			e := newFakeEngine()
			e.forwardErr = &forward.PortInUseError{Addr: "127.0.0.1", Port: 5173, Suggested: 5174}
			m, _ := press(t, connectedWith(newModel(e), rp(5173, "node")), "enter")
			return m
		},
		"modal-conflict-nosuggest": func(t *testing.T) Model {
			e := newFakeEngine()
			e.forwardErr = &forward.PortInUseError{Addr: "127.0.0.1", Port: 5173}
			m, _ := press(t, connectedWith(newModel(e), rp(5173, "node")), "enter")
			return m
		},
		"modal-help": func(t *testing.T) Model {
			m, _ := press(t, mainScreen(t), "?")
			return m
		},
		"modal-quit": func(t *testing.T) Model {
			m, _ := press(t, mainScreen(t), "q")
			return m
		},
		"hidden-shown": func(t *testing.T) Model {
			m, _ := press(t, mainScreen(t), "h")
			return m
		},
		"filter-typing": func(t *testing.T) Model {
			m, _ := press(t, mainScreen(t), "/", "8", "8")
			return m
		},
		"filter-kept": func(t *testing.T) Model {
			m, _ := press(t, mainScreen(t), "/", "r", "e", "enter")
			return m
		},
		"filter-nomatch": func(t *testing.T) Model {
			m, _ := press(t, mainScreen(t), "/", "z", "z", "enter")
			return m
		},
		"all-hidden": func(*testing.T) Model {
			return connectedWith(newModel(newFakeEngine()), ignored(22, "sshd"), ignored(53, "systemd-resolved"))
		},
		"log": func(t *testing.T) Model {
			m := apply(mainScreen(t),
				engine.PortsChanged{Host: "devbox", Strategy: detect.SS, Ports: []engine.Port{withLabel(8080, "python3", "")}, Added: []uint16{8080}, Removed: []uint16{6379, 9090}},
				engine.AutoForwardFailed{Host: "devbox", Port: 8080, Err: errors.New("port 8080 is busy on this machine; something else on this computer listens on it")},
			)
			m, _ = press(t, m, "L")
			return m
		},
		"ascii": func(t *testing.T) Model { return mainScreenWith(t, Options{ASCII: true}) },
		"ascii-help": func(t *testing.T) Model {
			m, _ := press(t, mainScreenWith(t, Options{ASCII: true}), "?")
			return m
		},
		"bind-badge": func(t *testing.T) Model { return mainScreenWith(t, Options{BindAddr: "0.0.0.0"}) },
	}
}

func TestGolden(t *testing.T) {
	sizes := [][2]int{{80, 24}, {140, 40}}
	for name, build := range goldenCases() {
		for _, size := range sizes {
			checkGolden(t, name, build(t), size[0], size[1])
		}
	}
	checkGolden(t, "main", mainScreen(t), 60, 24)
	checkGolden(t, "too-small", mainScreen(t), 50, 10)
}

// TestGoldenANSI pins the colors and attributes of the main screen on a
// dark and a light background, and what is left of them with NO_COLOR.
// ESC is written as \e to keep the files readable.
func TestGoldenANSI(t *testing.T) {
	dark := mainScreen(t)
	light := mainScreenWith(t, Options{Theme: "light"})
	for name, content := range map[string]string{
		"dark":     sized(dark, 80, 24).View().Content,
		"light":    sized(light, 80, 24).View().Content,
		"no-color": downsample(t, sized(dark, 80, 24).View().Content, colorprofile.Ascii),
	} {
		checkRawGolden(t, "ansi-"+name+"-80x24", strings.ReplaceAll(content, "\x1b", `\e`)+"\n")
	}
}

// downsample renders s the way Bubble Tea does for a terminal with the
// given color profile.
func downsample(t *testing.T, s string, p colorprofile.Profile) string {
	t.Helper()
	var b strings.Builder
	w := &colorprofile.Writer{Forward: &b, Profile: p}
	if _, err := w.WriteString(s); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func checkRawGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui/ -update to create it)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s does not match; run go test ./internal/tui/ -update and review the diff", path)
	}
}

func checkGolden(t *testing.T, name string, m Model, w, h int) {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	got := normalize(view(mustModel(next)))
	path := filepath.Join("testdata", name+"-"+itoa(w)+"x"+itoa(h)+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui/ -update to create it)", err)
	}
	if got != normalize(string(want)) {
		t.Errorf("%s does not match the view; run go test ./internal/tui/ -update and review the diff.\ngot:\n%s", path, got)
	}
}

// normalize drops trailing spaces so golden files stay readable.
func normalize(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}
