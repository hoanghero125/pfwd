package forward

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Handshake reads what a client wants before a forward dials: the address
// to dial, and a reply function the forward calls with the dial's result.
// The forward closes the client when it returns an error.
type Handshake func(c net.Conn) (target string, reply func(dialErr error) error, err error)

// SOCKS5 returns the handshake of a SOCKS5 proxy: no authentication, the
// CONNECT command only, and IPv4, IPv6 and domain-name targets. Domain
// names are passed on unresolved, so the far side of the tunnel resolves
// them, and internal names work.
func SOCKS5() Handshake { return socks5 }

func socks5(c net.Conn) (string, func(error) error, error) {
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return "", nil, fmt.Errorf("read the SOCKS greeting: %w", err)
	}
	if head[0] != 5 {
		return "", nil, fmt.Errorf("not a SOCKS5 client (version %d)", head[0])
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", nil, fmt.Errorf("read the SOCKS methods: %w", err)
	}
	if !bytes.Contains(methods, []byte{0}) {
		_, _ = c.Write([]byte{5, 0xff})
		return "", nil, errors.New("the SOCKS client wants authentication, which pfwd doesn't offer")
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return "", nil, err
	}

	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil {
		return "", nil, fmt.Errorf("read the SOCKS request: %w", err)
	}
	fail := func(code byte, err error) (string, func(error) error, error) {
		_ = writeReply(c, code)
		return "", nil, err
	}
	if req[0] != 5 {
		return "", nil, fmt.Errorf("not a SOCKS5 request (version %d)", req[0])
	}
	if req[1] != 1 {
		return fail(0x07, fmt.Errorf("SOCKS command %d is not supported; only CONNECT is", req[1]))
	}
	var host string
	switch req[3] {
	case 1, 4:
		size := net.IPv4len
		if req[3] == 4 {
			size = net.IPv6len
		}
		ip := make([]byte, size)
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", nil, err
		}
		host = net.IP(ip).String()
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return "", nil, err
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return "", nil, err
		}
		if len(name) == 0 {
			return fail(0x01, errors.New("empty SOCKS host name"))
		}
		host = string(name)
	default:
		return fail(0x08, fmt.Errorf("SOCKS address type %d is not supported", req[3]))
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		return "", nil, err
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	return target, func(err error) error { return writeReply(c, replyCode(err)) }, nil
}

// writeReply sends a SOCKS5 reply with an empty bound address.
func writeReply(c net.Conn, code byte) error {
	_, err := c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

// replyCode maps a dial error to a SOCKS5 reply: 0 success, 2 the server
// forbids forwarding, 5 refused, 4 unreachable, 1 anything else.
func replyCode(err error) byte {
	if err == nil {
		return 0
	}
	var oce *ssh.OpenChannelError
	if errors.As(err, &oce) {
		switch {
		case oce.Reason == ssh.Prohibited:
			return 0x02
		case strings.Contains(strings.ToLower(oce.Message), "refused"):
			return 0x05
		case oce.Reason == ssh.ConnectionFailed:
			return 0x04
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 0x04
	}
	return 0x01
}
