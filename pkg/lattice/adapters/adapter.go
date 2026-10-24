// Package adapters defines the language-adapter contract and the registry
// that dispatches files to the right adapter.
//
// The Lattice core never imports a language parser. Every language-specific
// concern lives behind LanguageAdapter; adding a language is implementing one
// adapter and registering it.
package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// ErrIndexerNotConfigured is returned by SCIPIndexerCommand when no SCIP
// indexer is available for the language.
var ErrIndexerNotConfigured = errors.New("scip indexer not configured")

// ErrMutationRunnerNotConfigured is returned when no mutation runner exists.
var ErrMutationRunnerNotConfigured = errors.New("mutation runner not configured")

// ParseError is a typed syntax error with a location. Adapters return it for
// source that cannot be parsed; they must not error on valid source.
type ParseError struct {
	File    string
	Line    int
	Column  int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Message)
}

// AnnotationSuggestion is a proposed annotation for a symbol, produced by the
// annotation-suggestion agentic capability and rendered to source by an
// adapter.
type AnnotationSuggestion struct {
	Annotation string        `json:"annotation"`
	Args       []interface{} `json:"args,omitempty"`
	Rationale  string        `json:"rationale,omitempty"`
	Confidence float64       `json:"confidence,omitempty"`
}

// Resolver is implemented by an adapter whose files define symbols across
// each other, e.g. database migrations where a later file redefines or
// drops what an earlier one created. Extraction calls Resolve once with all
// the adapter's modules (same order and length in, same out) after every
// file is parsed and before diagnostics are reported.
type Resolver interface {
	Resolve(modules []ir.Module) []ir.Module
}

// LanguageAdapter parses one language's source into Lattice IR and supplies
// the per-language command lines for SCIP indexing and mutation testing.
type LanguageAdapter interface {
	// Name is the canonical language name ("python", "typescript", "php",
	// "go").
	Name() string

	// FileExtensions lists the extensions this adapter owns, including the dot.
	FileExtensions() []string

	// CanParse reports whether this adapter handles the given path.
	CanParse(path string) bool

	// Parse turns one file's source into an IR module. It must not error on
	// syntactically valid source; for blocking syntax errors it returns a
	// *ParseError.
	Parse(ctx context.Context, path string, source []byte) (ir.Module, error)

	// RenderAnnotationSuggestion renders suggested annotations as a code
	// snippet idiomatic to this language.
	RenderAnnotationSuggestion(symbol ir.Symbol, suggested []AnnotationSuggestion) (string, error)

	// SCIPIndexerCommand returns the command that indexes repoPath and writes
	// a SCIP index to outputPath, or ErrIndexerNotConfigured.
	SCIPIndexerCommand(repoPath, outputPath string) ([]string, error)

	// MutationRunnerCommand returns the command that mutation-tests the given
	// files, or ErrMutationRunnerNotConfigured.
	MutationRunnerCommand(repoPath string, targetFiles []string) ([]string, error)
}
