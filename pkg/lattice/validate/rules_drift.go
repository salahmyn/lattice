package validate

// @module-feature validation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkDrift reports where the spec and the code part ways. Both findings are
// info and always on; each states a fact the graph holds and decides nothing.
//
//	INVARIANT_UNREQUIRED (info) — a declared invariant no criterion maps
//	STATUS_DRIFT         (info) — a proposal or accepted feature on the flow
//	                              of a production entry point
//
// @enforces validation:INV-21
func (c *corpus) checkDrift() []schema.Violation {
	return append(c.checkInvariantsUnrequired(), c.checkStatusDrift()...)
}

// checkInvariantsUnrequired fires INVARIANT_UNREQUIRED, one finding per
// feature, for every declared invariant that no criterion's
// maps_to_invariant names. An invariant no block carries counts too, which
// the audit's line view never lists. The set of required invariants is the
// one the audit shares (rtm.RequiredInvariants). The message says what is
// true, "mapped by no criterion", and nothing about importance.
// @enforces validation:INV-18
func (c *corpus) checkInvariantsUnrequired() []schema.Violation {
	_, unrequired := rtm.ComputeInvariantCoverage(c.kg)
	loc := map[string]*schema.Location{}
	for i := range c.kg.Features {
		loc[c.kg.Features[i].ID] = &schema.Location{File: c.kg.Features[i].SourcePath}
	}
	var v []schema.Violation
	for _, u := range unrequired {
		v = append(v, schema.Violation{
			Code: schema.CodeInvariantUnrequired, Severity: schema.SeverityInfo,
			FeatureID: u.Feature,
			Subject:   u.Feature,
			Refs:      u.Refs,
			Message:   unrequiredMessage(u.Feature, u.Refs),
			Location:  loc[u.Feature],
			NextAction: &schema.NextAction{
				Kind:  "edit_brd",
				Field: "success_criteria.maps_to_invariant",
				Detail: "map each from a criterion of the BRD it serves (maps_to_invariant: <feature>:<INV-N>), " +
					"or allowlist it with a reason",
			},
		})
	}
	return v
}

// unrequiredMessage words an INVARIANT_UNREQUIRED finding for the given refs.
func unrequiredMessage(feature string, refs []string) string {
	noun := "invariants"
	if len(refs) == 1 {
		noun = "invariant"
	}
	return fmt.Sprintf("feature %q has %d %s mapped by no criterion: %s",
		feature, len(refs), noun, strings.Join(refs, ", "))
}

// checkStatusDrift fires STATUS_DRIFT (info) for a proposal or accepted
// feature that the flow of a production entry point names. Only a declared
// production entry point counts: reach from a root, a passing verifier or a
// feature flag is never read as "in production". The message names the entry
// points and states the status; the next action names the command that
// prints the evidence and writes nothing.
// @enforces validation:INV-19
func (c *corpus) checkStatusDrift() []schema.Violation {
	via := map[string][]string{} // feature -> production entry points naming it
	for _, ep := range c.kg.EntryPoints {
		if ep.Status != schema.StatusProduction {
			continue
		}
		seen := map[string]bool{}
		for _, step := range ep.Flow {
			if seen[step.Feature] {
				continue
			}
			seen[step.Feature] = true
			via[step.Feature] = append(via[step.Feature], ep.ID)
		}
	}
	var v []schema.Violation
	for i := range c.kg.Features {
		f := &c.kg.Features[i]
		if f.Status != schema.StatusProposal && f.Status != schema.StatusAccepted {
			continue
		}
		eps := via[f.ID]
		if len(eps) == 0 {
			continue
		}
		sort.Strings(eps)
		noun := "entry point"
		if len(eps) > 1 {
			noun = "entry points"
		}
		v = append(v, schema.Violation{
			Code: schema.CodeStatusDrift, Severity: schema.SeverityInfo,
			FeatureID: f.ID,
			Subject:   f.ID,
			Message: fmt.Sprintf("feature %q has status %s, and the flow of the production %s %s names it",
				f.ID, f.Status, noun, strings.Join(eps, ", ")),
			Location: &schema.Location{File: f.SourcePath},
			NextAction: &schema.NextAction{
				Kind:  "edit_manifest",
				Field: "status",
				Detail: "see the evidence with `lattice feature promote " + f.ID + "` (without --by it prints and writes nothing), " +
					"then set the status by hand, or allowlist this finding with a reason",
			},
		})
	}
	return v
}
