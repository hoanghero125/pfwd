package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// StateDir returns the directory for pfwd's runtime state and log:
// $XDG_STATE_HOME/pfwd (default ~/.local/state/pfwd) on Linux and other
// Unix systems, ~/Library/Application Support/pfwd on macOS, and
// %APPDATA%\pfwd on Windows. It does not create the directory.
func StateDir() (string, error) {
	return stateDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func stateDir(goos string, getenv func(string) string, home func() (string, error)) (string, error) {
	if goos == "windows" {
		if appData := getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "pfwd"), nil
		}
	}
	if goos != "windows" && goos != "darwin" {
		// The XDG spec ignores relative paths.
		if xdg := getenv("XDG_STATE_HOME"); filepath.IsAbs(xdg) {
			return filepath.Join(xdg, "pfwd"), nil
		}
	}
	h, err := home()
	if err != nil {
		return "", fmt.Errorf("find the home directory for pfwd's state: %w", err)
	}
	switch goos {
	case "windows":
		return filepath.Join(h, "AppData", "Roaming", "pfwd"), nil
	case "darwin":
		return filepath.Join(h, "Library", "Application Support", "pfwd"), nil
	default:
		return filepath.Join(h, ".local", "state", "pfwd"), nil
	}
}
