package sshx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/hoanghero125/pfwd/internal/sshx/sshtest"
)

// resolverFor resolves aliases from configs, like sshx.Resolve would from
// an ssh config.
func resolverFor(configs map[string]*HostConfig) func(context.Context, string) (*HostConfig, error) {
	return func(_ context.Context, target string) (*HostConfig, error) {
		user, alias, err := SplitTarget(target)
		if err != nil {
			return nil, err
		}
		hc, ok := configs[alias]
		if !ok {
			return nil, fmt.Errorf("unknown host %s", alias)
		}
		cp := *hc
		if user != "" {
			cp.User = user
		}
		return &cp, nil
	}
}

// jumpHost starts a server that trusts f's key, adds it to f's known_hosts,
// and returns it with a config for it.
func jumpHost(t *testing.T, f *fixture, alias string) (*sshtest.Server, *HostConfig) {
	t.Helper()
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{f.key.PublicKey()}})
	kh, err := os.OpenFile(f.knownHosts, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kh.WriteString(srv.KnownHostsLine() + "\n"); err != nil {
		t.Fatal(err)
	}
	kh.Close()
	hc := &HostConfig{
		Alias: alias, HostName: srv.Host(), Port: srv.Port(), User: "tester",
		IdentityFiles: f.hc.IdentityFiles, KnownHostsFiles: f.hc.KnownHostsFiles, StrictHostKeyChecking: "yes",
	}
	return srv, hc
}

func TestDialProxyJump(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	jump, jumpHC := jumpHost(t, f, "jump")
	f.hc.ProxyJump = "jump"
	mustDial(t, f.hc, DialOptions{Resolve: resolverFor(map[string]*HostConfig{"jump": jumpHC})})
	if got, want := jump.Forwarded(), []string{f.srv.Addr}; !reflect.DeepEqual(got, want) {
		t.Errorf("the jump host forwarded %q, want %q", got, want)
	}
}

func TestDialProxyJumpTwoHops(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	first, firstHC := jumpHost(t, f, "first")
	second, secondHC := jumpHost(t, f, "second")
	f.hc.ProxyJump = "first,second"
	mustDial(t, f.hc, DialOptions{Resolve: resolverFor(map[string]*HostConfig{"first": firstHC, "second": secondHC})})
	if got, want := first.Forwarded(), []string{second.Addr}; !reflect.DeepEqual(got, want) {
		t.Errorf("first hop forwarded %q, want %q", got, want)
	}
	if got, want := second.Forwarded(), []string{f.srv.Addr}; !reflect.DeepEqual(got, want) {
		t.Errorf("second hop forwarded %q, want %q", got, want)
	}
}

func TestDialProxyJumpHopsOwnJump(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	outer, outerHC := jumpHost(t, f, "outer")
	inner, innerHC := jumpHost(t, f, "inner")
	// The first hop's own ProxyJump is followed, like OpenSSH does.
	innerHC.ProxyJump = "outer"
	f.hc.ProxyJump = "inner"
	mustDial(t, f.hc, DialOptions{Resolve: resolverFor(map[string]*HostConfig{"outer": outerHC, "inner": innerHC})})
	if got, want := outer.Forwarded(), []string{inner.Addr}; !reflect.DeepEqual(got, want) {
		t.Errorf("outer forwarded %q, want %q", got, want)
	}
	if got, want := inner.Forwarded(), []string{f.srv.Addr}; !reflect.DeepEqual(got, want) {
		t.Errorf("inner forwarded %q, want %q", got, want)
	}
}

func TestDialProxyJumpAsksPerHop(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	_, jumpHC := jumpHost(t, f, "jump")
	if err := os.WriteFile(f.knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	jumpHC.StrictHostKeyChecking = "ask"
	f.hc.ProxyJump = "jump"
	p := &fakePrompter{answer: true}
	mustDial(t, f.hc, DialOptions{Prompter: p, Resolve: resolverFor(map[string]*HostConfig{"jump": jumpHC})})
	var hosts []string
	for _, q := range p.questions() {
		hosts = append(hosts, q.Host)
	}
	if want := []string{"jump", "testhost"}; !reflect.DeepEqual(hosts, want) {
		t.Errorf("asked about %q, want %q", hosts, want)
	}
}

func TestDialProxyJumpPortAndUser(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	jump, jumpHC := jumpHost(t, f, "jump")
	realPort := jumpHC.Port
	jumpHC.Port = 1 // the hop spec's port must win
	jumpHC.User = "nobody"
	f.hc.ProxyJump = "tester@jump:" + strconv.Itoa(realPort)
	mustDial(t, f.hc, DialOptions{Resolve: resolverFor(map[string]*HostConfig{"jump": jumpHC})})
	if len(jump.Forwarded()) != 1 {
		t.Errorf("the jump host forwarded %q, want one channel", jump.Forwarded())
	}
}

func TestDialProxyJumpLoop(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	_, a := jumpHost(t, f, "a")
	_, b := jumpHost(t, f, "b")
	a.ProxyJump, b.ProxyJump = "b", "a"
	f.hc.ProxyJump = "a"
	_, err := Dial(t.Context(), f.hc, DialOptions{Resolve: resolverFor(map[string]*HostConfig{"a": a, "b": b}), dialAgent: noAgent})
	if err == nil || !strings.Contains(err.Error(), "10 hops") {
		t.Fatalf("err = %v, want the 10-hop limit", err)
	}
}

func TestDialProxyJumpWithoutResolver(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	f.hc.ProxyJump = "jump"
	_, err := Dial(t.Context(), f.hc, DialOptions{dialAgent: noAgent})
	if err == nil || !strings.Contains(err.Error(), "jump") {
		t.Fatalf("err = %v, want an error naming the jump host", err)
	}
}

func TestDialProxyJumpHopUnreachable(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	gone := &HostConfig{Alias: "gone", HostName: "127.0.0.1", Port: 1, User: "tester", StrictHostKeyChecking: "yes"}
	f.hc.ProxyJump = "gone"
	_, err := Dial(t.Context(), f.hc, DialOptions{Resolve: resolverFor(map[string]*HostConfig{"gone": gone}), dialAgent: noAgent})
	if !errors.Is(err, ErrConnect) || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v, want ErrConnect naming the hop", err)
	}
}

