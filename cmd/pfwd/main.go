// Command pfwd detects listening ports on SSH hosts and forwards them to
// localhost, from a terminal UI, plain CLI commands or a local web UI.
package main

import (
	"os"

	"github.com/hoanghero125/pfwd/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
