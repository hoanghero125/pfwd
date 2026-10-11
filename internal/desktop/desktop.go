// Package desktop opens URLs in the browser and copies text to the system
// clipboard by running the platform's own helper programs. The helpers get
// no terminal: their output goes nowhere and, on Unix, they start in a new
// session.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// helperTimeout bounds one run of a helper program.
const helperTimeout = 5 * time.Second

// OpenURL opens rawURL, an http or https URL on a local port such as
// http://localhost:3000, in the default browser.
func OpenURL(ctx context.Context, rawURL string) error {
	if err := checkURL(rawURL); err != nil {
		return err
	}
	h, err := hostSys().openCommand(rawURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	return open(ctx, h)
}

// Copy puts text on the system clipboard.
func Copy(ctx context.Context, text string) error {
	h, err := hostSys().copyCommand()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	return runHelper(ctx, h, text)
}

// checkURL accepts only what pfwd builds: http or https, localhost or an
// IP address, a port, and no path, query or user. Openers such as
// rundll32 would launch other kinds of URL as programs or files.
func checkURL(s string) error {
	bad := fmt.Errorf("refusing to open %q: pfwd only opens http or https URLs of forwarded ports", s)
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return bad
	}
	if host := u.Hostname(); host != "localhost" && net.ParseIP(host) == nil {
		return bad
	}
	if p, err := strconv.Atoi(u.Port()); err != nil || p < 1 || p > 65535 {
		return bad
	}
	return nil
}

// sys is the machine the helpers are chosen for; tests describe others.
type sys struct {
	goos     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	readFile func(string) ([]byte, error)
}

func hostSys() sys {
	return sys{goos: runtime.GOOS, getenv: os.Getenv, lookPath: exec.LookPath, readFile: os.ReadFile}
}

// helper is a program to run and its arguments.
type helper struct {
	name string
	args []string
}

// find returns name with args if name is installed.
func (s sys) find(name string, args ...string) (helper, bool) {
	p, err := s.lookPath(name)
	if err != nil {
		return helper{}, false
	}
	return helper{name: p, args: args}, true
}

// systemRoot is the Windows directory, from the path the system set.
func (s sys) systemRoot() string {
	if r := s.getenv("SystemRoot"); r != "" {
		return r
	}
	return `C:\Windows`
}

// isWSL reports whether this is Linux under the Windows Subsystem for
// Linux, where the browser and clipboard belong to Windows.
func (s sys) isWSL() bool {
	if s.getenv("WSL_DISTRO_NAME") != "" || s.getenv("WSL_INTEROP") != "" {
		return true
	}
	b, err := s.readFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// hasDisplay reports whether an X11 or Wayland session is running.
func (s sys) hasDisplay() bool {
	return s.getenv("DISPLAY") != "" || s.getenv("WAYLAND_DISPLAY") != ""
}

func (s sys) openCommand(u string) (helper, error) {
	switch s.goos {
	case "darwin":
		return helper{name: "/usr/bin/open", args: []string{u}}, nil
	case "windows":
		return helper{name: s.systemRoot() + `\System32\rundll32.exe`, args: []string{"url.dll,FileProtocolHandler", u}}, nil
	}
	if s.isWSL() {
		if h, ok := s.find("wslview", u); ok {
			return h, nil
		}
		if h, ok := s.find("rundll32.exe", "url.dll,FileProtocolHandler", u); ok {
			return h, nil
		}
	}
	if !s.hasDisplay() {
		return helper{}, fmt.Errorf("no graphical session to open a browser in; open %s yourself", u)
	}
	if h, ok := s.find("xdg-open", u); ok {
		return h, nil
	}
	return helper{}, fmt.Errorf("xdg-open is not installed; install xdg-utils, or open %s yourself", u)
}

func (s sys) copyCommand() (helper, error) {
	switch s.goos {
	case "darwin":
		return helper{name: "/usr/bin/pbcopy"}, nil
	case "windows":
		return helper{name: s.systemRoot() + `\System32\clip.exe`}, nil
	}
	if s.getenv("WAYLAND_DISPLAY") != "" {
		if h, ok := s.find("wl-copy"); ok {
			return h, nil
		}
	}
	if s.getenv("DISPLAY") != "" {
		if h, ok := s.find("xclip", "-selection", "clipboard"); ok {
			return h, nil
		}
		if h, ok := s.find("xsel", "--clipboard", "--input"); ok {
			return h, nil
		}
	}
	if s.isWSL() {
		if h, ok := s.find("clip.exe"); ok {
			return h, nil
		}
	}
	return helper{}, errors.New("no clipboard tool found; install wl-clipboard, xclip or xsel")
}

// open runs an opener. One still running at the deadline is most likely
// waiting for the browser it started (xdg-open does that outside a desktop
// environment), so it counts as opened; killing it leaves the browser be.
func open(ctx context.Context, h helper) error {
	err := runHelper(ctx, h, "")
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil
	}
	return err
}

// runHelper runs h with stdin as its input and waits for it. Its output
// goes to the null device: clipboard tools such as xclip stay running to
// serve the clipboard, and a pipe they hold would block Wait.
func runHelper(ctx context.Context, h helper, stdin string) error {
	cmd := exec.CommandContext(ctx, h.name, h.args...) //nolint:gosec // fixed helper programs; the only outside argument is a URL checkURL passed
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(h.name), err)
	}
	return nil
}
