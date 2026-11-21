package rtm

import (
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Criteria of every BRD count, drafts and superseded ones included, and an
// invariant no block carries is still declared, so it still counts in the
// figure and still has to be mapped.
// @verifies rtm:INV-9
func TestInvariantCoverageCountsEveryBRD(t *testing.T) {
	kg := schema.KnowledgeGraph{
		BRDs: []schema.BRD{
			{ID: "brd.a", Status: schema.BRDApproved, SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"fees:INV-1"}}}},
			{ID: "brd.b", Status: schema.BRDDraft, SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"fees:INV-2"}}}},
			{ID: "brd.c", Status: schema.BRDSuperseded, SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"wallet:INV-1"}}}},
		},
		Features: []schema.Manifest{
			{ID: "wallet", Invariants: []schema.Invariant{{ID: "INV-1"}, {ID: "INV-2"}}},
			{ID: "fees", Invariants: []schema.Invariant{{ID: "INV-1"}, {ID: "INV-2"}, {ID: "INV-3"}}},
		},
	}
	if got := RequiredInvariants(kg.BRDs); !reflect.DeepEqual(got, map[string]bool{
		"fees:INV-1": true, "fees:INV-2": true, "wallet:INV-1": true}) {
		t.Errorf("required = %v", got)
	}
	c, unrequired := ComputeInvariantCoverage(kg)
	if c.TotalInvariants != 5 || c.RequiredInvariants != 3 || c.Ratio != 0.6 {
		t.Errorf("coverage = %+v, want 3 of 5", c)
	}
	want := []UnrequiredFeature{
		{Feature: "fees", Refs: []string{"fees:INV-3"}},
		{Feature: "wallet", Refs: []string{"wallet:INV-2"}},
	}
	if !reflect.DeepEqual(unrequired, want) {
		t.Errorf("unrequired = %+v, want %+v", unrequired, want)
	}
	// The criteria figure keeps its meaning: it counts criteria, not invariants.
	if cc := ComputeCoverage(Build(kg, Options{})); cc.TotalCriteria != 3 {
		t.Errorf("criteria figure = %+v", cc)
	}
}
