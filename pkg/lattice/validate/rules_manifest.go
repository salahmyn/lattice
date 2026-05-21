package validate

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkManifests runs the manifest-integrity rules.
func (c *corpus) checkManifests() []schema.Violation {
	var v []schema.Violation
	seenID := map[string]string{} // id -> first source path

	for i := range c.kg.Features {
		m := &c.kg.Features[i]
		loc := &schema.Location{File: m.SourcePath}

		// MANIFEST_SCHEMA: required fields and enum validity.
		for _, msg := range manifestSchemaErrors(m) {
			v = append(v, schema.Violation{
				Code: schema.CodeManifestSchema, Severity: schema.SeverityError,
				FeatureID: m.ID, Message: msg, Location: loc,
				NextAction: &schema.NextAction{Kind: "edit_manifest", Detail: msg},
			})
		}

		// MANIFEST_ID_FORMAT.
		if m.ID != "" && !idPattern.MatchString(m.ID) {
			v = append(v, schema.Violation{
				Code: schema.CodeManifestIDFormat, Severity: schema.SeverityError,
				FeatureID: m.ID, Location: loc,
				Message: fmt.Sprintf("feature id %q does not match the required format", m.ID),
				NextAction: &schema.NextAction{
					Kind: "edit_manifest", Field: "id",
					Detail: "id must match ^[a-z][a-z0-9_]*(\\.[a-z][a-z0-9_]*)*$",
				},
			})
		}

		// MANIFEST_ID_DUPLICATE.
		if m.ID != "" {
			if first, dup := seenID[m.ID]; dup {
				v = append(v, schema.Violation{
					Code: schema.CodeManifestIDDuplicate, Severity: schema.SeverityError,
					FeatureID: m.ID, Location: loc,
					Message: fmt.Sprintf("feature id %q is already declared in %s", m.ID, first),
					NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "id",
						Detail: "feature ids must be globally unique"},
				})
			} else {
				seenID[m.ID] = m.SourcePath
			}
		}

		// SUBFEATURE_DEPTH_EXCEEDED.
		if depth := strings.Count(m.ID, ".") + 1; depth > maxSubfeatureDepth {
			v = append(v, schema.Violation{
				Code: schema.CodeSubfeatureDepthExceeded, Severity: schema.SeverityWarning,
				FeatureID: m.ID, Location: loc,
				Message: fmt.Sprintf("feature id is %d levels deep; consider a flatter taxonomy", depth),
			})
		}
	}
	return v
}

// manifestSchemaErrors returns human-readable schema problems for a manifest.
func manifestSchemaErrors(m *schema.Manifest) []string {
	var errs []string
	if strings.TrimSpace(m.ID) == "" {
		errs = append(errs, "missing required field: id")
	}
	if m.Version < 1 {
		errs = append(errs, "field version must be >= 1")
	}
	if !validStatus(m.Status) {
		errs = append(errs, fmt.Sprintf("invalid status %q (want proposal|accepted|production|deprecated)", m.Status))
	}
	if strings.TrimSpace(m.Purpose) == "" {
		errs = append(errs, "missing required field: purpose")
	}
	if strings.TrimSpace(m.Owners.Business) == "" {
		errs = append(errs, "missing required field: owners.business")
	}
	if strings.TrimSpace(m.Owners.Engineering) == "" {
		errs = append(errs, "missing required field: owners.engineering")
	}
	for _, cap := range m.Capabilities {
		if strings.TrimSpace(cap.ID) == "" {
			errs = append(errs, "capability is missing an id")
		}
		if len(cap.Rules) == 0 {
			errs = append(errs, fmt.Sprintf("capability %q must declare at least one rule", cap.ID))
		}
	}
	for _, inv := range m.Invariants {
		if strings.TrimSpace(inv.ID) == "" {
			errs = append(errs, "invariant is missing an id")
		}
		if strings.TrimSpace(inv.Statement) == "" {
			errs = append(errs, fmt.Sprintf("invariant %q is missing a statement", inv.ID))
		}
	}
	for _, s := range m.Surface {
		if !validSurfaceType(s.Type) {
			errs = append(errs, fmt.Sprintf("invalid surface type %q", s.Type))
		}
	}
	return errs
}

func validStatus(s schema.Status) bool {
	for _, ok := range schema.ValidStatuses {
		if s == ok {
			return true
		}
	}
	return false
}

func validSurfaceType(t schema.SurfaceType) bool {
	switch t {
	case schema.SurfaceHTTP, schema.SurfaceEventEmit, schema.SurfaceEventConsume,
		schema.SurfaceWebhookReceive, schema.SurfaceScheduled, schema.SurfaceModule:
		return true
	}
	return false
}
