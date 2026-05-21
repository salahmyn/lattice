// Package python is the Lattice language adapter for Python source.
//
// Annotations are decorators imported from the no-op `lattice` PyPI package.
// Parsing uses tree-sitter (wired in the tree-sitter adapter milestone); the
// command lines for SCIP and mutation are final.
package python

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Adapter implements adapters.LanguageAdapter for Python.
type Adapter struct{}

// New returns a Python adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the canonical language name.
func (*Adapter) Name() string { return "python" }

// FileExtensions returns the extensions this adapter owns.
func (*Adapter) FileExtensions() []string { return []string{".py"} }

// CanParse reports whether path is a Python file.
func (a *Adapter) CanParse(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".py")
}

// Parse turns Python source into an IR module via tree-sitter.
func (a *Adapter) Parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	return a.parse(ctx, path, source)
}

// decoratorNames maps annotation kinds to their Python decorator names.
var decoratorNames = map[string]string{
	"feature":                   "feature",
	"capability":                "feature_capability",
	"feature_capability":        "feature_capability",
	"enforces_invariant":        "enforces_invariant",
	"verifies":                  "verifies",
	"verifies_capability":       "verifies_capability",
	"depends_on_feature":        "depends_on_feature",
	"role":                      "role",
	"suppresses_invariant":      "suppresses_invariant",
	"module_feature":            "module_feature",
	"module_enforces_invariant": "module_enforces_invariant",
	"module_depends_on_feature": "module_depends_on_feature",
}

// RenderAnnotationSuggestion renders suggestions as Python decorator lines.
func (a *Adapter) RenderAnnotationSuggestion(_ ir.Symbol, suggested []adapters.AnnotationSuggestion) (string, error) {
	var b strings.Builder
	for _, s := range suggested {
		name, ok := decoratorNames[s.Annotation]
		if !ok {
			name = s.Annotation
		}
		b.WriteString("@")
		b.WriteString(name)
		b.WriteString("(")
		b.WriteString(renderArgs(s.Args))
		b.WriteString(")\n")
	}
	return b.String(), nil
}

func renderArgs(args []interface{}) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%q", v))
		case []string:
			for _, s := range v {
				parts = append(parts, fmt.Sprintf("%q", s))
			}
		case []interface{}:
			for _, s := range v {
				parts = append(parts, fmt.Sprintf("%q", fmt.Sprint(s)))
			}
		default:
			parts = append(parts, fmt.Sprintf("%q", fmt.Sprint(v)))
		}
	}
	return strings.Join(parts, ", ")
}

// SCIPIndexerCommand returns the scip-python indexer command.
func (a *Adapter) SCIPIndexerCommand(repoPath string) ([]string, error) {
	return []string{
		"scip-python", "index",
		"--cwd", repoPath,
		"--output", filepath.Join(repoPath, ".lattice", "scip", "python.scip"),
	}, nil
}

// MutationRunnerCommand returns the mutmut command for the target files.
func (a *Adapter) MutationRunnerCommand(_ string, targetFiles []string) ([]string, error) {
	if len(targetFiles) == 0 {
		return nil, adapters.ErrMutationRunnerNotConfigured
	}
	return []string{"mutmut", "run", "--paths-to-mutate", strings.Join(targetFiles, ",")}, nil
}