// pipeCommand returns a ProxyCommand that runs this test binary as nc.
func pipeCommand(t *testing.T, args string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFWD_SSHX_PIPE", "1")
	return `"` + exe + `" pipe ` + args
}

func TestDialProxyCommand(t *testing.T) {
	f := newFixture(t, sshtest.Options{Exec: echoExec})
	f.hc.ProxyCommand = pipeCommand(t, "%h %p")
	c := mustDial(t, f.hc, DialOptions{})
	out, err := c.Output(t.Context(), "hello")
	if err != nil || string(out) != "ran hello\n" {
		t.Fatalf("Output = %q, %v", out, err)
	}
}

func TestDialProxyCommandFailureShowsStderr(t *testing.T) {
	f := newFixture(t, sshtest.Options{})
	f.hc.ProxyCommand = pipeCommand(t, "127.0.0.1 1")
	_, err := Dial(t.Context(), f.hc, DialOptions{dialAgent: noAgent})
	if err == nil || !strings.Contains(err.Error(), "pipe:") || !strings.Contains(err.Error(), "ProxyCommand") {
		t.Fatalf("err = %v, want the ProxyCommand's stderr in it", err)
	}
}

func TestExpandProxyCommand(t *testing.T) {
	hc := &HostConfig{Alias: "devbox", HostName: "10.0.0.5", Port: 2222, User: "bob"}
	tests := []struct {
		cmd, want string
	}{
		{"nc %h %p", "nc 10.0.0.5 2222"},
		{"ssh -W %h:%p %r@bastion # %n %%", "ssh -W 10.0.0.5:2222 bob@bastion # devbox %"},
		{"no expansions", "no expansions"},
	}
	for _, tt := range tests {
		got, err := expandProxyCommand(tt.cmd, hc)
		if err != nil || got != tt.want {
			t.Errorf("expand(%q) = %q, %v; want %q", tt.cmd, got, err, tt.want)
		}
	}
	for _, bad := range []string{"nc %x", "nc %h %"} {
		if _, err := expandProxyCommand(bad, hc); err == nil {
			t.Errorf("expand(%q) succeeded, want an error", bad)
		}
	}
	v6 := &HostConfig{Alias: "v6", HostName: "fe80::1%eth0", Port: 22, User: "bob"}
	if got, err := expandProxyCommand("nc %h %p", v6); err != nil || got != "nc fe80::1%eth0 22" {
		t.Errorf("IPv6 with zone = %q, %v", got, err)
	}
	for _, field := range []string{"host", "user", "alias"} {
		for _, evil := range []string{"a;id", "$(id)", "a`id`", `a"b`, "a'b", "a b", "a\nb", "a|b", "a&b", "a>b"} {
			h := *hc
			switch field {
			case "host":
				h.HostName = evil
			case "user":
				h.User = evil
			case "alias":
				h.Alias = evil
			}
			if _, err := expandProxyCommand("nc %h %p %r %n", &h); err == nil {
				t.Errorf("%s %q was expanded, want it refused", field, evil)
			}
		}
	}
}

func TestParseJumpHop(t *testing.T) {
	tests := []struct {
		in         string
		user, host string
		port       int
	}{
		{"bastion", "", "bastion", 0},
		{"bob@bastion", "bob", "bastion", 0},
		{"bob@bastion:2222", "bob", "bastion", 2222},
		{"bastion:2222", "", "bastion", 2222},
		{"[fe80::1]:2222", "", "fe80::1", 2222},
		{"ssh://bob@bastion:2222", "bob", "bastion", 2222},
		{" bastion ", "", "bastion", 0},
	}
	for _, tt := range tests {
		user, host, port, err := parseJumpHop(tt.in)
		if err != nil || user != tt.user || host != tt.host || port != tt.port {
			t.Errorf("parseJumpHop(%q) = %q %q %d %v", tt.in, user, host, port, err)
		}
	}
	for _, bad := range []string{"", "bastion:0", "bastion:99999", "bastion:x"} {
		if _, _, _, err := parseJumpHop(bad); err == nil {
			t.Errorf("parseJumpHop(%q) succeeded", bad)
		}
	}
}
