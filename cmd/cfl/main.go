// Command cfl is a command-line client for Confluence Data Center.
package main

import (
	"os"

	"github.com/truthatt11/confluence-cli/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
