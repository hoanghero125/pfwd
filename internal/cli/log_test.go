package cli

import (
	"bytes"
	"errors"
	"log/slog"
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

// TestLogRedactsSecrets: secret-looking text never reaches the log file,
// even at debug level, in the message or any attribute.
func TestLogRedactsSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pfwd.log")
	logger, closeLog, err := openLogAt(path, "debug")
	if err != nil {
		t.Fatal(err)
	}
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAAkey\n-----END OPENSSH PRIVATE KEY-----"
	logger.Debug("retrying with password=hunter2",
		"url", "http://127.0.0.1:41234/login?token=Tok3nValue",
		"err", errors.New(`bad "passphrase": "s3cret"`),
		slog.Group("g", "secret", "secret=inner1"),
		"key", key)
	closeLog()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"hunter2", "Tok3nValue", "s3cret", "inner1", "AAAAkey"} {
		if strings.Contains(string(data), leaked) {
			t.Errorf("the log kept %q:\n%s", leaked, data)
		}
	}
	if !strings.Contains(string(data), "retrying with") {
		t.Errorf("the log lost the message:\n%s", data)
	}
}
