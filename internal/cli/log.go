package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoanghero125/pfwd/internal/config"
)

// The log rotates at startup once it passes maxLogSize, keeping
// keptLogs older files (pfwd.log.1 is the newest).
const (
	maxLogSize = 5 << 20
	keptLogs   = 3
)

// openLog opens <state dir>/pfwd.log. The TUI owns the terminal, so this is
// where everything else goes.
func openLog(level string) (*slog.Logger, func(), error) {
	dir, err := config.StateDir()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create the state directory %s: %w", dir, err)
	}
	return openLogAt(filepath.Join(dir, "pfwd.log"), level)
}

func openLogAt(path, level string) (*slog.Logger, func(), error) {
	lvl, err := parseLogLevel(level)
	if err != nil {
		return nil, nil, err
	}
	rotateLog(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open the log file: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: lvl, ReplaceAttr: redactAttr}))
	return logger, func() { _ = f.Close() }, nil
}

// redactAttr takes anything that looks like a secret out of a log line's
// message and attributes, at every level: passwords, passphrases, tokens
// (the web UI's login link carries one) and private keys.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	switch v := a.Value; v.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(redact(v.String(), ""))
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			a.Value = slog.StringValue(redact(x.Error(), ""))
		case fmt.Stringer:
			a.Value = slog.StringValue(redact(x.String(), ""))
		}
	}
	return a
}

// rotateLog shifts pfwd.log to pfwd.log.1 (and so on) when it is too big.
// Failures are ignored: an unrotated log is better than no pfwd.
func rotateLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxLogSize {
		return
	}
	for i := keptLogs - 1; i >= 1; i-- {
		older := fmt.Sprintf("%s.%d", path, i)
		if err := os.Rename(older, fmt.Sprintf("%s.%d", path, i+1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return
		}
	}
	_ = os.Rename(path, path+".1")
}

// parseLogLevel accepts debug, info, warn and error; empty means info.
func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("invalid log level %q: use debug, info, warn or error", s)
}
