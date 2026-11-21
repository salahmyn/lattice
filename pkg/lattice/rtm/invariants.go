package rtm

// @module-feature rtm

import (
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// RequiredInvariants is the set of invariants some criterion names in
// maps_to_invariant. Criteria of every BRD count, whatever its status, so a
// draft or a superseded BRD still makes an invariant required. The audit's
// line view and the invariant figure of the RTM share this one rule.
// @enforces rtm:INV-9
func RequiredInvariants(brds []schema.BRD) map[string]bool {
	required := map[string]bool{}
	for _, b := range brds {
		for _, sc := range b.SuccessCriteria {
			for _, r := range sc.MapsToInvariant {
				required[r] = true
			}
		}
	}
	return required
}

// InvariantCoverage is the second figure of the RTM header: how many of the
// declared invariants some criterion requires. It never replaces the criteria
// figure, which counts criteria, not invariants.
type InvariantCoverage struct {
	TotalInvariants    int     `json:"total_invariants"`
	RequiredInvariants int     `json:"required_invariants"`
	Ratio              float64 `json:"ratio"`
}

// UnrequiredFeature lists the invariants of one feature that no criterion
// names, as "<feature>:<INV-N>" refs in declaration order. It includes
// invariants no block carries.
type UnrequiredFeature struct {
	Feature string   `json:"feature"`
	Refs    []string `json:"refs"`
}

// ComputeInvariantCoverage counts every declared invariant and the ones a
// criterion requires, and lists the rest per feature, in feature id order.
// @enforces rtm:INV-9
func ComputeInvariantCoverage(kg schema.KnowledgeGraph) (InvariantCoverage, []UnrequiredFeature) {
	required := RequiredInvariants(kg.BRDs)
	var c InvariantCoverage
	var out []UnrequiredFeature
	for _, f := range kg.Features {
		var refs []string
		for _, inv := range f.Invariants {
			c.TotalInvariants++
			ref := f.ID + ":" + inv.ID
			if required[ref] {
				c.RequiredInvariants++
			} else {
				refs = append(refs, ref)
			}
		}
		if len(refs) > 0 {
			out = append(out, UnrequiredFeature{Feature: f.ID, Refs: refs})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Feature < out[j].Feature })
	if c.TotalInvariants > 0 {
		c.Ratio = round4(float64(c.RequiredInvariants) / float64(c.TotalInvariants))
	}
	return c, out
}
