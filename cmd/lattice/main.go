// Command lattice is the canonical Lattice interface: a single static binary
// exposing every operation as a subcommand.
package main

// @module-feature cli

import (
	"os"

	"github.com/salahmyn/lattice/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
