//go:build windows

package sshx

import (
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestAgentPipeDeniesImpersonation pins the fix for pipe squatting: when the
// OpenSSH agent service is stopped, any local user can create
// \\.\pipe\openssh-ssh-agent. pfwd must open agent pipes so the pipe server
// can at most identify it, never impersonate it.
func TestAgentPipeDeniesImpersonation(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\pfwd-test-%d`, time.Now().UnixNano())
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	server, err := windows.CreateNamedPipe(p, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatalf("CreateNamedPipe: %v", err)
	}
	defer func() { _ = windows.CloseHandle(server) }()

	client, err := dialAgentPath(name)
	if err != nil {
		t.Fatalf("dialAgentPath: %v", err)
	}
	defer client.Close()
	if err := windows.ConnectNamedPipe(server, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		t.Fatalf("ConnectNamedPipe: %v", err)
	}
	// The server has to read from the pipe before it can impersonate.
	if _, err := client.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	var n uint32
	if err := windows.ReadFile(server, make([]byte, 1), &n, nil); err != nil {
		t.Fatal(err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	impersonate := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")
	if r, _, err := impersonate.Call(uintptr(server)); r == 0 {
		t.Fatalf("ImpersonateNamedPipeClient: %v", err)
	}
	defer func() {
		if err := windows.RevertToSelf(); err != nil {
			t.Fatal(err)
		}
	}()
	var tok windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok); err != nil {
		t.Fatalf("OpenThreadToken: %v", err)
	}
	defer tok.Close()
	var level, size uint32
	if err := windows.GetTokenInformation(tok, windows.TokenImpersonationLevel, (*byte)(unsafe.Pointer(&level)), uint32(unsafe.Sizeof(level)), &size); err != nil {
		t.Fatalf("GetTokenInformation: %v", err)
	}
	if level != windows.SecurityIdentification {
		t.Errorf("pipe server got impersonation level %d, want SecurityIdentification (%d)", level, windows.SecurityIdentification)
	}
}
