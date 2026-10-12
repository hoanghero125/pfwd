package config

import (
	"strings"
	"testing"
)

func TestParseForward(t *testing.T) {
	tests := []struct {
		in   string
		want ForwardRule
	}{
		{"5432", ForwardRule{Kind: "local", Local: 5432, Remote: 5432}},
		{"15432:5432", ForwardRule{Kind: "local", Local: 15432, Remote: 5432}},
		{"15432:db.internal:5432", ForwardRule{Kind: "local", Local: 15432, RemoteHost: "db.internal", Remote: 5432}},
		{"0.0.0.0:8080:localhost:3000", ForwardRule{Kind: "local", Bind: "0.0.0.0", Local: 8080, RemoteHost: "localhost", Remote: 3000}},
		{"[::1]:8080:[fe80::2]:3000", ForwardRule{Kind: "local", Bind: "::1", Local: 8080, RemoteHost: "fe80::2", Remote: 3000}},
		{" 8888 ", ForwardRule{Kind: "local", Local: 8888, Remote: 8888}},
		{"-R 9000", ForwardRule{Kind: "remote", Local: 9000, Remote: 9000}},
		{"-R 9000:3000", ForwardRule{Kind: "remote", Local: 3000, Remote: 9000}},
		{"-D 1080", ForwardRule{Kind: "dynamic", Local: 1080}},
	}
	for _, tt := range tests {
		got, err := ParseForward(tt.in)
		tt.want.Raw = tt.in
		if err != nil || got != tt.want {
			t.Errorf("ParseForward(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "0", "70000", "abc", "1:2:3:4:5", "1::3", "-R", "-R 1:2:3", "-D x", "a b:1:c:2", "8080:host name:80", "[::1:8080:h:80"} {
		if _, err := ParseForward(bad); err == nil || !strings.Contains(err.Error(), "invalid forward") {
			t.Errorf("ParseForward(%q) = %v, want an invalid forward error", bad, err)
		}
	}
}

func TestParseSSHForward(t *testing.T) {
	tests := []struct {
		kind, in string
		want     ForwardRule
	}{
		{"localforward", "8080 localhost:80", ForwardRule{Kind: "local", Local: 8080, RemoteHost: "localhost", Remote: 80}},
		{"localforward", "127.0.0.1:15432 db.internal:5432", ForwardRule{Kind: "local", Bind: "127.0.0.1", Local: 15432, RemoteHost: "db.internal", Remote: 5432}},
		{"localforward", "[::1]:8080 [fe80::2]:80", ForwardRule{Kind: "local", Bind: "::1", Local: 8080, RemoteHost: "fe80::2", Remote: 80}},
		{"remoteforward", "9000 localhost:3000", ForwardRule{Kind: "remote", Remote: 9000, RemoteHost: "localhost", Local: 3000}},
		{"remoteforward", "0.0.0.0:9000 127.0.0.1:3000", ForwardRule{Kind: "remote", Bind: "0.0.0.0", Remote: 9000, RemoteHost: "127.0.0.1", Local: 3000}},
		{"dynamicforward", "1080", ForwardRule{Kind: "dynamic", Local: 1080}},
		{"dynamicforward", "127.0.0.1:1080", ForwardRule{Kind: "dynamic", Bind: "127.0.0.1", Local: 1080}},
	}
	for _, tt := range tests {
		got, err := ParseSSHForward(tt.kind, tt.in)
		tt.want.Raw = tt.in
		if err != nil || got != tt.want {
			t.Errorf("ParseSSHForward(%s %q) = %+v, %v; want %+v", tt.kind, tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []struct{ kind, in string }{
		{"localforward", "8080"},
		{"localforward", "/tmp/sock localhost:80"},
		{"localforward", "8080 localhost"},
		{"remoteforward", "9000"},
		{"dynamicforward", "1080 extra"},
		{"dynamicforward", "x"},
		{"bogus", "1"},
	} {
		if _, err := ParseSSHForward(bad.kind, bad.in); err == nil {
			t.Errorf("ParseSSHForward(%s %q) succeeded", bad.kind, bad.in)
		}
	}
}
