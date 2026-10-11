package sshx

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package if a connection, agent or test server leaves
// a goroutine behind.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
