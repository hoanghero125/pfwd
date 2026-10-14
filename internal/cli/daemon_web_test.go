package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hoanghero125/pfwd/internal/ipc"
	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// call makes one IPC call to the daemon at path.
func call(t *testing.T, path, method string, params, result any) error {
	t.Helper()
	c, err := ipc.Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.Call(t.Context(), method, params, result)
}

func TestDaemonHostsLabelStatsDisconnect(t *testing.T) {
	headlessEnv(t)
	echo := echoListener(t)
	h := newTestHost(t, sshtest.Options{Exec: fakeLinux(ssLine(echo, "echo"))}, true)
	path := startDaemon(t, "-F", h.sshConfig)
	local := freeLocalPort(t)
	if code, _, errOut := run(t, "fwd", "testbox", fmt.Sprintf("%d:%d", local, echo), "--detach"); code != 0 {
		t.Fatalf("pfwd fwd --detach: code %d, stderr %q", code, errOut)
	}

	var hosts struct {
		Hosts []daemonHostJSON `json:"hosts"`
	}
	if err := call(t, path, "hosts", nil, &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts) != 1 || hosts.Hosts[0].Host != "testbox" || hosts.Hosts[0].State != "connected" || hosts.Hosts[0].Forwards != 1 {
		t.Errorf("hosts = %+v", hosts.Hosts)
	}

	var list struct {
		Hosts []hostJSON `json:"hosts"`
	}
	if err := call(t, path, "list", nil, &list); err != nil || len(list.Hosts) != 1 || len(list.Hosts[0].Forwards) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	id := list.Hosts[0].Forwards[0].ID
	if err := call(t, path, "set_label", map[string]string{"id": id, "label": "Echo"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := call(t, path, "set_label", map[string]string{"id": "99", "label": "x"}, nil); !isCode(err, ipc.CodeNotFound) {
		t.Errorf("set_label of an unknown forward: %v", err)
	}
	var stats struct {
		Forwards []statJSON `json:"forwards"`
	}
	if err := call(t, path, "stats", nil, &stats); err != nil || len(stats.Forwards) != 1 || stats.Forwards[0].ID != id {
		t.Errorf("stats = %+v, %v", stats, err)
	}
	_ = call(t, path, "list", nil, &list)
	if got := list.Hosts[0].Forwards[0].Label; got != "Echo" {
		t.Errorf("label after set_label = %q", got)
	}

	if err := call(t, path, "disconnect", map[string]string{"host": "nowhere"}, nil); !isCode(err, ipc.CodeNotFound) {
		t.Errorf("disconnect of an unknown host: %v", err)
	}
	if err := call(t, path, "disconnect", map[string]string{"host": "testbox"}, nil); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		_ = call(t, path, "list", nil, &list)
		if list.Hosts[0].State == "closed" && len(list.Hosts[0].Forwards) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("after disconnect: %+v", list.Hosts)
}

func isCode(err error, code string) bool {
	var e *ipc.Error
	return errors.As(err, &e) && e.Code == code
}

func TestDaemonConfigMethods(t *testing.T) {
	headlessEnv(t)
	cfgPath := writeConfig(t, "# mine\n[ssh]\nuse_openssh_config = false\n\n[ui]\nnotifications = false\n")
	path := startDaemon(t)

	var got configJSON
	if err := call(t, path, "config_get", nil, &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != cfgPath || !strings.Contains(got.Text, "# mine") || got.Values["forward.auto"] != true {
		t.Errorf("config_get = %+v", got)
	}

	var saved configSaveJSON
	if err := call(t, path, "config_set", map[string]any{"values": map[string]any{"forward.auto": false, "detect.ignore_ports": []any{22, 8080}}}, &saved); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	if !saved.Saved || !strings.Contains(string(data), "# mine") || !strings.Contains(string(data), "auto = false") {
		t.Errorf("config_set: %+v:\n%s", saved, data)
	}

	if err := call(t, path, "config_set", map[string]any{"values": map[string]any{"forward.auto_ports": []any{99999}}}, &saved); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfgPath)
	if saved.Saved || len(saved.Problems) == 0 || !strings.Contains(saved.Problems[0].Message, "99999") || string(after) != string(data) {
		t.Errorf("an invalid value: %+v; file changed: %v", saved, string(after) != string(data))
	}
	if err := call(t, path, "config_set", map[string]any{"values": map[string]any{"labels.80": "x"}}, &saved); !isCode(err, ipc.CodeUsage) {
		t.Errorf("a key the form can't set: %v", err)
	}

	if err := call(t, path, "config_put", map[string]string{"text": "[forward]\nauto = 3\n"}, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Saved || len(saved.Problems) != 1 || saved.Problems[0].Line != 2 {
		t.Errorf("config_put of a bad file: %+v", saved)
	}
	if err := call(t, path, "config_put", map[string]string{"text": "[forward]\nauto = true\n"}, &saved); err != nil || !saved.Saved {
		t.Fatalf("config_put: %+v, %v", saved, err)
	}
	if data, _ := os.ReadFile(cfgPath); string(data) != "[forward]\nauto = true\n" {
		t.Errorf("file after config_put: %q", data)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "null") {
		t.Errorf("config_put result has nulls: %s", raw)
	}
}
