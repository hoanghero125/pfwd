package notify

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"go.uber.org/goleak"
)

// TestMain fails the package if a D-Bus connection outlives its call.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// call records one helper program run by a Notifier.
type call struct {
	env  []string
	name string
	args []string
}

// fakeNotifier returns a Notifier whose helper programs are recorded, not
// run, and fail with err.
func fakeNotifier(err error) (*Notifier, *[]call) {
	var calls []call
	n := New("pfwd")
	n.command = func(_ context.Context, env []string, name string, args ...string) error {
		calls = append(calls, call{env: env, name: name, args: args})
		return err
	}
	return n, &calls
}

func TestCleanText(t *testing.T) {
	if got := cleanText("node\x1b]52;c;x\x07 \u202eevil"); strings.ContainsAny(got, "\x1b\x07\u202e") {
		t.Errorf("cleanText kept control or bidi characters: %q", got)
	}
	long := strings.Repeat("é", maxText+10)
	got := cleanText(long)
	if n := utf8.RuneCountInString(got); n != maxText || !strings.HasSuffix(got, "…") {
		t.Errorf("cleanText(%d runes) = %d runes ending %q; want %d ending with an ellipsis", maxText+10, n, got[len(got)-3:], maxText)
	}
	if got := cleanText("vite: localhost:5173"); got != "vite: localhost:5173" {
		t.Errorf("cleanText changed plain text: %q", got)
	}
}

func TestEscapeMarkup(t *testing.T) {
	got := escapeMarkup(`a&b <a href="x">c</a> 'q'`)
	if want := `a&amp;b &lt;a href="x"&gt;c&lt;/a&gt; 'q'`; got != want {
		t.Errorf("escapeMarkup = %q, want %q", got, want)
	}
}

// TestOsascriptArgs: the text travels as arguments, never inside the
// AppleScript source.
func TestOsascriptArgs(t *testing.T) {
	title, body := `x" & (do shell script "id") & "`, `body"`
	args := osascriptArgs(title, body)
	if n := len(args); n < 2 || args[n-2] != title || args[n-1] != body {
		t.Fatalf("osascriptArgs = %q; want the title and body as the last two arguments", args)
	}
	for _, a := range args[:len(args)-2] {
		if strings.Contains(a, "shell") || strings.Contains(a, "body") {
			t.Errorf("script argument %q contains the text", a)
		}
	}
	if !slices.Contains(args, "on run argv") {
		t.Errorf("osascriptArgs = %q; want an `on run argv` script", args)
	}
}

func TestNotifySendArgs(t *testing.T) {
	got := notifySendArgs("pfwd", "-title", "a <b>")
	want := []string{"--app-name=pfwd", "--", "-title", "a &lt;b&gt;"}
	if !slices.Equal(got, want) {
		t.Errorf("notifySendArgs = %q, want %q", got, want)
	}
}

// TestToastCommand: the PowerShell script is a constant and the text
// arrives in environment variables, so PowerShell never parses it.
func TestToastCommand(t *testing.T) {
	title, body := "t $(calc) `n", "b ]]><x/> @\""
	args, env := toastCommand(title, body)
	if len(args) < 2 || args[len(args)-2] != "-EncodedCommand" {
		t.Fatalf("toastCommand args = %q; want -EncodedCommand last", args)
	}
	raw, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("encoded command is not base64 UTF-16: %v", err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(u)); got != toastScript {
		t.Errorf("decoded script = %q, want toastScript", got)
	}
	if strings.Contains(toastScript, "calc") || strings.Contains(toastScript, "]]>") {
		t.Error("the script contains the text")
	}
	want := []string{"PFWD_NOTIFY_TITLE=" + title, "PFWD_NOTIFY_BODY=" + body}
	if !slices.Equal(env, want) {
		t.Errorf("env = %q, want %q", env, want)
	}
}
