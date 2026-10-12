package sshx

import (
	"io"
	"net"
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package if a connection, agent or test server leaves
// a goroutine behind. Run as `<test binary> pipe HOST PORT` with
// PFWD_SSHX_PIPE=1, it is a ProxyCommand instead: it connects to HOST:PORT
// and copies stdin and stdout, like `nc HOST PORT`.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "pipe" && os.Getenv("PFWD_SSHX_PIPE") == "1" {
		os.Exit(pipe(net.JoinHostPort(os.Args[2], os.Args[3])))
	}
	goleak.VerifyTestMain(m)
}

func pipe(addr string) int {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		_, _ = os.Stderr.WriteString("pipe: " + err.Error() + "\n")
		return 1
	}
	go func() {
		_, _ = io.Copy(c, os.Stdin)
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	_, _ = io.Copy(os.Stdout, c)
	return 0
}
