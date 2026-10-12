//go:build windows

package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"strings"
	"testing"
)

// testPath is a fresh pipe name.
func testPath(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return `\\.\pipe\pfwd-test-` + hex.EncodeToString(b)
}

func TestDefaultPipeNamesTheUser(t *testing.T) {
	t.Setenv("PFWD_SOCKET", "")
	sid, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	if p := DefaultPath(); !strings.HasPrefix(p, `\\.\pipe\pfwd-`) || !strings.HasSuffix(p, sid) {
		t.Errorf("DefaultPath = %q, want the pipe for %s", p, sid)
	}
}

// TestVerifyServerNeedsAPipe: a connection whose server can't be checked
// is refused.
func TestVerifyServerNeedsAPipe(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if err := verifyServer(a); err == nil {
		t.Error("verifyServer accepted a connection it can't check")
	}
}
