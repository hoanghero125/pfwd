//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// testPath is a socket path short enough for macOS's 104-byte limit.
func testPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pfwd") //nolint:usetesting // t.TempDir is too long for a socket path on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d", "pfwd.sock")
}

func TestSocketIsPrivate(t *testing.T) {
	path := serve(t, &fakeHandler{})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %o, want 600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %o, want 700", dir.Mode().Perm())
	}
}

func TestStaleSocketRemoved(t *testing.T) {
	path := testPath(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	// A daemon that died leaves its socket file behind.
	ul, ok := ln.(*net.UnixListener)
	if !ok {
		t.Fatalf("Listen returned a %T", ln)
	}
	ul.SetUnlinkOnClose(false)
	_ = ln.Close()
	ln, err = Listen(path)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	_ = ln.Close()
}

func TestOpenDirectoryRefused(t *testing.T) {
	path := testPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if ln, err := Listen(path); err == nil {
		_ = ln.Close()
		t.Error("Listen in a directory others can open succeeded")
	}
}
