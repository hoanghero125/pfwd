//go:build linux || freebsd

package notify

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// noBus points the session bus at nothing, and puts a dbus-launch on PATH
// that leaves a marker if anything runs it.
func noBus(t *testing.T) (marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "launched")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(filepath.Join(dir, "dbus-launch"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return marker
}

func TestNotifyFallsBackToNotifySend(t *testing.T) {
	marker := noBus(t)
	n, calls := fakeNotifier(nil)
	if err := n.Notify(t.Context(), "Forwarded :3000", "a <b>"); err != nil {
		t.Fatal(err)
	}
	want := call{name: "notify-send", args: []string{"--app-name=pfwd", "--", "Forwarded :3000", "a &lt;b&gt;"}}
	if len(*calls) != 1 || (*calls)[0].name != want.name || !slices.Equal((*calls)[0].args, want.args) {
		t.Errorf("calls = %+v; want %+v", *calls, want)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("Notify ran dbus-launch")
	}
}

func TestNotifyReportsBothFailures(t *testing.T) {
	noBus(t)
	n, _ := fakeNotifier(exec.ErrNotFound)
	err := n.Notify(t.Context(), "t", "b")
	if err == nil || !strings.Contains(err.Error(), "notify-send") || !strings.Contains(err.Error(), "D-Bus") {
		t.Errorf("Notify = %v; want an error naming D-Bus and notify-send", err)
	}
}

// fakeServer implements org.freedesktop.Notifications.Notify.
type fakeServer struct{ got chan []any }

func (s fakeServer) Notify(app string, id uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	s.got <- []any{app, id, icon, summary, body, len(actions), len(hints), timeout}
	return 7, nil
}

// TestNotifyOverDBus runs a private dbus-daemon with a fake notification
// server on it. It needs dbus-daemon, so it skips without one.
func TestNotifyOverDBus(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is not installed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, daemon, "--session", "--nofork", "--nopidfile", "--print-address=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("read the bus address: %v", err)
	}
	addr = strings.TrimSpace(addr)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)

	srv, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	fake := fakeServer{got: make(chan []any, 1)}
	if err := srv.Export(fake, "/org/freedesktop/Notifications", "org.freedesktop.Notifications"); err != nil {
		t.Fatal(err)
	}
	if reply, err := srv.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName = %v, %v", reply, err)
	}

	n, calls := fakeNotifier(errors.New("notify-send must not run"))
	if err := n.Notify(ctx, "Forwarded :3000", "vite & co: localhost:3000"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fake.got:
		want := []any{"pfwd", uint32(0), "", "Forwarded :3000", "vite &amp; co: localhost:3000", 0, 0, int32(-1)}
		if !slices.Equal(got, want) {
			t.Errorf("server got %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server got no notification")
	}
	if len(*calls) != 0 {
		t.Errorf("notify-send ran although D-Bus worked: %+v", *calls)
	}
}

func TestSessionBusAddress(t *testing.T) {
	dir := t.TempDir()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got, err := sessionBusAddress(env(map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/x"})); err != nil || got != "unix:path=/x" {
		t.Errorf("from the environment: %q, %v", got, err)
	}
	if _, err := sessionBusAddress(env(map[string]string{"XDG_RUNTIME_DIR": dir})); err == nil {
		t.Error("found a bus in a runtime directory without one")
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "bus"))
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	defer ln.Close()
	want := "unix:path=" + filepath.Join(dir, "bus")
	if got, err := sessionBusAddress(env(map[string]string{"XDG_RUNTIME_DIR": dir})); err != nil || got != want {
		t.Errorf("from XDG_RUNTIME_DIR: %q, %v; want %q", got, err, want)
	}
	if _, err := sessionBusAddress(env(nil)); err == nil {
		t.Error("found a bus with neither variable set")
	}
}

// TestNotifyDoesNotGuessTheBus: godbus's own lookup tries
// /run/user/<uid>/bus and then sets DBUS_SESSION_BUS_ADDRESS for the whole
// process. Notify looks only where the environment points, and never
// changes it. It needs dbus-daemon and a writable /run/user, as in CI's
// containers.
func TestNotifyDoesNotGuessTheBus(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is not installed")
	}
	dir := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	sock := filepath.Join(dir, "bus")
	if _, err := os.Stat(sock); err == nil {
		t.Skip("a session bus already runs at " + sock)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Skipf("can't create %s: %v", dir, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, daemon, "--session", "--nofork", "--nopidfile", "--address=unix:path="+sock, "--print-address=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait(); _ = os.Remove(sock) })
	if _, err := bufio.NewReader(out).ReadString('\n'); err != nil {
		t.Fatalf("dbus-daemon did not start: %v", err)
	}

	noBus(t)
	n, calls := fakeNotifier(nil)
	if err := n.Notify(t.Context(), "t", "b"); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); got != "" {
		t.Errorf("Notify set DBUS_SESSION_BUS_ADDRESS to %q", got)
	}
	if len(*calls) != 1 || (*calls)[0].name != "notify-send" {
		t.Errorf("calls = %+v; want the notify-send fallback, not the guessed bus", *calls)
	}
}
