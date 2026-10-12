package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// The fake engine's M3 calls.
func (f *fakeEngine) Reconnect(engine.HostID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconnects++
}

func (f *fakeEngine) StopRetrying(engine.HostID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopRetries++
}

func (f *fakeEngine) SetPinned(id string, pinned bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pinErr != nil {
		return f.pinErr
	}
	f.pins = append(f.pins, id+"="+map[bool]string{true: "pin", false: "unpin"}[pinned])
	return nil
}

func (f *fakeEngine) calls() (reconnects, stopRetries int, pins []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reconnects, f.stopRetries, append([]string(nil), f.pins...)
}

var errLost = &sshx.Error{Kind: sshx.ErrConnect, Msg: "connection to devbox was lost (read: connection reset by peer)"}

// lostScreen is the main screen after the connection dropped: the
// forwards are paused and the next attempt is 4 seconds away.
func lostScreen(t *testing.T) Model {
	t.Helper()
	m := mainScreen(t)
	for _, f := range m.forwards {
		if f.State == engine.Active {
			f.State, f.Pause = engine.Paused, engine.Disconnected
			m = apply(m, engine.ForwardChanged{Forward: f})
		}
	}
	return apply(m, engine.SessionChanged{Host: "devbox", State: engine.Reconnecting, Err: errLost, RetryAt: t0.Add(4 * time.Second), Attempt: 1})
}

func TestReconnectingCountdown(t *testing.T) {
	m := lostScreen(t)
	if v := view(m); !strings.Contains(v, "reconnecting in 4s") {
		t.Errorf("host bar lacks the countdown:\n%s", v)
	}
	m = apply(m, engine.SessionChanged{Host: "devbox", State: engine.Reconnecting, Err: errLost, Attempt: 1})
	if v := view(m); !strings.Contains(v, "reconnecting…") || strings.Contains(v, "reconnecting in") {
		t.Errorf("while dialing, the host bar should say reconnecting…:\n%s", v)
	}
	m = apply(m, engine.SessionChanged{Host: "devbox", State: engine.Reconnecting, Err: errLost, RetryAt: t0.Add(-3 * time.Second), Attempt: 2})
	if v := view(m); strings.Contains(v, "in -") || !strings.Contains(v, "reconnecting…") {
		t.Errorf("a past retry time must not count below zero:\n%s", v)
	}
}

func TestLostConnectionKeepsForwardsOnScreen(t *testing.T) {
	m := lostScreen(t)
	v := view(m)
	for _, want := range []string{":5173", ":8888", "pause", "disconnected"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "redis-server") {
		t.Errorf("stale detected ports are still listed:\n%s", v)
	}
	if !strings.Contains(v, "Lost the connection to devbox") {
		t.Errorf("the status line doesn't say the connection was lost:\n%s", v)
	}
}

func TestReconnectingKeys(t *testing.T) {
	e := newFakeEngine()
	m := lostScreen(t)
	m.eng = e
	m, _ = press(t, m, "c")
	m, _ = press(t, m, "x")
	reconnects, stops, _ := e.calls()
	if reconnects != 1 || stops != 1 || len(e.stopped) != 0 {
		t.Errorf("reconnects %d, stop-retrying %d, forwards stopped %v; want 1, 1, none", reconnects, stops, e.stopped)
	}
	if v := view(m); !strings.Contains(v, "c reconnect") || !strings.Contains(v, "x stop retrying") {
		t.Errorf("hints lack c and x:\n%s", v)
	}
}

func TestReconnectedStatus(t *testing.T) {
	m := apply(lostScreen(t), engine.SessionChanged{Host: "devbox", State: engine.Connected})
	if v := view(m); !strings.Contains(v, "Reconnected to devbox") || !strings.Contains(v, "connected") {
		t.Errorf("view after reconnecting:\n%s", v)
	}
}

func TestDisconnectedAfterLossKeepsTable(t *testing.T) {
	e := newFakeEngine()
	m := apply(lostScreen(t), engine.SessionChanged{Host: "devbox", State: engine.SessionFailed, Err: errors.New("stopped reconnecting to devbox")})
	m.eng = e
	v := view(m)
	if !strings.Contains(v, "disconnected") || !strings.Contains(v, ":5173") || !strings.Contains(v, "c reconnect") {
		t.Errorf("view after giving up:\n%s", v)
	}
	m, _ = press(t, m, "c")
	if reconnects, _, _ := e.calls(); reconnects != 1 {
		t.Errorf("c called Reconnect %d times", reconnects)
	}
}

