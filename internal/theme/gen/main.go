// Command gen writes the web UI's copy of the theme: theme.gen.css and
// theme.gen.ts in the directory given as its argument.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hoanghero125/pfwd/internal/theme"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen DIR")
		os.Exit(2)
	}
	dir := os.Args[1]
	for name, text := range map[string]string{"theme.gen.css": theme.CSS(), "theme.gen.ts": theme.TS()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil { //nolint:gosec // generated source files are meant to be readable
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
