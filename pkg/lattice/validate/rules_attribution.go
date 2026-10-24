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
	tests := map[string]bool{}
	for _, t := range c.kg.Tests {
		tests[t.FQN] = true
	}
	featureOf := map[string]string{}
	for _, s := range c.kg.Symbols {
		if s.Feature != "" {
			featureOf[s.FQN] = s.Feature
		}
	}
	var v []schema.Violation
	for _, mod := range c.kg.Modules {
		if mod.Feature != "" {
			continue
		}
		production, attributed := 0, false
		for _, fqn := range mod.SymbolFQNs {
			if tests[fqn] {
				continue
			}
			production++
			if featureOf[fqn] != "" {
				attributed = true
				break
			}
		}
		if production == 0 || attributed {
			continue
		}
		v = append(v, schema.Violation{
			Code:     schema.CodeUnattributedModule,
			Severity: schema.SeverityWarning,
			Subject:  mod.File,
			Message: fmt.Sprintf("module %s has %d production symbol(s) and belongs to no feature",
				mod.File, production),
			Location: &schema.Location{File: mod.File, Line: 1},
			NextAction: &schema.NextAction{
				Kind:       "add_annotation",
				Annotation: "@module-feature",
				Detail:     "attribute the module to the feature it implements, or exclude it in workspace.yaml if it is not product code",
			},
		})
	}
	return v
}
