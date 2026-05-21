// Package cli implements the `lattice` command tree. The CLI is the canonical
// Lattice interface: every operation is a subcommand, and every command
// supports --json for machine-readable output.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// IO carries the global flags and output streams shared by every command.
type IO struct {
	Repo string // target repository path
	JSON bool   // emit machine-readable JSON
	Out  io.Writer
	Err  io.Writer
}

// printJSON writes v as indented JSON to Out.
func (io *IO) printJSON(v interface{}) error {
	enc := json.NewEncoder(io.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// printf writes a human-readable line to Out.
func (io *IO) printf(format string, args ...interface{}) {
	fmt.Fprintf(io.Out, format, args...)
}

// errorf writes a human-readable line to Err.
func (io *IO) errorf(format string, args ...interface{}) {
	fmt.Fprintf(io.Err, format, args...)
}

// jsonError is the envelope for a machine-readable error.
type jsonError struct {
	Error      string      `json:"error"`
	Code       string      `json:"code,omitempty"`
	NextAction interface{} `json:"next_action,omitempty"`
}

// fail emits an error (respecting --json) and returns a non-nil error so the
// caller can propagate a non-zero exit code.
func (io *IO) fail(code, msg string, nextAction interface{}) error {
	if io.JSON {
		_ = io.printJSON(jsonError{Error: msg, Code: code, NextAction: nextAction})
	} else {
		io.errorf("error: %s\n", msg)
	}
	return errExit
}

// errExit is a sentinel meaning "already reported; just set exit code".
var errExit = fmt.Errorf("lattice: command failed")

// IsExit reports whether err is the silent exit sentinel.
func IsExit(err error) bool { return err == errExit }

// defaultIO returns an IO writing to the process streams.
func defaultIO() *IO {
	return &IO{Repo: ".", Out: os.Stdout, Err: os.Stderr}
}
