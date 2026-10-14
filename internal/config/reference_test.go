package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReferenceCoversEverySetting: every key of the config struct is in
// the reference, with a description and the default the code uses.
func TestReferenceCoversEverySetting(t *testing.T) {
	known, err := knownKeys()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := Values(nil)
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, s := range Reference() {
		documented[s.Key] = true
		if !known[s.Key] {
			t.Errorf("the reference has %s, which isn't a setting", s.Key)
		}
		if s.Doc == "" {
			t.Errorf("%s has no description; add a comment to its line in template.go", s.Key)
		}
		want, err := tomlValue(defaults[s.Key])
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Default; got != want && !(got == "[]" && want == "[]") {
			t.Errorf("%s: the reference says %s, the code defaults to %s", s.Key, got, want)
		}
	}
	for k := range known {
		if !documented[k] {
			t.Errorf("%s isn't in the reference; add it to template.go", k)
		}
	}
}

// TestReferenceUpToDate: docs/config.md is what the code generates. Run
// `go generate ./internal/config` after changing a setting.
func TestReferenceUpToDate(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", "docs", "config.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != ReferenceMarkdown() {
		t.Error("docs/config.md is stale; run `go generate ./internal/config`")
	}
}
