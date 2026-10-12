package cli

import (
	"archive/zip"
	"cmp"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/hoanghero125/pfwd/internal/config"
	"github.com/hoanghero125/pfwd/internal/state"
	"github.com/hoanghero125/pfwd/internal/version"
)

// logTail is how much of each log file goes into a bundle.
const logTail = 2 << 20

var (
	// privateKeyBlock matches a PEM private key.
	privateKeyBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	// secretValue matches key=value or "key": "value" pairs that name a
	// secret, keeping the key.
	secretValue = regexp.MustCompile(`(?i)((?:password|passphrase|passwd|secret|token)["']?\s*[:=]\s*["']?)[^\s"',}]+`)
)

// redact takes secrets and the home directory out of text.
func redact(text, home string) string {
	text = privateKeyBlock.ReplaceAllString(text, "[redacted private key]")
	text = secretValue.ReplaceAllString(text, "${1}[redacted]")
	if home != "" {
		for _, h := range []string{strings.ReplaceAll(home, `\`, `\\`), home, filepath.ToSlash(home)} {
			text = strings.ReplaceAll(text, h, "~")
		}
	}
	return text
}

// writeBundle writes the support zip to path ("" picks a name in the
// current directory) and returns where it went. doctorText is the doctor
// report, if one ran.
func writeBundle(path string, c common, doctorText string) (string, error) {
	if path == "" {
		path = "pfwd-doctor-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	}
	home, _ := os.UserHomeDir()
	files := map[string]string{
		"version.txt": fmt.Sprintf("%s\n%s/%s, %s\n", version.Get().String(), runtime.GOOS, runtime.GOARCH, runtime.Version()),
		"config.toml": configForBundle(c.cfgPath),
		"state.txt":   stateForBundle(),
	}
	if doctorText != "" {
		files["doctor.txt"] = doctorText
	}
	if dir, err := config.StateDir(); err == nil {
		for _, name := range []string{"pfwd.log", "pfwd.log.1"} {
			if tail, ok := readTail(filepath.Join(dir, name), logTail); ok {
				files[name] = tail
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create the bundle: %w; name another file with --output", err)
	}
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err == nil {
			_, err = io.WriteString(w, redact(files[name], home))
		}
		if err != nil {
			_ = zw.Close()
			_ = f.Close()
			return "", fmt.Errorf("write the bundle: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write the bundle: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write the bundle: %w", err)
	}
	return path, nil
}

// configForBundle is the config file as the user wrote it.
func configForBundle(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("# no config file at %s; pfwd uses its defaults\n", path)
	}
	return string(data)
}

// stateForBundle summarizes state.json: hosts with their forward counts,
// not the forwards themselves.
func stateForBundle() string {
	path, err := config.StatePath()
	if err != nil {
		return "no state directory: " + err.Error() + "\n"
	}
	hosts, err := state.Open(path, state.Options{}).Hosts()
	if err != nil {
		return "state.json can't be read: " + err.Error() + "\n"
	}
	names := make([]string, 0, len(hosts))
	for n := range hosts {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int { return cmp.Compare(a, b) })
	var b strings.Builder
	fmt.Fprintf(&b, "%d hosts\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "%s: %d forwards, last used %s\n", n, hosts[n].Forwards, hosts[n].LastUsed.Format(time.RFC3339))
	}
	return b.String()
}

// readTail returns up to limit bytes from the end of the file at path.
func readTail(path string, limit int64) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false
	}
	if info.Size() > limit {
		if _, err := f.Seek(-limit, io.SeekEnd); err != nil {
			return "", false
		}
	}
	data, err := io.ReadAll(f)
	return string(data), err == nil
}
