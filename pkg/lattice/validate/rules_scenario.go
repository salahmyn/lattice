package validate

import (
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkScenarios runs the v0.8 α (executable scenario verifiers) and β
// (entry-point journey coverage) rules. They make a BRD user_scenario a
// verifiable unit peer to a success_criterion, and make reachability a
// signal rather than an assumption.
//
//	BRD_SCENARIO_UNMAPPED    (info)    — scenario declares no verified_by
//	BRD_SCENARIO_UNVERIFIED  (warning) — verified_by resolves to no test
//	VERIFIER_FAILING         (warning) — an ingested result is red
//	SCENARIO_NO_ENTRYPOINT   (info)    — verified only at pure-logic altitude
//	FEATURE_UNREACHED        (info)    — production feature on no EP flow
func (c *corpus) checkScenarios() []schema.Violation {
	var v []schema.Violation

	matrix := rtm.Build(c.kg, rtm.Options{
		MutationThreshold: c.cfg.MutationTesting.Thresholds.Default,
		ResultOf:          c.opts.ResultOf,
	})

	brdLoc := func(brdID string) *schema.Location {
		for i := range c.kg.BRDs {
			if c.kg.BRDs[i].ID == brdID {
				return &schema.Location{File: c.kg.BRDs[i].SourcePath}
			}
		}
		return nil
	}

	for _, s := range matrix.Scenarios {
		loc := brdLoc(s.BRDID)
		switch s.Status {
		case rtm.StatusUnmapped:
			v = append(v, schema.Violation{
				Code: schema.CodeBRDScenarioUnmapped, Severity: schema.SeverityInfo,
				Message: fmt.Sprintf("BRD %q scenario %s declares no verified_by — what the user does is untraced",
					s.BRDID, s.ScenarioID),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "user_scenarios.verified_by",
					Detail: "add verified_by: [<test-fqn>|<entry-point-id>] so the scenario can be demonstrated",
				},
			})
		case rtm.StatusUnverified:
			v = append(v, schema.Violation{
				Code: schema.CodeBRDScenarioUnverified, Severity: schema.SeverityWarning,
				Message: fmt.Sprintf("BRD %q scenario %s: %s",
					s.BRDID, s.ScenarioID, s.StatusReason),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "add_verification",
					Detail: "tag a test `@verifies " + s.BRDID + ":" + s.ScenarioID + "` (or fix the verified_by ref)",
				},
			})
		case rtm.StatusFailing:
			v = append(v, schema.Violation{
				Code: schema.CodeVerifierFailing, Severity: schema.SeverityWarning,
				Message: fmt.Sprintf("BRD %q scenario %s: %s",
					s.BRDID, s.ScenarioID, s.StatusReason),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "fix_test",
					Detail: "the scenario verifier is failing on the generated commit",
				},
			})
		}

		// β — SCENARIO_NO_ENTRYPOINT: the scenario has a verifier but it
		// exercises no declared entry point, so it asserts pure logic, not
		// a user-reachable path. Only meaningful once a verifier resolves.
		if !s.TouchesEntryPoint &&
			(s.Status == rtm.StatusVerified || s.Status == rtm.StatusDemonstrated || s.Status == rtm.StatusFailing) {
			v = append(v, schema.Violation{
				Code: schema.CodeScenarioNoEntryPoint, Severity: schema.SeverityInfo,
				Message: fmt.Sprintf("BRD %q scenario %s is verified at pure-logic altitude — no entry point in verified_by",
					s.BRDID, s.ScenarioID),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "user_scenarios.verified_by",
					Detail: "add the declared entry point id the scenario is reached through, so it counts toward journey coverage",
				},
			})
		}
	}

	v = append(v, c.checkFeatureReach()...)
	return v
}

// checkFeatureReach fires FEATURE_UNREACHED (β) for a production feature
// that sits on no entry-point flow — either dead code or an unmodeled
// seam (the two states a CLI↔store boundary can hide in). Only runs when
// at least one entry point is declared, otherwise every feature would
// trivially be "unreached".
func (c *corpus) checkFeatureReach() []schema.Violation {
	if len(c.kg.EntryPoints) == 0 {
		return nil
	}
	reached := map[string]bool{}
	for _, ep := range c.kg.EntryPoints {
		for _, step := range ep.Flow {
			reached[step.Feature] = true
		}
	}
	var v []schema.Violation
	for i := range c.kg.Features {
		f := &c.kg.Features[i]
		if f.Status != schema.StatusProduction || reached[f.ID] {
			continue
		}
		// An umbrella/parent feature with children is reached transitively
		// if any child is reached — don't flag pure groupers.
		if len(f.Children) > 0 && anyChildReached(f, reached) {
			continue
		}
		v = append(v, schema.Violation{
			Code: schema.CodeFeatureUnreached, Severity: schema.SeverityInfo,
			FeatureID: f.ID,
			Message:   fmt.Sprintf("production feature %q is on no entry-point flow — dead code or an unmodeled seam", f.ID),
			Location:  &schema.Location{File: f.SourcePath},
			NextAction: &schema.NextAction{
				Kind:   "edit_entry_point",
				Detail: "declare the trigger that reaches " + f.ID + ", or retire the feature",
			},
		})
	}
	return v
}

func anyChildReached(f *schema.Manifest, reached map[string]bool) bool {
	for _, ch := range f.Children {
		if reached[ch] {
			return true
		}
	}
	return false
}
