package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxHops caps the SSH connections one Dial makes, counting every hop of
// nested ProxyJumps, so a loop in the ssh config ends with an error.
const maxHops = 10

// transport opens the stream hc's SSH connection runs over: a channel
// through via, a channel through the ProxyJump hops, a ProxyCommand's stdin
// and stdout, or plain TCP. under are hop connections to close after it,
// innermost last.
func transport(ctx context.Context, hc *HostConfig, o DialOptions, hops *int, via *Conn, timeout time.Duration) (net.Conn, []io.Closer, error) {
	addr := net.JoinHostPort(hc.HostName, strconv.Itoa(hc.Port))
	switch {
	case via != nil:
		nc, err := via.client.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, nil, &Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
				"jump host %s could not reach %s (%s): %v; check that the jump host can connect to it", via.alias, hc.Alias, addr, err)}
		}
		return nc, nil, nil
	case hc.ProxyJump != "":
		return jumpTransport(ctx, hc, o, hops)
	case hc.ProxyCommand != "":
		pc, err := startProxyCommand(hc)
		if err != nil {
			return nil, nil, err
		}
		return pc, nil, nil
	}
	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	nc, err := d.DialContext(dialCtx, "tcp", addr)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, &Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
			"cannot reach %s (%s): %v; check the host name and port, and that `ssh %s` works", hc.Alias, addr, err, hc.Alias)}
	}
	return nc, nil, nil
}

// jumpTransport connects through each ProxyJump hop in turn and opens a
// channel from the last one to hc. The first hop is dialed with its own
// config, ProxyJump and ProxyCommand included; later hops go through the
// hop before them, as with ssh -J.
func jumpTransport(ctx context.Context, hc *HostConfig, o DialOptions, hops *int) (net.Conn, []io.Closer, error) {
	if o.Resolve == nil {
		return nil, nil, &Error{Kind: ErrConnect, Msg: fmt.Sprintf(
			"%s uses ProxyJump %s, but pfwd was given no way to look up jump hosts", hc.Alias, hc.ProxyJump)}
	}
	hopOpts := o
	hopOpts.Keepalive = Keepalive{} // the target's keepalives cover the whole path
	var under []io.Closer
	fail := func(err error) (net.Conn, []io.Closer, error) {
		closeAll(under)
		return nil, nil, err
	}
	var prev *Conn
	for _, spec := range strings.Split(hc.ProxyJump, ",") {
		user, host, port, err := parseJumpHop(spec)
		if err != nil {
			return fail(&Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
				"ProxyJump %q for %s is not valid: %v; use [user@]host[:port], separated by commas", hc.ProxyJump, hc.Alias, err)})
		}
		target := host
		if user != "" {
			target = user + "@" + host
		}
		hhc, err := o.Resolve(ctx, target)
		if err != nil {
			return fail(&Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
				"could not look up jump host %s for %s: %v", host, hc.Alias, err)})
		}
		if port != 0 {
			hhc.Port = port
		}
		c, err := dialHost(ctx, hhc, hopOpts, hops, prev)
		if err != nil {
			return fail(err)
		}
		under = append(under, c)
		prev = c
	}
	nc, _, err := transport(ctx, hc, o, hops, prev, 0)
	if err != nil {
		return fail(err)
	}
	return nc, under, nil
}

// closeAll closes cs, last first.
func closeAll(cs []io.Closer) {
	for i := len(cs) - 1; i >= 0; i-- {
		_ = cs[i].Close()
	}
}

// parseJumpHop splits one ProxyJump entry: [user@]host[:port], with an
// optional ssh:// prefix and [brackets] around an IPv6 host. port is 0 when
// not given.
func parseJumpHop(s string) (user, host string, port int, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "ssh://")
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		user, s = s[:i], s[i+1:]
	}
	host = s
	portStr, hasPort := "", false
	switch {
	case strings.HasPrefix(s, "["):
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", "", 0, fmt.Errorf("%q has no closing ]", s)
		}
		host = s[1:end]
		if rest := s[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", "", 0, fmt.Errorf("unexpected %q after ]", rest)
			}
			portStr, hasPort = rest[1:], true
		}
	case strings.Count(s, ":") == 1:
		host, portStr, hasPort = strings.Cut(s, ":")
	}
	if host == "" {
		return "", "", 0, errors.New("a hop has no host")
	}
	if hasPort {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return "", "", 0, fmt.Errorf("invalid port %q: use a number from 1 to 65535", portStr)
		}
		port = p
	}
	return user, host, port, nil
}

