// Package state keeps what pfwd remembers between runs, the forwards of
// each host, in state.json. The user never edits the file. Writes are
// atomic (a temporary file renamed over the old one) and guarded by a lock
// file, so several pfwd processes can share it.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Version is the state.json format this pfwd writes.
const Version = 1

// Forward is one remembered forward.
type Forward struct {
	// Kind is "local"; "remote" and "dynamic" arrive with -R and -D.
	Kind       string `json:"kind"`
	Bind       string `json:"bind,omitempty"`
	Local      uint16 `json:"local"`
	RemoteHost string `json:"remote_host"`
	Remote     uint16 `json:"remote"`
	Label      string `json:"label,omitempty"`
	// Pinned forwards are kept when pfwd quits.
	Pinned bool `json:"pinned"`
	// Auto marks a forward auto-forward created.
	Auto bool `json:"auto"`
}

// host is one host's entry.
type host struct {
	LastUsed time.Time `json:"last_used"`
	Forwards []Forward `json:"forwards"`
}

// file is the whole of state.json.
type file struct {
	Version int             `json:"version"`
	Hosts   map[string]host `json:"hosts"`
}

// Options configures a Store.
type Options struct {
	// Now is the clock for last_used; nil means time.Now.
	Now func() time.Time
	// Logger receives notes, such as a corrupt file moved aside; nil
	// discards them.
	Logger *slog.Logger
	// LockTimeout bounds waiting for another pfwd's lock; 0 means 2 s.
	LockTimeout time.Duration
}

// Store reads and writes one state.json. Its methods are safe for
// concurrent use, also by other processes.
type Store struct {
	path string
	o    Options
	mu   sync.Mutex
}

// Open returns a Store for the state file at path. Nothing is read or
// created until it is used.
func Open(path string, o Options) *Store {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.LockTimeout <= 0 {
		o.LockTimeout = 2 * time.Second
	}
	return &Store{path: path, o: o}
}

// Load returns the forwards remembered for host: none when the file or the
// host is missing.
func (s *Store) Load(hostName string) ([]Forward, error) {
	var out []Forward
	err := s.locked(func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		out = f.Hosts[hostName].Forwards
		return nil
	})
	return out, err
}

// HostInfo is what state.json says about one host.
type HostInfo struct {
	// LastUsed is when pfwd last saved the host's forwards.
	LastUsed time.Time
	// Forwards is how many forwards are remembered for it.
	Forwards int
}

// Hosts returns every host state.json knows: none when the file is
// missing.
func (s *Store) Hosts() (map[string]HostInfo, error) {
	out := make(map[string]HostInfo)
	err := s.locked(func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		for name, h := range f.Hosts {
			out[name] = HostInfo{LastUsed: h.LastUsed, Forwards: len(h.Forwards)}
		}
		return nil
	})
	return out, err
}

// Save replaces the forwards remembered for host and records it as used
// now. Other hosts are left as they are.
func (s *Store) Save(hostName string, forwards []Forward) error {
	return s.locked(func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		if forwards == nil {
			forwards = []Forward{}
		}
		f.Hosts[hostName] = host{LastUsed: s.o.Now().UTC(), Forwards: forwards}
		return s.write(f)
	})
}

// locked runs fn holding the lock file, and the Store's mutex for callers
// in this process.
func (s *Store) locked(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create the state directory: %w", err)
	}
	lf, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open the state lock: %w", err)
	}
	defer lf.Close()
	deadline := time.Now().Add(s.o.LockTimeout)
	for {
		ok, err := tryLock(lf)
		if err != nil {
			return fmt.Errorf("lock %s: %w", s.path, err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s is locked by another pfwd for more than %s; try again in a moment (the lock goes away when that pfwd exits)", s.path, s.o.LockTimeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer func() { _ = unlock(lf) }()
	return fn()
}

// read parses the state file. A missing file is empty. A corrupt one is
// renamed to state.json.bad, logged, and treated as empty. A file from a
// newer pfwd is an error, so it is never overwritten.
func (s *Store) read() (file, error) {
	empty := file{Version: Version, Hosts: map[string]host{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return file{}, fmt.Errorf("read the state file: %w", err)
	}
	f, err := migrate(data)
	var newer *newerError
	switch {
	case errors.As(err, &newer):
		return file{}, fmt.Errorf("%s was written by a newer pfwd (format %d, this one reads %d); upgrade pfwd, or move the file away", s.path, newer.version, Version)
	case err != nil:
		bad := s.path + ".bad"
		if rerr := os.Rename(s.path, bad); rerr != nil {
			return file{}, fmt.Errorf("the state file %s is corrupt (%w) and could not be moved aside: %w", s.path, err, rerr)
		}
		s.o.Logger.Warn("state file was corrupt; moved it aside and started empty", "path", s.path, "moved_to", bad, "err", err)
		return empty, nil
	}
	return f, nil
}

type newerError struct{ version int }

func (e *newerError) Error() string { return fmt.Sprintf("state format %d is newer", e.version) }

// migrate parses data and brings it up to Version. Version 0 (no version
// field) has the same layout as 1.
func migrate(data []byte) (file, error) {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return file{}, err
	}
	if head.Version > Version {
		return file{}, &newerError{version: head.Version}
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return file{}, err
	}
	f.Version = Version
	if f.Hosts == nil {
		f.Hosts = map[string]host{}
	}
	return f, nil
}

// write replaces the state file atomically: a temporary file in the same
// directory, synced, then renamed over the old one.
func (s *Store) write(f file) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("write the state file: %w", err)
	}
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write the state file: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write the state file: %w", err)
	}
	return nil
}
