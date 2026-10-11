package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLogRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pfwd.log")
	big := bytes.Repeat([]byte("x"), maxLogSize+1)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, closeLog, err := openLogAt(path, "info")
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hello", "port", 8123)
	closeLog()

	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != int64(len(big)) {
		t.Errorf("pfwd.log.1 = %v, %v; want the rotated %d-byte file", info, err, len(big))
	}
	if data, err := os.ReadFile(path + ".2"); err != nil || string(data) != "older" {
		t.Errorf("pfwd.log.2 = %q, %v; want the previous .1", data, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"msg":"hello"`) || len(data) > 1000 {
		t.Errorf("pfwd.log = %q, %v; want one fresh JSON line", data, err)
	}
}

func TestOpenLogKeepsSmallFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pfwd.log")
	if err := os.WriteFile(path, []byte("earlier\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, closeLog, err := openLogAt(path, "debug")
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("visible at debug")
	closeLog()
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "earlier\n") || !strings.Contains(string(data), "visible at debug") {
		t.Errorf("pfwd.log = %q", data)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("a small log was rotated")
	}
}

func TestParseLogLevel(t *testing.T) {
	for _, ok := range []string{"debug", "INFO", "warn", "error", ""} {
		if _, err := parseLogLevel(ok); err != nil {
			t.Errorf("parseLogLevel(%q) = %v", ok, err)
		}
	}
	if _, err := parseLogLevel("loud"); err == nil {
		t.Error("parseLogLevel(loud) accepted")
	}
}