func TestFirstConnectFailureOffersRetry(t *testing.T) {
	e := newFakeEngine()
	m := apply(newModel(e), engine.SessionChanged{Host: "devbox", State: engine.SessionFailed, Err: errors.New("cannot reach devbox")})
	if v := view(m); !strings.Contains(v, "Press c to try again") {
		t.Errorf("failure page lacks the retry hint:\n%s", v)
	}
	_, _ = press(t, m, "c")
	if reconnects, _, _ := e.calls(); reconnects != 1 {
		t.Errorf("c called Reconnect %d times", reconnects)
	}
}

func TestPinKey(t *testing.T) {
	e := newFakeEngine()
	m := mainScreen(t)
	m.eng = e
	r, _ := m.current()
	m, _ = press(t, m, "p")
	if _, _, pins := e.calls(); len(pins) != 1 || pins[0] != r.fwd.ID+"=pin" {
		t.Fatalf("SetPinned calls = %v", pins)
	}
	if v := view(m); !strings.Contains(v, "Pinned :5173") {
		t.Errorf("status after p:\n%s", v)
	}
	f := *r.fwd
	f.Spec.Pinned = true
	m = apply(m, engine.ForwardChanged{Forward: f})
	if v := view(m); !strings.Contains(v, "pin") {
		t.Errorf("pinned row lacks its badge:\n%s", v)
	}
	m, _ = press(t, m, "p")
	if _, _, pins := e.calls(); len(pins) != 2 || pins[1] != r.fwd.ID+"=unpin" {
		t.Errorf("second p = %v, want unpin", pins)
	}
}

func TestPinErrorShown(t *testing.T) {
	e := newFakeEngine()
	e.pinErr = errors.New("the forward of port 5173 comes from config.toml (hosts.devbox.forwards); edit the file to change it")
	m := mainScreen(t)
	m.eng = e
	m, _ = press(t, m, "p")
	if v := view(m); !strings.Contains(v, "config.toml") {
		t.Errorf("status lacks the error:\n%s", v)
	}
}

func TestConfigBadge(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(5432, "postgres"))
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "1", Host: "devbox", State: engine.Active,
		Spec: engine.ForwardSpec{BindAddr: "127.0.0.1", LocalPort: 5432, RemoteHost: "127.0.0.1", RemotePort: 5432, Pinned: true, Config: true}}})
	if v := view(m); !strings.Contains(v, "cfg") {
		t.Errorf("config forward lacks its badge:\n%s", v)
	}
}

func TestPausedRowShowsReason(t *testing.T) {
	m := connectedWith(newModel(newFakeEngine()), rp(3000, "node"))
	m = apply(m, engine.ForwardChanged{Forward: engine.Forward{ID: "1", Host: "devbox", State: engine.Paused, Pause: engine.PortGone,
		Spec: engine.ForwardSpec{BindAddr: "127.0.0.1", LocalPort: 3000, RemoteHost: "127.0.0.1", RemotePort: 3000}}})
	if v := view(m); !strings.Contains(v, "pause") || !strings.Contains(v, "port closed") {
		t.Errorf("paused row:\n%s", v)
	}
}

func credPrompt(kind sshx.CredentialKind, prompt string) (engine.CredentialPrompt, chan engine.CredentialAnswer) {
	reply := make(chan engine.CredentialAnswer, 1)
	return engine.NewCredentialPrompt("devbox", sshx.CredentialQuestion{Host: "devbox", Kind: kind, Prompt: prompt}, reply), reply
}

func TestCredentialModalMasksAndAnswers(t *testing.T) {
	p, reply := credPrompt(sshx.Password, "alice")
	m := apply(connecting(t), p)
	if v := view(m); !strings.Contains(v, "Password for alice@devbox") {
		t.Fatalf("modal title:\n%s", v)
	}
	m, _ = press(t, m, "h", "q", "2")
	v := view(m)
	if strings.Contains(v, "hq2") || !strings.Contains(v, "•••") {
		t.Errorf("the password isn't masked:\n%s", v)
	}
	buf := m.modal.secret
	m, quit := press(t, m, "enter")
	if quit {
		t.Fatal("q typed in the password field quit pfwd")
	}
	select {
	case a := <-reply:
		if a.Canceled || string(a.Secret) != "hq2" {
			t.Errorf("answer = %+v", a)
		}
	default:
		t.Fatal("enter sent no answer")
	}
	if m.modal.kind != modalNone {
		t.Error("the modal stayed open")
	}
	if string(buf[:cap(buf)]) != strings.Repeat("\x00", cap(buf)) {
		t.Error("the typed password is still in the model's buffer")
	}
}

