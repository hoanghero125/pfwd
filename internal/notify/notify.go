// Package notify shows desktop notifications: over D-Bus, or with
// notify-send, on Linux and FreeBSD; with osascript on macOS; and with a
// PowerShell toast on Windows. The text never becomes part of a script, and
// is escaped wherever the platform reads markup.
package notify

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/hoanghero125/pfwd/internal/safetext"
)

// maxText caps the title and the body, in runes.
const maxText = 200

// Notifier shows desktop notifications. It is safe for concurrent use.
type Notifier struct {
	app string
	// command runs a helper program and waits for it; tests replace it.
	command func(ctx context.Context, env []string, name string, args ...string) error
}

// New returns a Notifier that names appName as the sender where the
// platform shows one (D-Bus and notify-send).
func New(appName string) *Notifier {
	return &Notifier{app: appName, command: run}
}

// cleanText replaces control and bidi characters and caps the length.
func cleanText(s string) string {
	s = safetext.Clean(s)
	if r := []rune(s); len(r) > maxText {
		s = string(r[:maxText-1]) + "…"
	}
	return s
}

var markup = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeMarkup escapes the body for notification servers, which read a
// subset of HTML in it (links and images included).
func escapeMarkup(s string) string { return markup.Replace(s) }

// notifySendArgs are notify-send's arguments; "--" keeps a title that
// starts with "-" from being read as an option.
func notifySendArgs(app, title, body string) []string {
	return []string{"--app-name=" + app, "--", title, escapeMarkup(body)}
}

// osascriptArgs runs a fixed AppleScript that takes the title and body as
// arguments, so neither is ever parsed as AppleScript.
func osascriptArgs(title, body string) []string {
	return []string{
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		title, body,
	}
}

// toastScript shows a toast under PowerShell's app ID, which Windows knows
// without any registration. It reads the text from the environment and
// adds it as text nodes, so PowerShell never parses it and it can't break
// the XML.
const toastScript = `$ErrorActionPreference = 'Stop'
try {
  [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
  $xml = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
  $text = $xml.GetElementsByTagName('text')
  $text.Item(0).AppendChild($xml.CreateTextNode($env:PFWD_NOTIFY_TITLE)) > $null
  $text.Item(1).AppendChild($xml.CreateTextNode($env:PFWD_NOTIFY_BODY)) > $null
  $app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
  [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show([Windows.UI.Notifications.ToastNotification]::new($xml))
} catch {
  [Console]::Error.WriteLine($_.Exception.Message)
  exit 1
}
`

// toastCommand returns PowerShell's arguments and the environment that
// carries the text.
func toastCommand(title, body string) (args, env []string) {
	u := utf16.Encode([]rune(toastScript))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	args = []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b)}
	env = []string{"PFWD_NOTIFY_TITLE=" + title, "PFWD_NOTIFY_BODY=" + body}
	return args, env
}

// run starts name with env added to pfwd's own, and waits for it. The
// child's output never reaches the terminal; the start of its stderr goes
// into the error.
func run(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.SysProcAttr = sysProcAttr()
	var stderr capped
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(string(stderr.b)); msg != "" {
			return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, msg)
		}
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return nil
}

// capped keeps the first 512 bytes written to it.
type capped struct{ b []byte }

func (c *capped) Write(p []byte) (int, error) {
	if room := 512 - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
