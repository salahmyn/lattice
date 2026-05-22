// Package views renders human-readable views from the Lattice knowledge
// graph. Each view is a Go text/template; a repo may override any template by
// placing one at lattice/views/<name>.tmpl.
package views

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// funcs are the helpers available to every view template (builtin or
// repo-override): `inline` collapses a multi-line field to one line,
// `trimSpace` strips leading/trailing whitespace.
var funcs = template.FuncMap{
	"inline":    schema.InlineText,
	"trimSpace": strings.TrimSpace,
	"surfaceLabel": func(s schema.GraphSurface) string {
		if s.Path != "" {
			return strings.TrimSpace(s.Method + " " + s.Path)
		}
		return strings.TrimSpace(s.Type + " " + s.Name)
	},
}

//go:embed templates/*.tmpl
var builtinTemplates embed.FS

// Names lists the template-driven views.
var Names = []string{"developer", "product"}

// Render renders the named view from the knowledge graph. If viewsDir holds
// an override at <name>.tmpl, that template is used instead of the builtin.
func Render(viewsDir, name string, kg schema.KnowledgeGraph) (string, error) {
	tmplText, err := loadTemplate(viewsDir, name)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(name).Funcs(funcs).Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, kg); err != nil {
		return "", fmt.Errorf("render %s view: %w", name, err)
	}
	return buf.String(), nil
}

// loadTemplate returns the override template if present, else the builtin.
func loadTemplate(viewsDir, name string) (string, error) {
	override := filepath.Join(viewsDir, name+".tmpl")
	if data, err := os.ReadFile(override); err == nil {
		return string(data), nil
	}
	data, err := builtinTemplates.ReadFile("templates/" + name + ".tmpl")
	if err != nil {
		return "", fmt.Errorf("unknown view %q", name)
	}
	return string(data), nil
}