// expandProxyCommand expands %h, %p, %r, %n and %% in cmd, like OpenSSH. A
// host, user or alias with characters a shell treats specially is refused,
// as OpenSSH 9.6 and later do, so a host name can't inject commands.
func expandProxyCommand(cmd string, hc *HostConfig) (string, error) {
	var b strings.Builder
	put := func(what, v string) error {
		if !shellSafe(v) {
			return fmt.Errorf("the %s %q has characters a shell treats specially, so pfwd won't put it in a ProxyCommand; check the host and user", what, v)
		}
		b.WriteString(v)
		return nil
	}
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '%' {
			b.WriteByte(cmd[i])
			continue
		}
		i++
		if i == len(cmd) {
			return "", errors.New("the ProxyCommand ends with a lone %; write %% for a percent sign")
		}
		var err error
		switch cmd[i] {
		case 'h':
			err = put("host name", hc.HostName)
		case 'p':
			b.WriteString(strconv.Itoa(hc.Port))
		case 'r':
			err = put("user", hc.User)
		case 'n':
			err = put("host alias", hc.Alias)
		case '%':
			b.WriteByte('%')
		default:
			return "", fmt.Errorf("the ProxyCommand has %%%c, which pfwd doesn't expand; use %%h, %%p, %%r, %%n or %%%%", cmd[i])
		}
		if err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// shellSafe reports whether s has only characters no shell treats
// specially: letters, digits and . _ - : @ [ ] % +.
func shellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-:@[]%+", r):
		default:
			return false
		}
	}
	return true
}

// procConn is a net.Conn over a ProxyCommand's stdin and stdout. Closing it
// kills the command and waits for it.
type procConn struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stdout *os.File
	stderr limitedBuffer
	done   chan struct{} // closed when the command has exited
	once   sync.Once
}

// startProxyCommand runs hc's ProxyCommand with its tokens expanded.
func startProxyCommand(hc *HostConfig) (*procConn, error) {
	line, err := expandProxyCommand(hc.ProxyCommand, hc)
	if err != nil {
		return nil, &Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf("ProxyCommand for %s: %v", hc.Alias, err)}
	}
	cmd, err := proxyCommand(line)
	if err != nil {
		return nil, &Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
			"could not run the ProxyCommand for %s: %v; check ProxyCommand in ~/.ssh/config", hc.Alias, err)}
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("proxy command pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, fmt.Errorf("proxy command pipe: %w", err)
	}
	pc := &procConn{cmd: cmd, stdin: inW, stdout: outR, stderr: limitedBuffer{max: 4 << 10}, done: make(chan struct{})}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, &pc.stderr
	cmd.WaitDelay = time.Second
	err = cmd.Start()
	// The command holds its own copies of these ends now.
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, &Error{Kind: ErrConnect, Err: err, Msg: fmt.Sprintf(
			"could not run the ProxyCommand for %s: %v; check ProxyCommand in ~/.ssh/config", hc.Alias, err)}
	}
	go func() {
		_ = cmd.Wait()
		close(pc.done)
	}()
	return pc, nil
}

func (p *procConn) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *procConn) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Close closes the pipes, kills the command and waits for it to exit.
func (p *procConn) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		_ = p.cmd.Process.Kill()
		<-p.done
	})
	return nil
}

func (p *procConn) LocalAddr() net.Addr  { return procAddr{} }
func (p *procConn) RemoteAddr() net.Addr { return procAddr{} }

// SetDeadline works where the OS supports deadlines on pipes; elsewhere it
// fails and the handshake falls back to a timer.
func (p *procConn) SetDeadline(t time.Time) error {
	return errors.Join(p.stdout.SetReadDeadline(t), p.stdin.SetWriteDeadline(t))
}
func (p *procConn) SetReadDeadline(t time.Time) error  { return p.stdout.SetReadDeadline(t) }
func (p *procConn) SetWriteDeadline(t time.Time) error { return p.stdin.SetWriteDeadline(t) }

// said returns what the command wrote to stderr, on one line, once it has
// exited or after a second.
func (p *procConn) said() string {
	select {
	case <-p.done:
	case <-time.After(time.Second):
	}
	return strings.Join(strings.Fields(p.stderr.String()), " ")
}

type procAddr struct{}

func (procAddr) Network() string { return "proxycommand" }
func (procAddr) String() string  { return "proxycommand" }

// limitedBuffer keeps the first max bytes written to it. It is safe for
// concurrent use, since exec writes stderr from its own goroutine.
type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
