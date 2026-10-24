package validate

import (
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkAttribution flags production modules that belong to no feature:
// neither a @module-feature nor a feature on any of their symbols. The
// other rules check that what is declared is consistent; this one checks
// that the declared graph covers the code at all, so a percentage over
// annotated code cannot hide the code nobody annotated.
//
// UNATTRIBUTED_MODULE is off by default. A workspace turns it on with
// `validate.severity.UNATTRIBUTED_MODULE: warning` (or error) in config.yaml.
func (c *corpus) checkAttribution() []schema.Violation {
	var v []schema.Violation
	for _, a := range c.kg.Attribution() {
		if a.ProductionSymbols == 0 || a.Attributed {
			continue
		}
		v = append(v, schema.Violation{
			Code:     schema.CodeUnattributedModule,
			Severity: schema.SeverityWarning,
			Subject:  a.File,
			Message: fmt.Sprintf("module %s has %d production symbol(s) and belongs to no feature",
				a.File, a.ProductionSymbols),
			Location: &schema.Location{File: a.File, Line: 1},
			NextAction: &schema.NextAction{
				Kind:       "add_annotation",
				Annotation: "@module-feature",
				Detail:     "attribute the module to the feature it implements, or exclude it in workspace.yaml if it is not product code",
			},
		})
	}
	return v
}
