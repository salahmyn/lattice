package validate

import (
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkErrors verifies that the error contract a feature manifest declares and
// the errors its code actually raises agree. A mismatch is a warning: it
// surfaces drift in the API's failure modes without failing the build.
func (c *corpus) checkErrors() []schema.Violation {
	var v []schema.Violation
	for _, e := range c.kg.Errors {
		switch {
		case e.Declared && !e.Implemented:
			loc := &schema.Location{}
			if m := c.features[e.Feature]; m != nil {
				loc.File = m.SourcePath
			}
			v = append(v, schema.Violation{
				Code: schema.CodeErrorUnimplemented, Severity: schema.SeverityWarning,
				FeatureID: e.Feature, Location: loc,
				Message: fmt.Sprintf("error %q is declared by feature %s but no code raises it",
					e.Code, e.Feature),
				NextAction: &schema.NextAction{
					Kind: "implement_error", Ref: e.Code,
					Detail: "raise this error in code with an @error annotation, or remove it from the manifest",
				},
			})
		case e.Implemented && !e.Declared:
			loc := &schema.Location{}
			if len(e.RaisedBy) > 0 {
				loc.File = e.RaisedBy[0].File
				loc.Line = e.RaisedBy[0].Line
			}
			v = append(v, schema.Violation{
				Code: schema.CodeErrorUndeclared, Severity: schema.SeverityWarning,
				FeatureID: e.Feature, Location: loc,
				Message: fmt.Sprintf("error %q is raised by code but no feature manifest declares it",
					e.Code),
				NextAction: &schema.NextAction{
					Kind: "declare_error", Ref: e.Code,
					Detail: "add this error to the owning feature manifest's errors list",
				},
			})
		}
	}
	return v
}
