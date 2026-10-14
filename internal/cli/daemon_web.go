package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/ipc"
)

// The daemon calls the web UI uses, on top of the CLI's.

// daemonHostJSON is a host in the hosts call: one from ssh config or
// state.json, or one the daemon is connected to.
type daemonHostJSON struct {
	Host string `json:"host"`
	// State is the daemon's session state, empty when it has none.
	State    string     `json:"state"`
	LastUsed *time.Time `json:"last_used"`
	// Remembered counts the forwards state.json keeps; Forwards the ones
	// running in the daemon.
	Remembered int `json:"remembered"`
	Forwards   int `json:"forwards"`
}

// statJSON is one forward's counters in the stats call.
type statJSON struct {
	ID       string `json:"id"`
	Conns    int64  `json:"conns"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

// configJSON is the config file: where it is, its text and the settings
// the form shows.
type configJSON struct {
	Path   string         `json:"path"`
	Text   string         `json:"text"`
	Values map[string]any `json:"values"`
}

// configSaveJSON answers config_put and config_set: saved, or the problems
// that kept the file from being saved.
type configSaveJSON struct {
	Saved    bool           `json:"saved"`
	Problems []problemJSON  `json:"problems"`
	Values   map[string]any `json:"values,omitempty"`
}

type labelParams struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type configTextParams struct {
	Text string `json:"text"`
}

type configValuesParams struct {
	Values map[string]any `json:"values"`
}

// hosts lists ssh config's hosts and the daemon's, most recently used first.
func (h *daemonHandler) hostList() (any, error) {
	rows, err := listHosts(h.d.f.sshConfig)
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeUsage, "%s", err.Error())
	}
	live := map[engine.HostID]int{}
	for _, f := range h.eng.Forwards() {
		live[f.Host]++
	}
	h.mu.Lock()
	known := slices.Clone(h.hosts)
	states := make(map[engine.HostID]string, len(known))
	for _, host := range known {
		states[host] = h.sessions[host].State.String()
	}
	h.mu.Unlock()
	out := []daemonHostJSON{}
	listed := map[string]bool{}
	for _, r := range rows {
		j := daemonHostJSON{Host: r.Host, State: states[engine.HostID(r.Host)], Remembered: r.Forwards, Forwards: live[engine.HostID(r.Host)]}
		if !r.LastUsed.IsZero() {
			j.LastUsed = &r.LastUsed
		}
		listed[r.Host] = true
		out = append(out, j)
	}
	for _, host := range known {
		if !listed[string(host)] {
			out = append(out, daemonHostJSON{Host: string(host), State: states[host], Forwards: live[host]})
		}
	}
	return map[string]any{"hosts": out}, nil
}

// disconnect ends a host's connection.
func (h *daemonHandler) disconnect(host engine.HostID) (any, error) {
	h.mu.Lock()
	known := slices.Contains(h.hosts, host)
	h.mu.Unlock()
	if !known {
		return nil, ipc.Errorf(ipc.CodeNotFound, "the daemon isn't connected to %s", host)
	}
	h.eng.Disconnect(host)
	return map[string]bool{"ok": true}, nil
}

// stats returns every forward's counters.
func (h *daemonHandler) stats() any {
	out := []statJSON{}
	for _, f := range h.eng.Forwards() {
		out = append(out, statJSON{ID: f.ID, Conns: f.Stats.Conns, BytesIn: f.Stats.BytesIn, BytesOut: f.Stats.BytesOut})
	}
	return map[string]any{"forwards": out}
}

// readConfig returns the config file's text; a missing file is empty.
func (h *daemonHandler) readConfig() ([]byte, error) {
	data, err := os.ReadFile(h.cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeInternal, "read %s: %v", h.cfgPath, err)
	}
	return data, nil
}

func (h *daemonHandler) configGet() (any, error) {
	data, err := h.readConfig()
	if err != nil {
		return nil, err
	}
	values, err := config.Values(data)
	if err != nil {
		// A file that can't be parsed still opens in the raw editor; the
		// form shows the defaults.
		values, _ = config.Values(nil)
	}
	return configJSON{Path: h.cfgPath, Text: string(data), Values: values}, nil
}

// configSave validates text and, when it is valid, writes it as the config
// file. The daemon uses it after a restart.
func (h *daemonHandler) configSave(text []byte) (any, error) {
	if _, err := config.Parse(h.cfgPath, text); err != nil {
		var p *config.Problems
		if !errors.As(err, &p) {
			return nil, ipc.Errorf(ipc.CodeInternal, "%s", err.Error())
		}
		out := configSaveJSON{Problems: []problemJSON{}}
		for _, q := range p.List {
			out.Problems = append(out.Problems, problemJSON{Line: q.Line, Col: q.Col, Key: q.Key, Message: q.Msg})
		}
		return out, nil
	}
	if err := writeAtomic(h.cfgPath, text); err != nil {
		return nil, ipc.Errorf(ipc.CodeInternal, "%s", err.Error())
	}
	h.log.Info("config saved", "path", h.cfgPath)
	values, _ := config.Values(text)
	return configSaveJSON{Saved: true, Problems: []problemJSON{}, Values: values}, nil
}

func (h *daemonHandler) configSet(values map[string]any) (any, error) {
	data, err := h.readConfig()
	if err != nil {
		return nil, err
	}
	out, err := config.SetValues(data, values)
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeUsage, "%s", err.Error())
	}
	return h.configSave(out)
}

// writeAtomic replaces path with data through a temporary file in the same
// directory. A new file is private to the user; an existing one keeps its
// mode.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}
