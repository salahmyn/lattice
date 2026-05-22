package validate

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkSurfaces verifies that the interaction surfaces a feature manifest
// declares and the surfaces the code actually exposes agree. A mismatch is a
// warning: it surfaces drift without failing the build.
func (c *corpus) checkSurfaces() []schema.Violation {
	var v []schema.Violation
	for _, s := range c.kg.Surfaces {
		label := surfaceLabel(s)
		switch {
		case s.Declared && !s.Implemented:
			loc := &schema.Location{}
			if m := c.features[s.Feature]; m != nil {
				loc.File = m.SourcePath
			}
			v = append(v, schema.Violation{
				Code: schema.CodeSurfaceUnimplemented, Severity: schema.SeverityWarning,
				FeatureID: s.Feature, Location: loc,
				Message: fmt.Sprintf("surface %q is declared by feature %s but no code exposes it",
					label, s.Feature),
				NextAction: &schema.NextAction{
					Kind: "implement_surface", Ref: label,
					Detail: "add a route or handler exposing this surface, or remove it from the manifest",
				},
			})
		case s.Implemented && !s.Declared:
			loc := &schema.Location{}
			if len(s.ImplementedBy) > 0 {
				loc.File = s.ImplementedBy[0].File
				loc.Line = s.ImplementedBy[0].Line
			}
			v = append(v, schema.Violation{
				Code: schema.CodeSurfaceUndeclared, Severity: schema.SeverityWarning,
				FeatureID: s.Feature, Location: loc,
				Message: fmt.Sprintf("surface %q is exposed by code but no feature manifest declares it",
					label),
				NextAction: &schema.NextAction{
					Kind: "declare_surface", Ref: label,
					Detail: "add this surface to the owning feature manifest, or remove the route",
				},
			})
		}
	}
	return v
}

// surfaceLabel renders a surface as a short human-readable string.
func surfaceLabel(s schema.GraphSurface) string {
	if s.Path != "" {
		return strings.TrimSpace(s.Method + " " + s.Path)
	}
	return strings.TrimSpace(s.Type + " " + s.Name)
}
