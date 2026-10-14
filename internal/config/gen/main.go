// Command gen writes the configuration reference, docs/config.md, to the
// path given as its argument.
package main

import (
	"fmt"
	"os"

	"github.com/hoanghero125/pfwd/internal/config"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen FILE")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], []byte(config.ReferenceMarkdown()), 0o644); err != nil { //nolint:gosec // documentation is meant to be readable
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
