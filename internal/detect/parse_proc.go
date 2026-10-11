package detect

import (
	"bufio"
	"encoding/hex"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

// tcpListen is the socket state LISTEN in /proc/net/tcp.
const tcpListen = "0A"

// ParseProcNetTCP parses /proc/net/tcp and /proc/net/tcp6 (concatenated)
// and returns the listening sockets. /proc doesn't name the owning process,
// so PID and Process stay empty. Lines it can't parse are skipped.
func ParseProcNetTCP(r io.Reader) ([]RemotePort, error) {
	var out []RemotePort
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != tcpListen {
			continue
		}
		hexAddr, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil || port == 0 {
			continue
		}
		addr, ok := decodeProcAddr(hexAddr)
		if !ok {
			continue
		}
		out = append(out, RemotePort{Port: uint16(port), Addrs: []string{addr.String()}})
	}
	return out, sc.Err()
}

// decodeProcAddr decodes the kernel's hex address: 8 digits for IPv4 and 32
// for IPv6, each 32-bit word in host (little-endian) byte order.
func decodeProcAddr(h string) (netip.Addr, bool) {
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return netip.Addr{}, false
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	if len(b) == 4 {
		return netip.AddrFrom4([4]byte(b)), true
	}
	return netip.AddrFrom16([16]byte(b)).Unmap(), true
}
