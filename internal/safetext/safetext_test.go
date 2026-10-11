package safetext

import "testing"

func TestClean(t *testing.T) {
	cases := map[string]string{
		"node":                         "node",
		"a\tb\nc\rd":                   "a b c d",
		"x\x1b]52;c;ZQ==\x07y":         "x\ufffd]52;c;ZQ==\ufffdy",
		"\x1b[2J":                      "\ufffd[2J",
		"\u009b2J":                     "\ufffd2J",
		"\x7f":                         "\ufffd",
		"abc\u202edef\u2066g":          "abc\ufffddef\ufffdg",
		"bad \xff utf8":                "bad \ufffd utf8",
		"Café ✓ 日本 \U0001F600 ok":      "Café ✓ 日本 \U0001F600 ok",
		"zero\u200dwidth joiner stays": "zero\u200dwidth joiner stays",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