func TestCredentialModalBackspace(t *testing.T) {
	p, reply := credPrompt(sshx.Passphrase, "/home/alice/.ssh/id_ed25519")
	m := apply(connecting(t), p)
	if v := view(m); !strings.Contains(v, "id_ed25519") {
		t.Errorf("passphrase modal lacks the key:\n%s", v)
	}
	_, _ = press(t, m, "a", "b", "backspace", "c", "enter")
	if a := <-reply; string(a.Secret) != "ac" {
		t.Errorf("answer = %q, want ac", a.Secret)
	}
}

func TestCredentialModalEchoAndServerText(t *testing.T) {
	reply := make(chan engine.CredentialAnswer, 1)
	p := engine.NewCredentialPrompt("devbox", sshx.CredentialQuestion{Host: "bastion", Kind: sshx.Code,
		Prompt: "Passcode or option (1-3): \x1b[31m", Instruction: "Duo two-factor login", Echo: true}, reply)
	m := apply(connecting(t), p)
	m, _ = press(t, m, "1")
	v := view(m)
	for _, want := range []string{"bastion asks", "Duo two-factor login", "Passcode or option (1-3):", "1"} {
		if !strings.Contains(v, want) {
			t.Errorf("modal lacks %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "\ufffd[31m") {
		t.Errorf("the escape in the server's prompt wasn't replaced:\n%s", v)
	}
}

func TestCredentialModalCancel(t *testing.T) {
	p, reply := credPrompt(sshx.Password, "alice")
	m := apply(connecting(t), p)
	m, _ = press(t, m, "x", "esc")
	if a := <-reply; !a.Canceled {
		t.Errorf("esc answered %+v, want a cancel", a)
	}
	if m.modal.kind != modalNone {
		t.Error("the modal stayed open")
	}
}

func TestCredentialRetryNote(t *testing.T) {
	reply := make(chan engine.CredentialAnswer, 1)
	p := engine.NewCredentialPrompt("devbox", sshx.CredentialQuestion{Host: "devbox", Kind: sshx.Password, Prompt: "alice", Retry: true}, reply)
	if v := view(apply(connecting(t), p)); !strings.Contains(v, "didn't work") {
		t.Errorf("a retry prompt should say the last answer failed:\n%s", v)
	}
}

func TestCredentialPromptsQueue(t *testing.T) {
	first, r1 := credPrompt(sshx.Passphrase, "/k1")
	second, r2 := credPrompt(sshx.Password, "alice")
	m := apply(connecting(t), first, second)
	if v := view(m); !strings.Contains(v, "/k1") {
		t.Fatalf("first prompt not shown:\n%s", v)
	}
	m, _ = press(t, m, "enter")
	<-r1
	if v := view(m); !strings.Contains(v, "Password for alice@devbox") {
		t.Errorf("second prompt not shown after the first:\n%s", v)
	}
	_, _ = press(t, m, "esc")
	<-r2
}

func TestCtrlCCancelsCredential(t *testing.T) {
	p, reply := credPrompt(sshx.Password, "alice")
	m := apply(connecting(t), p)
	if _, quit := press(t, m, "ctrl+c"); !quit {
		t.Error("ctrl+c didn't quit")
	}
	if a := <-reply; !a.Canceled {
		t.Errorf("answer = %+v, want a cancel", a)
	}
}

func TestRestoreFailedLogged(t *testing.T) {
	m := apply(mainScreen(t), engine.RestoreFailed{Host: "devbox", Spec: engine.ForwardSpec{RemotePort: 5432, LocalPort: 5432},
		Err: errors.New("local port 5432 is already in use on 127.0.0.1")})
	if v := view(m); !strings.Contains(v, "Couldn't restore :5432") {
		t.Errorf("status lacks the restore failure:\n%s", v)
	}
}
