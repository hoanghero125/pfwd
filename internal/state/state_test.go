package state

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var t0 = time.Date(2026, 10, 13, 9, 12, 0, 0, time.UTC)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pfwd", "state.json")
	return Open(path, Options{Now: func() time.Time { return t0 }}), path
}

var sample = []Forward{
	{Kind: "local", Local: 5173, RemoteHost: "127.0.0.1", Remote: 5173, Label: "Vite", Auto: true},
	{Kind: "local", Bind: "0.0.0.0", Local: 15432, RemoteHost: "db.internal", Remote: 5432, Pinned: true},
}

func TestRoundTrip(t *testing.T) {
	s, path := newStore(t)
	if err := s.Save("devbox", sample); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("devbox")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sample) {
		t.Errorf("Load = %+v, want %+v", got, sample)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"version": 1`, `"last_used": "2026-10-13T09:12:00Z"`, `"remote_host": "db.internal"`, `"pinned": true`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("state.json lacks %s:\n%s", want, data)
		}
	}
}

func TestLoadMissing(t *testing.T) {
	s, path := newStore(t)
	got, err := s.Load("devbox")
	if err != nil || got != nil {
		t.Errorf("Load = %v, %v; want nothing", got, err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("Load created the state file")
	}
}

func TestHostsDontClobber(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Save("a", sample[:1]); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("b", sample[1:]); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load("a"); !reflect.DeepEqual(got, sample[:1]) {
		t.Errorf("host a = %+v after saving b", got)
	}
}

func TestSaveEmptyRemovesForwards(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Save("a", sample); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("a", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load("a"); len(got) != 0 {
		t.Errorf("Load = %+v, want none", got)
	}
}

func TestConcurrentSaves(t *testing.T) {
	s, path := newStore(t)
	other := Open(path, Options{})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = other
			}
			if err := store.Save(fmt.Sprintf("host%d", i), sample); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := range 20 {
		if got, err := s.Load(fmt.Sprintf("host%d", i)); err != nil || len(got) != len(sample) {
			t.Errorf("host%d = %v, %v; a concurrent write was lost", i, got, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" && e.Name() != "state.json.lock" {
			t.Errorf("leftover file %s", e.Name())
		}
	}
}

func TestCorruptFileMovedAside(t *testing.T) {
	var logs bytes.Buffer
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(path, Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if got, err := s.Load("devbox"); err != nil || got != nil {
		t.Fatalf("Load = %v, %v; want an empty start", got, err)
	}
	if bad, err := os.ReadFile(path + ".bad"); err != nil || string(bad) != "{not json" {
		t.Errorf("state.json.bad = %q, %v; want the corrupt file kept", bad, err)
	}
	if !strings.Contains(logs.String(), "state.json.bad") {
		t.Errorf("the move wasn't logged:\n%s", logs.String())
	}
	if err := s.Save("devbox", sample); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load("devbox"); !reflect.DeepEqual(got, sample) {
		t.Errorf("Load after the reset = %+v", got)
	}
}

func TestNewerVersionRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	future := `{"version": 2, "hosts": {}}`
	if err := os.WriteFile(path, []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(path, Options{})
	if _, err := s.Load("devbox"); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("Load err = %v, want one about a newer pfwd", err)
	}
	if err := s.Save("devbox", sample); err == nil {
		t.Error("Save overwrote a file from a newer pfwd")
	}
	if data, _ := os.ReadFile(path); string(data) != future {
		t.Errorf("the newer file changed: %s", data)
	}
}

func TestVersionZeroMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"hosts": {"devbox": {"forwards": [{"kind": "local", "local": 8888, "remote_host": "127.0.0.1", "remote": 8888, "pinned": true}]}}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Open(path, Options{}).Load("devbox")
	if err != nil || len(got) != 1 || got[0].Remote != 8888 || !got[0].Pinned {
		t.Errorf("Load = %+v, %v", got, err)
	}
}

func TestFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix modes")
	}
	s, path := newStore(t)
	if err := s.Save("a", sample); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + ".lock"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(p), fi.Mode().Perm())
		}
	}
}

func TestLockTimeoutAdvice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if ok, err := tryLock(lf); !ok || err != nil {
		t.Fatalf("tryLock = %v, %v", ok, err)
	}
	defer func() { _ = unlock(lf) }()
	err = Open(path, Options{LockTimeout: 100 * time.Millisecond}).Save("devbox", sample)
	if err == nil || !strings.Contains(err.Error(), "another pfwd") || strings.Contains(err.Error(), "delete") {
		t.Errorf("err = %v; want it to name the other pfwd, without advising to delete the lock file (the OS frees it when that pfwd exits)", err)
	}
}

func TestStoreHosts(t *testing.T) {
	s, _ := newStore(t)
	if got, err := s.Hosts(); err != nil || len(got) != 0 {
		t.Fatalf("Hosts without a file = %v, %v; want none", got, err)
	}
	if err := s.Save("devbox", sample); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("gpu", nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Hosts()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]HostInfo{"devbox": {LastUsed: t0, Forwards: 2}, "gpu": {LastUsed: t0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Hosts = %+v, want %+v", got, want)
	}
}
