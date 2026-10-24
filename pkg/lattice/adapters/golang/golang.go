// Package golang is the Lattice language adapter for Go source. Annotations
// are tags in the doc comment directly above a declaration (// @feature x);
// module tags (// @module-feature x) may sit in any comment of the file,
// usually the file or package comment.
//
// Symbols are named by package directory, not by file, so FQNs read like
// Go's own identifiers and join `go test` results: a function Lookup in
// pkg/lattice/results is pkg.lattice.results.Lookup, a method Lookup on Set
// is pkg.lattice.results.Set.Lookup, a test TestLookup is
// pkg.lattice.results.TestLookup and its t.Run("case", ...) subtest is
// pkg.lattice.results.TestLookup.case. A JUnit report of `go test` (gotestsum,
// go-junit-report) names that case classname
// "github.com/<module>/pkg/lattice/results", name "TestLookup/case", which
// normalizes to a key ending in the FQN.
package golang

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Name is the canonical language name, the key in adapters.yaml.
const Name = "go"

// Adapter implements adapters.LanguageAdapter for Go.
type Adapter struct{}

// New returns a Go adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the canonical language name.
func (*Adapter) Name() string { return Name }

// FileExtensions returns the extensions this adapter owns.
func (*Adapter) FileExtensions() []string { return []string{".go"} }

// CanParse reports whether path is a Go file.
func (*Adapter) CanParse(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".go")
}

// Parse turns Go source into an IR module via tree-sitter.
func (a *Adapter) Parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	return a.parse(ctx, path, source)
}

// commentTags maps annotation kinds to their comment tag names. The names
// are the TypeScript adapter's JSDoc tags.
var commentTags = map[string]string{
	"feature":                   "feature",
	"capability":                "capability",
	"feature_capability":        "capability",
	"enforces_invariant":        "enforces",
	"verifies":                  "verifies",
	"verifies_capability":       "verifies-capability",
	"depends_on_feature":        "depends-on-feature",
	"role":                      "role",
	"suppresses_invariant":      "suppresses",
	"surface":                   "surface",
	"error":                     "error",
	"module_feature":            "module-feature",
	"module_enforces_invariant": "module-enforces",
	"module_depends_on_feature": "module-depends-on-feature",
}

// RenderAnnotationSuggestion renders suggestions as doc-comment lines.
func (*Adapter) RenderAnnotationSuggestion(_ ir.Symbol, suggested []adapters.AnnotationSuggestion) (string, error) {
	var b strings.Builder
	for _, s := range suggested {
		tag, ok := commentTags[s.Annotation]
		if !ok {
			tag = s.Annotation
		}
		b.WriteString("// @")
		b.WriteString(tag)
		if args := renderArgs(s.Args); args != "" {
			b.WriteString(" ")
			b.WriteString(args)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func renderArgs(args []interface{}) string {
	var parts []string
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			parts = append(parts, v)
		case []string:
			parts = append(parts, v...)
		case []interface{}:
			for _, s := range v {
				if str, ok := s.(string); ok {
					parts = append(parts, str)
				}
			}
		}
	}
	return strings.Join(parts, ", ")
}

// Resolve keeps FQNs unique across a directory's files. Go allows a name
// once per package per build, but files built for different platforms
// (open_linux.go, open_windows.go), a `//go:build ignore` program, or an
// external test package beside its package may each declare the same name.
// The first declaration in path order keeps the FQN; a later one is named
// <fqn>@<file stem>, and the symbols nested under it (methods of a renamed
// type, subtests of a renamed test) move with it.
func (*Adapter) Resolve(modules []ir.Module) []ir.Module {
	seen := map[string]bool{}
	for i := range modules {
		syms := modules[i].Symbols
		suffix := "@" + strings.TrimSuffix(path.Base(modules[i].File), path.Ext(modules[i].File))
		renamed := map[string]string{} // old FQN -> new, within this file
		rename := func(fqn string) string {
			for old, nu := range renamed {
				if fqn == old {
					return nu
				}
				if strings.HasPrefix(fqn, old+".") {
					return nu + strings.TrimPrefix(fqn, old)
				}
			}
			return fqn
		}
		for j := range syms {
			s := &syms[j]
			s.FQN, s.EnclosingFQN = rename(s.FQN), rename(s.EnclosingFQN)
			if seen[s.FQN] {
				nu := s.FQN + suffix
				for n := 2; seen[nu]; n++ {
					nu = fmt.Sprintf("%s%s_%d", s.FQN, suffix, n)
				}
				renamed[s.FQN] = nu
				s.FQN = nu
			}
			seen[s.FQN] = true
		}
	}
	return modules
}

// SCIPIndexerCommand reports that no SCIP indexer is wired for Go yet.
func (*Adapter) SCIPIndexerCommand(_, _ string) ([]string, error) {
	return nil, adapters.ErrIndexerNotConfigured
}

// MutationRunnerCommand reports that no mutation runner is wired for Go yet.
func (*Adapter) MutationRunnerCommand(_ string, _ []string) ([]string, error) {
	return nil, adapters.ErrMutationRunnerNotConfigured
}
