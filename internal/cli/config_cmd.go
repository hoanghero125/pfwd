package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/config"
)

// newConfigCmd builds `pfwd config` and its path, edit and validate
// subcommands.
func newConfigCmd() *cobra.Command {
	return group("config", "Manage the config file",
		&cobra.Command{
			Use:   "path",
			Short: "Print the config file path",
			Args:  usageArgs(cobra.NoArgs),
			RunE:  runConfigPath,
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open the config file in your editor",
			Long: `Open the config file in $VISUAL, else $EDITOR, else notepad on Windows and
vi elsewhere. A missing file is created first, with every setting at its
default and a comment on what it does.`,
			Args: usageArgs(cobra.NoArgs),
			RunE: runConfigEdit,
		},
		&cobra.Command{
			Use:   "validate",
			Short: "Check the config file for unknown keys and bad values",
			Args:  usageArgs(cobra.NoArgs),
			RunE:  runConfigValidate,
		},
	)
}

// configPath is --config if given, else config.Path.
func configPath(cmd *cobra.Command) (string, error) {
	if p, err := cmd.Flags().GetString("config"); err == nil && p != "" {
		return p, nil
	}
	return config.Path()
}

// wantJSON reports whether --json was given.
func wantJSON(cmd *cobra.Command) bool {
	v, err := cmd.Flags().GetBool("json")
	return err == nil && v
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func runConfigPath(cmd *cobra.Command, _ []string) error {
	path, err := configPath(cmd)
	if err != nil {
		return err
	}
	if wantJSON(cmd) {
		return writeJSON(cmd.OutOrStdout(), map[string]string{"path": path})
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), path)
	return err
}

// validation is the JSON form of `pfwd config validate`.
type validation struct {
	Path     string        `json:"path"`
	Exists   bool          `json:"exists"`
	OK       bool          `json:"ok"`
	Problems []problemJSON `json:"problems"`
	Notes    []string      `json:"notes"`
}

type problemJSON struct {
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Key     string `json:"key"`
	Message string `json:"message"`
}

func runConfigValidate(cmd *cobra.Command, _ []string) error {
	path, err := configPath(cmd)
	if err != nil {
		return err
	}
	v := validation{Path: path, Problems: []problemJSON{}, Notes: []string{}}
	_, statErr := os.Stat(path)
	v.Exists = !errors.Is(statErr, fs.ErrNotExist)

	cfg, err := config.Load(path)
	var probs *config.Problems
	switch {
	case errors.As(err, &probs):
		for _, p := range probs.List {
			v.Problems = append(v.Problems, problemJSON{Line: p.Line, Col: p.Col, Key: p.Key, Message: p.Msg})
		}
	case err != nil:
		return err
	default:
		v.OK = true
		v.Notes = append(v.Notes, cfg.Unapplied()...)
	}

	out := cmd.OutOrStdout()
	if wantJSON(cmd) {
		if err := writeJSON(out, v); err != nil {
			return err
		}
	} else {
		var b strings.Builder
		switch {
		case probs != nil:
			for _, line := range probs.Lines() {
				b.WriteString(line + "\n")
			}
		case !v.Exists:
			fmt.Fprintf(&b, "no config file at %s; pfwd uses the defaults. Create one with: pfwd config edit\n", path)
		default:
			fmt.Fprintf(&b, "config ok: %s\n", path)
			for _, n := range v.Notes {
				fmt.Fprintf(&b, "note: %s\n", n)
			}
		}
		if _, err := io.WriteString(out, b.String()); err != nil {
			return err
		}
	}
	if probs != nil {
		return fmt.Errorf("%s has %d %s; fix %s and run pfwd config validate again",
			path, len(probs.List), plural(len(probs.List), "problem", "problems"), plural(len(probs.List), "it", "them"))
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func runConfigEdit(cmd *cobra.Command, _ []string) error {
	path, err := configPath(cmd)
	if err != nil {
		return err
	}
	created, err := createConfig(path)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(cmd.OutOrStdout(), "created %s with the default settings\n", path)
	}
	argv, from := editorCommand(os.Getenv, runtime.GOOS)
	c := exec.CommandContext(cmd.Context(), argv[0], append(argv[1:], path)...) //nolint:gosec // the editor is the user's own VISUAL or EDITOR
	c.Stdin, c.Stdout, c.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		hint := "set VISUAL or EDITOR to your editor"
		if from != "" {
			hint = "check " + from + ", or unset it to use the default editor"
		}
		return fmt.Errorf("run the editor %s: %w; %s", argv[0], err, hint)
	}
	return nil
}

// createConfig writes the template to path unless a file is there already.
func createConfig(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create the config directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create the config file: %w", err)
	}
	if _, err := io.WriteString(f, config.Template()); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("write the config file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("write the config file %s: %w", path, err)
	}
	return true, nil
}

// editorCommand returns the editor to run and the variable it came from
// ("" for the platform default). Like git, it allows arguments, as in
// VISUAL="code --wait"; quote a path with spaces.
func editorCommand(getenv func(string) string, goos string) ([]string, string) {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if argv := splitCommand(getenv(name)); len(argv) > 0 {
			return argv, name
		}
	}
	if goos == "windows" {
		return []string{"notepad"}, ""
	}
	return []string{"vi"}, ""
}

// splitCommand splits s at spaces outside single or double quotes.
// Backslashes are literal, so Windows paths work unquoted.
func splitCommand(s string) []string {
	var (
		argv  []string
		cur   strings.Builder
		quote rune
		inArg bool
	)
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote, inArg = r, true
		case r == ' ' || r == '\t':
			if inArg {
				argv = append(argv, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if inArg {
		argv = append(argv, cur.String())
	}
	return argv
}
