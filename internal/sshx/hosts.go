package sshx

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxIncludeDepth bounds nested Include directives, as OpenSSH does.
const maxIncludeDepth = 16

// ConfigHosts returns the host aliases an ssh config names: the Host
// patterns without wildcards or negation, in file order, following
// Include. path is the file given with -F; "" reads ~/.ssh/config. A
// missing file has no hosts.
func ConfigHosts(path string, env Env) ([]string, error) {
	if path == "" {
		path = filepath.Join(env.Home, ".ssh", "config")
	}
	var hosts []string
	seen := make(map[string]bool)
	visited := make(map[string]bool)
	var read func(path string, depth int) error
	read = func(path string, depth int) error {
		if depth > maxIncludeDepth || visited[path] {
			return nil
		}
		visited[path] = true
		f, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read ssh config %s: %w", path, err)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			key, args := configLine(sc.Text())
			switch key {
			case "host":
				for _, p := range args {
					if p != "" && !strings.ContainsAny(p, "*?!") && !seen[p] {
						seen[p] = true
						hosts = append(hosts, p)
					}
				}
			case "include":
				for _, inc := range args {
					matches, err := filepath.Glob(includePath(inc, env.Home))
					if err != nil {
						continue
					}
					for _, m := range matches {
						if info, err := os.Stat(m); err == nil && info.IsDir() {
							continue // OpenSSH reads a directory as empty
						}
						if err := read(m, depth+1); err != nil {
							return err
						}
					}
				}
			}
		}
		return sc.Err()
	}
	if err := read(path, 0); err != nil {
		return nil, err
	}
	return hosts, nil
}

// configLine splits an ssh config line into its lowercase keyword and
// arguments, as "Keyword arg ..." or "Keyword=arg", dropping comments.
// Double quotes group an argument.
func configLine(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil
	}
	end := strings.IndexAny(line, " \t=")
	if end < 0 {
		return strings.ToLower(line), nil
	}
	key := strings.ToLower(line[:end])
	rest := strings.TrimLeft(line[end:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
	var args []string
	var cur strings.Builder
	quoted, started := false, false
	for _, r := range rest {
		switch {
		case r == '"':
			quoted, started = !quoted, true
		case !quoted && r == '#' && cur.Len() == 0 && !started:
			return key, args
		case !quoted && (r == ' ' || r == '\t'):
			if cur.Len() > 0 || started {
				args = append(args, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || started {
		args = append(args, cur.String())
	}
	return key, args
}

// includePath resolves an Include argument the way OpenSSH does for a user
// config: absolute as is, ~/ under home, anything else under ~/.ssh.
func includePath(p, home string) string {
	switch {
	case filepath.IsAbs(p):
		return p
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return filepath.Join(home, ".ssh", p)
}
