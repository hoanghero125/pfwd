package config

import (
	"strings"
	"testing"
)

const editBase = `# my settings
[detect]
interval = "2s" # fast enough
ignore_ports = [22,
  2222]

[forward]
auto = true   # on by default
`

func TestSetValuesReplacesAndKeepsComments(t *testing.T) {
	out, err := SetValues([]byte(editBase), map[string]any{"forward.auto": false, "detect.interval": "5s"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"# my settings", "auto = false   # on by default", `interval = "5s" # fast enough`, "2222]"} {
		if !strings.Contains(s, want) {
			t.Errorf("result lacks %q:\n%s", want, s)
		}
	}
	cfg, err := Parse("config.toml", out)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Forward.Auto || cfg.Detect.Interval.String() != "5s" {
		t.Errorf("parsed %+v %+v", cfg.Forward, cfg.Detect)
	}
}

func TestSetValuesArray(t *testing.T) {
	out, err := SetValues([]byte(editBase), map[string]any{"detect.ignore_ports": []any{float64(22), "8000-8100"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse("config.toml", out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := cfg.Detect.IgnorePorts; len(got) != 2 || got[0].String() != "22" || got[1].String() != "8000-8100" {
		t.Errorf("ignore_ports = %v:\n%s", got, out)
	}
	if strings.Contains(string(out), "2222") {
		t.Errorf("the old multi-line value is left over:\n%s", out)
	}
}

func TestSetValuesAddsKeyToTable(t *testing.T) {
	out, err := SetValues([]byte(editBase), map[string]any{"forward.on_conflict": "next"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse("config.toml", out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if cfg.Forward.OnConflict != "next" || !cfg.Forward.Auto {
		t.Errorf("forward = %+v:\n%s", cfg.Forward, out)
	}
}

func TestSetValuesAddsTable(t *testing.T) {
	out, err := SetValues([]byte(editBase), map[string]any{"ui.notifications": false})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse("config.toml", out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if cfg.UI.Notifications {
		t.Errorf("ui.notifications still on:\n%s", out)
	}
	out, err = SetValues(nil, map[string]any{"forward.auto": false})
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := Parse("config.toml", out); err != nil || cfg.Forward.Auto {
		t.Errorf("from an empty file: %+v %v:\n%s", cfg.Forward, err, out)
	}
}

func TestSetValuesRefusesUnknownKeys(t *testing.T) {
	for _, key := range []string{"forward.nope", "labels.8000", "hosts.devbox.auto", "auto", "forward"} {
		if _, err := SetValues([]byte(editBase), map[string]any{key: true}); err == nil {
			t.Errorf("%s accepted", key)
		}
	}
}

func TestParseReportsProblems(t *testing.T) {
	_, err := Parse("config.toml", []byte("[forward]\nauto = 3\n"))
	if err == nil || !strings.Contains(err.Error(), "config.toml:2") {
		t.Errorf("Parse = %v", err)
	}
}
