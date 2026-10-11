// Package safetext makes text from untrusted sources safe to print to a
// terminal. Process names, uname output and SSH error messages come from the
// server, and a hostile one could otherwise smuggle escape sequences that
// move the cursor, clear the screen, retitle the window or write the
// clipboard (OSC 52).
package safetext

import (
	"strings"
	"unicode"
)

// Clean replaces control characters (C0 including ESC, DEL and C1) and bidi
// overrides with U+FFFD, and tabs and line breaks with spaces. Invalid UTF-8
// becomes U+FFFD too.
func Clean(s string) string {
	s = strings.ToValidUTF8(s, "\ufffd")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r),
			r >= '\u202a' && r <= '\u202e',
			r >= '\u2066' && r <= '\u2069':
			return '\ufffd'
		}
		return r
	}, s)
}
