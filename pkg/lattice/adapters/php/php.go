// Package php is the Lattice language adapter for PHP 8.0+ source.
// Annotations are PHP 8 attributes; there is no docblock fallback.
package php

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Adapter implements adapters.LanguageAdapter for PHP.
type Adapter struct{}

// New returns a PHP adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the canonical language name.
func (*Adapter) Name() string { return "php" }

// FileExtensions returns the extensions this adapter owns.
func (*Adapter) FileExtensions() []string { return []string{".php"} }

// CanParse reports whether path is a PHP file.
func (a *Adapter) CanParse(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".php")
}

// Parse turns PHP source into an IR module via tree-sitter.
func (a *Adapter) Parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	return a.parse(ctx, path, source)
}

// attributeNames maps annotation kinds to their PHP attribute class names.
var attributeNames = map[string]string{
	"feature":                   "Feature",
	"capability":                "Capability",
	"feature_capability":        "Capability",
	"enforces_invariant":        "EnforcesInvariant",
	"verifies":                  "Verifies",
	"verifies_capability":       "VerifiesCapability",
	"depends_on_feature":        "DependsOnFeature",
	"role":                      "Role",
	"suppresses_invariant":      "SuppressesInvariant",
	"surface":                   "Surface",
	"error":                     "Error",
	"module_feature":            "ModuleFeature",
	"module_enforces_invariant": "ModuleEnforcesInvariant",
	"module_depends_on_feature": "ModuleDependsOnFeature",
}

// RenderAnnotationSuggestion renders suggestions as PHP 8 attribute lines.
func (a *Adapter) RenderAnnotationSuggestion(_ ir.Symbol, suggested []adapters.AnnotationSuggestion) (string, error) {
	var b strings.Builder
	for _, s := range suggested {
		name, ok := attributeNames[s.Annotation]
		if !ok {
			name = s.Annotation
		}
		b.WriteString("#[")
		b.WriteString(name)
		b.WriteString("(")
		b.WriteString(renderArgs(s.Args))
		b.WriteString(")]\n")
	}
	return b.String(), nil
}

func renderArgs(args []interface{}) string {
	var parts []string
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("'%s'", v))
		case []string:
			for _, s := range v {
				parts = append(parts, fmt.Sprintf("'%s'", s))
			}
		case []interface{}:
			for _, s := range v {
				parts = append(parts, fmt.Sprintf("'%s'", fmt.Sprint(s)))
			}
		default:
			parts = append(parts, fmt.Sprintf("'%s'", fmt.Sprint(v)))
		}
	}
	return strings.Join(parts, ", ")
}

// SCIPIndexerCommand returns the scip-php indexer command.
func (a *Adapter) SCIPIndexerCommand(repoPath, outputPath string) ([]string, error) {
	return []string{"scip-php", "index", "--cwd", repoPath, "--output", outputPath}, nil
}

// MutationRunnerCommand returns the Infection command.
func (a *Adapter) MutationRunnerCommand(_ string, _ []string) ([]string, error) {
	return []string{"vendor/bin/infection", "--only-covered", "--show-mutations"}, nil
}
