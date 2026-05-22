// Package typescript is the Lattice language adapter for TypeScript and
// JavaScript source. Annotations are JSDoc tags; decorators are not supported.
package typescript

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Adapter implements adapters.LanguageAdapter for TypeScript/JavaScript.
type Adapter struct{}

// New returns a TypeScript adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the canonical language name.
func (*Adapter) Name() string { return "typescript" }

// FileExtensions returns the extensions this adapter owns.
func (*Adapter) FileExtensions() []string { return []string{".ts", ".tsx", ".js", ".jsx"} }

// CanParse reports whether path is a TS/JS file.
func (a *Adapter) CanParse(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".js", ".jsx":
		return true
	}
	return false
}

// Parse turns TS/JS source into an IR module via tree-sitter.
func (a *Adapter) Parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	return a.parse(ctx, path, source)
}

// jsdocTags maps annotation kinds to their JSDoc tag names.
var jsdocTags = map[string]string{
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

// RenderAnnotationSuggestion renders suggestions as a JSDoc comment block.
func (a *Adapter) RenderAnnotationSuggestion(_ ir.Symbol, suggested []adapters.AnnotationSuggestion) (string, error) {
	var b strings.Builder
	b.WriteString("/**\n")
	for _, s := range suggested {
		tag, ok := jsdocTags[s.Annotation]
		if !ok {
			tag = s.Annotation
		}
		b.WriteString(" * @")
		b.WriteString(tag)
		if args := renderArgs(s.Args); args != "" {
			b.WriteString(" ")
			b.WriteString(args)
		}
		b.WriteString("\n")
	}
	b.WriteString(" */\n")
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

// SCIPIndexerCommand returns the scip-typescript indexer command.
func (a *Adapter) SCIPIndexerCommand(repoPath, outputPath string) ([]string, error) {
	return []string{"scip-typescript", "index", "--cwd", repoPath, "--output", outputPath}, nil
}

// MutationRunnerCommand returns the Stryker command for the target files.
func (a *Adapter) MutationRunnerCommand(_ string, targetFiles []string) ([]string, error) {
	if len(targetFiles) == 0 {
		return nil, adapters.ErrMutationRunnerNotConfigured
	}
	return []string{"npx", "stryker", "run", "--mutate", strings.Join(targetFiles, ",")}, nil
}
