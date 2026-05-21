package validate

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkAnnotations runs the annotation-integrity rules over every code and
// test symbol, plus module-level annotations.
func (c *corpus) checkAnnotations() []schema.Violation {
	var v []schema.Violation

	all := append(append([]schema.GraphSymbol(nil), c.kg.Symbols...), c.kg.Tests...)
	for _, s := range all {
		loc := &schema.Location{File: s.File, Line: s.Line}

		// ORPHAN_ANNOTATION_FEATURE.
		if s.Feature != "" {
			if _, ok := c.features[s.Feature]; !ok {
				v = append(v, schema.Violation{
					Code: schema.CodeOrphanAnnotationFeature, Severity: schema.SeverityError,
					FeatureID: s.Feature, Location: loc,
					Message: fmt.Sprintf("%s annotates feature %q which has no manifest", s.FQN, s.Feature),
					NextAction: &schema.NextAction{Kind: "create_manifest", Ref: s.Feature,
						Detail: "create a manifest for the feature or correct the annotation"},
				})
			}
		}

		// ORPHAN_ANNOTATION_CAPABILITY.
		for _, cap := range s.Capabilities {
			f, item := resolveRef(cap, s.Feature)
			if !c.hasCapability(f, item) {
				v = append(v, schema.Violation{
					Code: schema.CodeOrphanAnnotationCapability, Severity: schema.SeverityError,
					FeatureID: f, CapabilityID: item, Location: loc,
					Message:    fmt.Sprintf("%s references capability %q not declared on feature %q", s.FQN, item, f),
					NextAction: &schema.NextAction{Kind: "add_capability", Ref: f + ":" + item},
				})
			}
		}

		// ORPHAN_ANNOTATION_INVARIANT.
		for _, inv := range s.EnforcesInvariants {
			f, item := resolveRef(inv, s.Feature)
			if !c.hasInvariant(f, item) {
				v = append(v, schema.Violation{
					Code: schema.CodeOrphanAnnotationInvariant, Severity: schema.SeverityError,
					FeatureID: f, InvariantID: item, Location: loc,
					Message:    fmt.Sprintf("%s enforces invariant %q not declared on feature %q", s.FQN, item, f),
					NextAction: &schema.NextAction{Kind: "add_invariant", Ref: f + ":" + item},
				})
			}
		}

		// ORPHAN_ANNOTATION_ROLE.
		for _, role := range s.Roles {
			if !c.roles[role] {
				v = append(v, schema.Violation{
					Code: schema.CodeOrphanAnnotationRole, Severity: schema.SeverityError,
					Location: loc,
					Message:  fmt.Sprintf("%s uses role %q which is declared in no manifest", s.FQN, role),
					NextAction: &schema.NextAction{Kind: "add_role", Ref: role,
						Detail: "declare the role under a manifest's roles: section"},
				})
			}
		}

		// verifies refs resolve to either an invariant or a capability.
		for _, ref := range s.Verifies {
			f, item := resolveRef(ref, s.Feature)
			if c.hasInvariant(f, item) || c.hasCapability(f, item) {
				continue
			}
			code := schema.CodeOrphanAnnotationCapability
			if strings.HasPrefix(strings.ToUpper(item), "INV-") {
				code = schema.CodeOrphanAnnotationInvariant
			}
			v = append(v, schema.Violation{
				Code: code, Severity: schema.SeverityError,
				FeatureID: f, Location: loc,
				Message: fmt.Sprintf("%s verifies %q which is not declared on feature %q", s.FQN, item, f),
			})
		}

		// SUPPRESSION_WITHOUT_REASON.
		for _, sup := range s.SuppressedInvariants {
			if strings.TrimSpace(sup.Reason) == "" {
				v = append(v, schema.Violation{
					Code: schema.CodeSuppressionWithoutReason, Severity: schema.SeverityError,
					InvariantID: sup.Invariant, Location: loc,
					Message: fmt.Sprintf("%s suppresses %s without a reason", s.FQN, sup.Invariant),
					NextAction: &schema.NextAction{Kind: "add_annotation",
						Annotation: "suppresses_invariant",
						Detail:     "suppression requires a reason: argument"},
				})
			}
		}

		// DEPENDS_ON_FEATURE_NOT_DECLARED: code depends on a feature the
		// owning manifest does not declare in depends_on.
		if s.Feature != "" {
			if m, ok := c.features[s.Feature]; ok {
				declared := map[string]bool{}
				for _, d := range m.DependsOn {
					declared[d] = true
				}
				for _, dep := range s.DependsOnFeatures {
					if dep != s.Feature && !declared[dep] {
						v = append(v, schema.Violation{
							Code: schema.CodeDependsOnFeatureNotDeclared, Severity: schema.SeverityWarning,
							FeatureID: s.Feature, Location: loc,
							Message:    fmt.Sprintf("%s depends on %q but manifest %q does not declare it", s.FQN, dep, s.Feature),
							NextAction: &schema.NextAction{Kind: "edit_manifest", Field: "depends_on", Ref: dep},
						})
					}
				}
			}
		}
	}

	// Module-level feature orphans.
	for _, mod := range c.kg.Modules {
		if mod.Feature != "" {
			if _, ok := c.features[mod.Feature]; !ok {
				v = append(v, schema.Violation{
					Code: schema.CodeOrphanAnnotationFeature, Severity: schema.SeverityError,
					FeatureID: mod.Feature, Location: &schema.Location{File: mod.File},
					Message: fmt.Sprintf("module %s annotates feature %q which has no manifest", mod.File, mod.Feature),
				})
			}
		}
	}
	return v
}

func (c *corpus) hasCapability(feature, id string) bool {
	caps, ok := c.caps[feature]
	return ok && caps[id]
}

func (c *corpus) hasInvariant(feature, id string) bool {
	invs, ok := c.invs[feature]
	return ok && invs[id]
}
