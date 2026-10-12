package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/hoanghero125/pfwd/internal/config"
)

// TestMain keeps the tests away from the user's real config file and lets
// the test binary stand in for an editor (see fakeEditorEnv).
func TestMain(m *testing.M) {
	if os.Getenv(fakeEditorEnv) == "1" {
		os.Exit(fakeEditor(os.Args[1:]))
	}
	dir, err := os.MkdirTemp("", "pfwd-cli-test")
	if err != nil {
		panic(err)
	}
	// An empty file: the defaults, whatever the user's own config says.
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		panic(err)
	}
	if err := os.Setenv("PFWD_CONFIG", cfg); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeEditorEnv makes the test binary act as an editor: it appends
// editedMark to the file named by its last argument.
const (
	fakeEditorEnv = "PFWD_TEST_FAKE_EDITOR"
	editedMark    = "# edited by the fake editor\n"
)

func fakeEditor(args []string) int {
	if len(args) == 0 {
		return 3
	}
	f, err := os.OpenFile(args[len(args)-1], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 4
	}
	defer f.Close()
	if _, err := f.WriteString(editedMark); err != nil {
		return 5
	}
	return 0
}

// useFakeEditor points VISUAL at the test binary.
func useFakeEditor(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeEditorEnv, "1")
	t.Setenv("VISUAL", `"`+exe+`"`)
	t.Setenv("EDITOR", "")
}

// writeConfig writes a config file and points PFWD_CONFIG at it.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFWD_CONFIG", path)
	return path
}

func TestConfigPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "from-env.toml")
	t.Setenv("PFWD_CONFIG", path)
	if code, out, errOut := run(t, "config", "path"); code != 0 || out != path+"\n" || errOut != "" {
		t.Errorf("pfwd config path: code %d, stdout %q, stderr %q; want 0, %q", code, out, errOut, path)
	}
	flag := filepath.Join(t.TempDir(), "from-flag.toml")
	for _, args := range [][]string{{"--config", flag, "config", "path"}, {"config", "path", "--config", flag}} {
		if code, out, _ := run(t, args...); code != 0 || out != flag+"\n" {
			t.Errorf("pfwd %v: code %d, stdout %q; want %q (the flag wins over PFWD_CONFIG)", args, code, out, flag)
		}
	}
	code, out, _ := run(t, "config", "path", "--json")
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil || got["path"] != path {
		t.Errorf("pfwd config path --json: code %d, stdout %q (%v); want {\"path\": %q}", code, out, err, path)
	}
}

func TestConfigValidateNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	t.Setenv("PFWD_CONFIG", path)
	code, out, errOut := run(t, "config", "validate")
	if code != 0 || errOut != "" || !strings.Contains(out, "no config file at "+path) || !strings.Contains(out, "pfwd config edit") {
		t.Errorf("pfwd config validate without a file: code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestConfigValidateOK(t *testing.T) {
	path := writeConfig(t, "[forward]\nrestore = false\n\n[hosts.devbox]\nforwards = [\"5432\", \"-D 1080\"]\n")
	code, out, errOut := run(t, "config", "validate")
	want := "config ok: " + path + "\n" +
		"note: hosts.devbox.forwards entry \"-D 1080\" is accepted but applies from v0.4.0\n"
	if code != 0 || out != want || errOut != "" {
		t.Errorf("pfwd config validate: code %d, stdout %q, stderr %q; want 0 and\n%s", code, out, errOut, want)
	}
}

func TestConfigValidateProblems(t *testing.T) {
	path := writeConfig(t, "[detect]\nintervl = \"2s\"\n\n[forward]\non_conflict = \"maybe\"\n")
	code, out, errOut := run(t, "config", "validate")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != 1 || len(lines) != 2 {
		t.Fatalf("pfwd config validate: code %d, stdout %q, stderr %q; want 1 and two problem lines", code, out, errOut)
	}
	if !strings.HasPrefix(lines[0], path+":2:1: detect.intervl: unknown setting") {
		t.Errorf("first problem = %q; want %s:2:1: detect.intervl: unknown setting", lines[0], path)
	}
	if !strings.HasPrefix(lines[1], path+":5:") || !strings.Contains(lines[1], "forward.on_conflict: ") {
		t.Errorf("second problem = %q; want %s:5:...: forward.on_conflict: ...", lines[1], path)
	}
	if !strings.HasPrefix(errOut, "Error: ") || !strings.Contains(errOut, "2 problems") || strings.Contains(errOut, "--help") {
		t.Errorf("stderr = %q; want one error line counting 2 problems, no usage hint", errOut)
	}
}

func TestConfigValidateJSON(t *testing.T) {
	path := writeConfig(t, "[detect]\nintervl = \"2s\"\n")
	code, out, _ := run(t, "config", "validate", "--json")
	var got struct {
		Path     string           `json:"path"`
		Exists   bool             `json:"exists"`
		OK       bool             `json:"ok"`
		Problems []map[string]any `json:"problems"`
		Notes    []string         `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if code != 1 || got.Path != path || !got.Exists || got.OK || len(got.Problems) != 1 {
		t.Fatalf("pfwd config validate --json: code %d, %+v", code, got)
	}
	p := got.Problems[0]
	if p["line"] != 2.0 || p["col"] != 1.0 || p["key"] != "detect.intervl" || p["message"] != "unknown setting" {
		t.Errorf("problem = %v", p)
	}

	writeConfig(t, "")
	code, out, _ = run(t, "config", "validate", "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || !got.OK || got.Problems == nil || got.Notes == nil {
		t.Errorf("valid config: code %d, stdout %s; want ok true and empty (not null) lists", code, out)
	}
}

func TestConfigEditCreatesTemplate(t *testing.T) {
	useFakeEditor(t)
	path := filepath.Join(t.TempDir(), "new", "pfwd", "config.toml")
	t.Setenv("PFWD_CONFIG", path)
	code, out, errOut := run(t, "config", "edit")
	if code != 0 {
		t.Fatalf("pfwd config edit: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != config.Template()+editedMark {
		t.Errorf("file = %q; want the template, then the editor's change", data)
	}
	if !strings.Contains(out, "created "+path) {
		t.Errorf("stdout = %q; want it to say the file was created", out)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}
}

func TestConfigEditKeepsExistingFile(t *testing.T) {
	useFakeEditor(t)
	path := writeConfig(t, "[ui]\nmouse = false\n")
	code, out, errOut := run(t, "config", "edit")
	data, _ := os.ReadFile(path)
	if code != 0 || string(data) != "[ui]\nmouse = false\n"+editedMark || strings.Contains(out, "created") {
		t.Errorf("pfwd config edit: code %d, stdout %q, stderr %q, file %q; want the file edited, not replaced", code, out, errOut, data)
	}
}

func TestConfigEditReportsEditorFailure(t *testing.T) {
	writeConfig(t, "")
	t.Setenv("VISUAL", "pfwd-no-such-editor")
	code, _, errOut := run(t, "config", "edit")
	if code != 1 || !strings.Contains(errOut, "pfwd-no-such-editor") || !strings.Contains(errOut, "VISUAL") || !strings.Contains(errOut, "quotes") {
		t.Errorf("pfwd config edit with a missing editor: code %d, stderr %q; want 1, naming the editor and VISUAL, and suggesting quotes", code, errOut)
	}
}

func TestEditorCommand(t *testing.T) {
	for _, tc := range []struct {
		visual, editor, goos string
		want                 []string
		from                 string
	}{
		{"nvim", "nano", "linux", []string{"nvim"}, "VISUAL"},
		{"", "code --wait", "darwin", []string{"code", "--wait"}, "EDITOR"},
		{"  ", "", "linux", []string{"vi"}, ""},
		{"", "", "windows", []string{"notepad"}, ""},
		{`"C:\Program Files\Notepad++\notepad++.exe" -multiInst`, "", "windows", []string{`C:\Program Files\Notepad++\notepad++.exe`, "-multiInst"}, "VISUAL"},
		{`'/opt/my editor/ed' -w`, "", "linux", []string{"/opt/my editor/ed", "-w"}, "VISUAL"},
		// An unquoted path with spaces works when it names a file.
		{`C:\Program Files\Sublime Text\subl.exe`, "", "windows", []string{`C:\Program Files\Sublime Text\subl.exe`}, "VISUAL"},
		{`C:\Program Files\Other\ed.exe --wait`, "", "windows", []string{`C:\Program`, `Files\Other\ed.exe`, "--wait"}, "VISUAL"},
	} {
		env := map[string]string{"VISUAL": tc.visual, "EDITOR": tc.editor}
		isFile := func(p string) bool { return p == `C:\Program Files\Sublime Text\subl.exe` }
		argv, from := editorCommand(func(k string) string { return env[k] }, tc.goos, isFile)
		if !reflect.DeepEqual(argv, tc.want) || from != tc.from {
			t.Errorf("editorCommand(VISUAL=%q, EDITOR=%q, %s) = %q from %q; want %q from %q", tc.visual, tc.editor, tc.goos, argv, from, tc.want, tc.from)
		}
	}
}

func TestRootStopsOnConfigProblems(t *testing.T) {
	path := writeConfig(t, "[detect]\nintervl = \"2s\"\ninterval = \"1ms\"\n")
	code, out, errOut := run(t, "devbox")
	if code != 1 || out != "" {
		t.Fatalf("pfwd devbox with a bad config: code %d, stdout %q; want 1", code, out)
	}
	for _, want := range []string{path + ":2:1: detect.intervl: unknown setting", path + ":3:", "pfwd config validate"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "interactive terminal") {
		t.Errorf("stderr = %q; the config must be checked before the terminal", errOut)
	}
}

func TestRootConfigFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(path, []byte("bogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, "devbox", "--config", path); code != 1 || !strings.Contains(errOut, path+":1:1: bogus: unknown setting") {
		t.Errorf("pfwd devbox --config bad.toml: code %d, stderr %q", code, errOut)
	}
}

func TestBadLogLevelNamesItsSource(t *testing.T) {
	t.Setenv("PFWD_LOG", "loud")
	code, _, errOut := run(t, "devbox")
	if code != 1 || !strings.Contains(errOut, "PFWD_LOG") || !strings.Contains(errOut, `"loud"`) || strings.Contains(errOut, "--help") {
		t.Errorf("PFWD_LOG=loud: code %d, stderr %q; want 1, naming PFWD_LOG and the value", code, errOut)
	}
	t.Setenv("PFWD_LOG", "")
	code, _, errOut = run(t, "devbox", "--log-level", "loud")
	if code != 2 || !strings.Contains(errOut, "--log-level") {
		t.Errorf("--log-level loud: code %d, stderr %q; want 2, naming --log-level", code, errOut)
	}
}

// TestRootRefusesMissingNamedConfig: a config file named with --config or
// PFWD_CONFIG must exist; a typo must not mean "run with the defaults".
func TestRootRefusesMissingNamedConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "typo.toml")
	code, _, errOut := run(t, "devbox", "--config", missing)
	if code != 1 || !strings.Contains(errOut, missing) || !strings.Contains(errOut, "does not exist") {
		t.Errorf("--config with a missing file: code %d, stderr %q", code, errOut)
	}
	t.Setenv("PFWD_CONFIG", missing)
	code, _, errOut = run(t, "devbox")
	if code != 1 || !strings.Contains(errOut, "PFWD_CONFIG") {
		t.Errorf("PFWD_CONFIG with a missing file: code %d, stderr %q", code, errOut)
	}
}

func TestConfigValidateJSONReadError(t *testing.T) {
	dir := t.TempDir() // a directory can't be read as a file
	t.Setenv("PFWD_CONFIG", dir)
	code, out, _ := run(t, "config", "validate", "--json")
	var got struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%q", err, out)
	}
	if code != 1 || got.OK || got.Error == "" {
		t.Errorf("code %d, %+v; want 1 with the read error in the JSON", code, got)
	}
}

func TestConfigFlagHelp(t *testing.T) {
	_, out, _ := run(t, "--help")
	if !strings.Contains(out, "--config path") || strings.Contains(out, "--config pfwd") {
		t.Errorf("--config help should name its value path:\n%s", out)
	}
}
