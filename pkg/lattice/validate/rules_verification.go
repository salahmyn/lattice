package validate

import (
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkVerification runs the verification-integrity rules.
func (c *corpus) checkVerification() []schema.Violation {
	enforced := c.enforcedInvariants()
	verified := c.verifiedRefs()
	implemented := c.implementedCapabilities()
	structural := c.structuralInvariants()

	var v []schema.Violation
	for i := range c.kg.Features {
		m := &c.kg.Features[i]
		loc := &schema.Location{File: m.SourcePath}

		for _, inv := range m.Invariants {
			methods := inv.EffectiveVerifiableBy()

			// UNENFORCED_INVARIANT.
			if !enforced[key(m.ID, inv.ID)] {
				v = append(v, schema.Violation{
					Code: schema.CodeUnenforcedInvariant, Severity: schema.SeverityError,
					FeatureID: m.ID, InvariantID: inv.ID, Location: loc,
					Message: fmt.Sprintf("invariant %s is declared but no code enforces it", inv.ID),
					NextAction: &schema.NextAction{
						Kind: "add_annotation", Annotation: "enforces_invariant",
						Ref: m.ID + ":" + inv.ID, TargetKind: "code",
					},
				})
			}

			// UNVERIFIED_INVARIANT (only when test verification is expected).
			if hasMethod(methods, schema.VerifiableByTest) && !verified[key(m.ID, inv.ID)] {
				v = append(v, schema.Violation{
					Code: schema.CodeUnverifiedInvariant, Severity: schema.SeverityError,
					FeatureID: m.ID, InvariantID: inv.ID, Location: loc,
					Message: fmt.Sprintf("invariant %s is declared but no test verifies it", inv.ID),
					NextAction: &schema.NextAction{
						Kind: "add_annotation", Annotation: "verifies",
						Ref: m.ID + ":" + inv.ID, TargetKind: "test",
					},
				})
			}

			// STRUCTURAL_CHECK_MISSING.
			if hasMethod(methods, schema.VerifiableByStructural) && !structural[key(m.ID, inv.ID)] {
				v = append(v, schema.Violation{
					Code: schema.CodeStructuralCheckMissing, Severity: schema.SeverityError,
					FeatureID: m.ID, InvariantID: inv.ID, Location: loc,
					Message:    fmt.Sprintf("invariant %s expects structural verification but no structural_check covers it", inv.ID),
					NextAction: &schema.NextAction{Kind: "add_structural_check", Ref: m.ID + ":" + inv.ID},
				})
			}

			// MUTATION_SCORE_BELOW_THRESHOLD.
			if score, ok := m.MutationScores[inv.ID]; ok {
				threshold := c.cfg.MutationTesting.Thresholds.ThresholdFor(m.ID + ":" + inv.ID)
				if score < threshold {
					v = append(v, schema.Violation{
						Code: schema.CodeMutationScoreBelowThreshold, Severity: schema.SeverityError,
						FeatureID: m.ID, InvariantID: inv.ID, Location: loc,
						Message: fmt.Sprintf("invariant %s mutation score %.0f%% is below threshold %.0f%%",
							inv.ID, score, threshold),
						NextAction: &schema.NextAction{Kind: "strengthen_tests", Ref: m.ID + ":" + inv.ID},
					})
				}
			}
		}

		// UNIMPLEMENTED_CAPABILITY (warning).
		for _, cap := range m.Capabilities {
			if !implemented[key(m.ID, cap.ID)] {
				v = append(v, schema.Violation{
					Code: schema.CodeUnimplementedCapability, Severity: schema.SeverityWarning,
					FeatureID: m.ID, CapabilityID: cap.ID, Location: loc,
					Message: fmt.Sprintf("capability %s is declared but no code implements it", cap.ID),
					NextAction: &schema.NextAction{
						Kind: "add_annotation", Annotation: "capability",
						Ref: m.ID + ":" + cap.ID, TargetKind: "code",
					},
				})
			}
		}
	}
	return v
}

// key joins a feature id and an item id.
func key(feature, item string) string { return feature + "\x00" + item }

func hasMethod(methods []schema.VerifiableBy, want schema.VerifiableBy) bool {
	for _, m := range methods {
		if m == want {
			return true
		}
	}
	return false
}

// enforcedInvariants returns the set of feature/invariant pairs that at least
// one code symbol or module enforces.
func (c *corpus) enforcedInvariants() map[string]bool {
	out := map[string]bool{}
	for _, s := range c.kg.Symbols {
		for _, inv := range s.EnforcesInvariants {
			f, item := resolveRef(inv, s.Feature)
			out[key(f, item)] = true
		}
	}
	for _, mod := range c.kg.Modules {
		for _, inv := range mod.EnforcesInvariants {
			f, item := resolveRef(inv, mod.Feature)
			out[key(f, item)] = true
		}
	}
	return out
}

// verifiedRefs returns the set of feature/item pairs at least one test verifies.
func (c *corpus) verifiedRefs() map[string]bool {
	out := map[string]bool{}
	for _, t := range c.kg.Tests {
		for _, ref := range t.Verifies {
			f, item := resolveRef(ref, t.Feature)
			out[key(f, item)] = true
		}
	}
	return out
}

// implementedCapabilities returns the set of implemented feature/capability pairs.
func (c *corpus) implementedCapabilities() map[string]bool {
	out := map[string]bool{}
	for _, s := range c.kg.Symbols {
		for _, cap := range s.Capabilities {
			f, item := resolveRef(cap, s.Feature)
			out[key(f, item)] = true
		}
	}
	return out
}

// structuralInvariants returns the set of feature/invariant pairs covered by a
// declared structural check.
func (c *corpus) structuralInvariants() map[string]bool {
	out := map[string]bool{}
	for _, sc := range c.kg.StructuralChecks {
		for _, inv := range sc.VerifiesInvariants {
			f, item := resolveRef(inv, sc.Feature)
			out[key(f, item)] = true
		}
	}
	return out
}
