package desktop

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// helperEnv makes the test binary act as a helper program: "copy" writes
// its stdin to the file named by helperOutEnv, "hang" never exits.
const (
	helperEnv    = "PFWD_TEST_DESKTOP_HELPER"
	helperOutEnv = "PFWD_TEST_DESKTOP_OUT"
)

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "copy":
		data, _ := io.ReadAll(os.Stdin)
		if err := os.WriteFile(os.Getenv(helperOutEnv), data, 0o600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "fail":
		os.Exit(2)
	case "hang":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	goleak.VerifyTestMain(m)
}

// fakeSys describes a machine: its OS, environment and installed programs.
func fakeSys(goos string, env map[string]string, programs ...string) sys {
	return sys{
		goos:   goos,
		getenv: func(k string) string { return env[k] },
		lookPath: func(name string) (string, error) {
			if slices.Contains(programs, name) {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		readFile: func(name string) ([]byte, error) {
			if name == "/proc/sys/kernel/osrelease" && env["osrelease"] != "" {
				return []byte(env["osrelease"]), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

const url3000 = "http://localhost:3000"

func TestOpenCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    sys
		want []string // program and arguments, or nil for an error
		err  string
	}{
		{"macOS", fakeSys("darwin", nil), []string{"/usr/bin/open", url3000}, ""},
		{"Windows", fakeSys("windows", map[string]string{"SystemRoot": `D:\Win`}),
			[]string{`D:\Win\System32\rundll32.exe`, "url.dll,FileProtocolHandler", url3000}, ""},
		{"Windows without SystemRoot", fakeSys("windows", nil),
			[]string{`C:\Windows\System32\rundll32.exe`, "url.dll,FileProtocolHandler", url3000}, ""},
		{"WSL with wslview", fakeSys("linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, "wslview", "rundll32.exe"),
			[]string{"/usr/bin/wslview", url3000}, ""},
		{"WSL by kernel release", fakeSys("linux", map[string]string{"osrelease": "5.15.167.4-microsoft-standard-WSL2"}, "rundll32.exe", "xdg-open"),
			[]string{"/usr/bin/rundll32.exe", "url.dll,FileProtocolHandler", url3000}, ""},
		{"Linux desktop", fakeSys("linux", map[string]string{"DISPLAY": ":0"}, "xdg-open"),
			[]string{"/usr/bin/xdg-open", url3000}, ""},
		{"FreeBSD on Wayland", fakeSys("freebsd", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "xdg-open"),
			[]string{"/usr/bin/xdg-open", url3000}, ""},
		{"headless", fakeSys("linux", nil, "xdg-open"), nil, "no graphical session"},
		{"no xdg-open", fakeSys("linux", map[string]string{"DISPLAY": ":0"}), nil, "xdg-utils"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := tc.s.openCommand(url3000)
			if tc.want == nil {
				if err == nil || !strings.Contains(err.Error(), tc.err) || !strings.Contains(err.Error(), url3000) {
					t.Errorf("openCommand = %v, %v; want an error with %q and the URL", h, err, tc.err)
				}
				return
			}
			if got := append([]string{h.name}, h.args...); err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("openCommand = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestCopyCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    sys
		want []string
	}{
		{"macOS", fakeSys("darwin", nil), []string{"/usr/bin/pbcopy"}},
		{"Windows", fakeSys("windows", map[string]string{"SystemRoot": `D:\Win`}), []string{`D:\Win\System32\clip.exe`}},
		{"Wayland", fakeSys("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, "wl-copy", "xclip"),
			[]string{"/usr/bin/wl-copy"}},
		{"XWayland without wl-copy", fakeSys("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, "xclip"),
			[]string{"/usr/bin/xclip", "-selection", "clipboard"}},
		{"X11 with xsel", fakeSys("freebsd", map[string]string{"DISPLAY": ":0"}, "xsel"),
			[]string{"/usr/bin/xsel", "--clipboard", "--input"}},
		{"WSL without a display", fakeSys("linux", map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}, "clip.exe", "xclip"),
			[]string{"/usr/bin/clip.exe"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := tc.s.copyCommand()
			if got := append([]string{h.name}, h.args...); err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("copyCommand = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := fakeSys("linux", nil, "xclip").copyCommand(); err == nil || !strings.Contains(err.Error(), "wl-clipboard, xclip or xsel") {
		t.Errorf("copyCommand on a headless Linux = %v; want a hint to install a clipboard tool", err)
	}
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{url3000, "https://localhost:8443", "http://127.0.0.1:80", "http://[::1]:8080", "http://localhost:3000/"} {
		if err := checkURL(ok); err != nil {
			t.Errorf("checkURL(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "http://example.com:80", "http://localhost",
		"http://localhost:3000/x", "http://localhost:3000?q", "http://user@localhost:3000", "ftp://localhost:21",
		"http://localhost:3000#f", "\\\\host\\share", "http://localhost:3000 --flag",
	} {
		if err := checkURL(bad); err == nil {
			t.Errorf("checkURL(%q) accepted it", bad)
		}
	}
}

func TestOpenURLRejectsOtherURLs(t *testing.T) {
	if err := OpenURL(t.Context(), "file:///etc/passwd"); err == nil {
		t.Error("OpenURL opened a file URL")
	}
}

// selfHelper runs the test binary as a helper in mode.
func selfHelper(t *testing.T, mode string) helper {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(helperEnv, mode)
	return helper{name: exe}
}

func TestRunHelperPassesStdin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip")
	t.Setenv(helperOutEnv, out)
	if err := runHelper(t.Context(), selfHelper(t, "copy"), url3000); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != url3000 {
		t.Errorf("the helper read %q, %v; want %q", data, err, url3000)
	}
}

func TestRunHelperReportsFailure(t *testing.T) {
	err := runHelper(t.Context(), selfHelper(t, "fail"), "")
	if err == nil || !strings.Contains(err.Error(), "exit status 2") {
		t.Errorf("runHelper = %v; want exit status 2", err)
	}
}

// TestOpenStillRunningCountsAsOpened: an opener that is still running at
// the deadline is waiting for the browser it started.
func TestOpenStillRunningCountsAsOpened(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := open(ctx, selfHelper(t, "hang")); err != nil {
		t.Errorf("open = %v; want nil once the deadline passes", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("open took %v", d)
	}
}
