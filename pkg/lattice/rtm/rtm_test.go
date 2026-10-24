package rtm

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// fixture builds a minimal KnowledgeGraph with one BRD pointing at
// one feature that has one invariant. Callers tweak it for each test
// (drop the enforcer, change maps_to_invariant, etc).
func fixture() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.checkout", Title: "Checkout", Status: schema.BRDApproved, Version: 1,
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "Refund never exceeds charge",
					MapsToInvariant: schema.InvariantRefs{"checkout:INV-1"}},
			},
		}},
		Features: []schema.Manifest{{
			ID: "checkout", Version: 1, Status: schema.StatusProduction,
			Purpose: "checkout",
			Owners:  schema.Owners{Business: "b", Engineering: "e"},
			Invariants: []schema.Invariant{
				{ID: "INV-1", Statement: "refund <= charge"},
			},
		}},
		Symbols: []schema.GraphSymbol{
			{FQN: "Checkout\\Refund", Feature: "checkout", EnforcesInvariants: []string{"INV-1"}},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "Tests\\Checkout\\RefundTest", IsTest: true, Verifies: []string{"checkout:INV-1"}},
		},
	}
}

func TestVerifiedHappyPath(t *testing.T) {
	m := Build(fixture(), Options{})
	if len(m.Rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(m.Rows))
	}
	if m.Rows[0].Status != StatusVerified {
		t.Errorf("status=%q, want verified; reason=%q", m.Rows[0].Status, m.Rows[0].StatusReason)
	}
	if len(m.Rows[0].Enforcers) != 1 || len(m.Rows[0].Verifiers) != 1 {
		t.Errorf("enforcers/verifiers not threaded: %+v", m.Rows[0])
	}
	if m.Summaries[0].VerificationRate != 1.0 {
		t.Errorf("rate=%v, want 1.0", m.Summaries[0].VerificationRate)
	}
}

func TestUnenforced(t *testing.T) {
	kg := fixture()
	kg.Symbols = nil // drop the enforcer
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusUnenforced {
		t.Errorf("status=%q, want unenforced", row.Status)
	}
}

func TestUnverified(t *testing.T) {
	kg := fixture()
	kg.Tests = nil // drop the verifier
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusUnverified {
		t.Errorf("status=%q, want unverified", row.Status)
	}
}

func TestPhantomFeature(t *testing.T) {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = schema.InvariantRefs{"nonexistent:INV-1"}
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusPhantom {
		t.Errorf("status=%q, want phantom (missing feature)", row.Status)
	}
}

func TestPhantomInvariant(t *testing.T) {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = schema.InvariantRefs{"checkout:INV-NOPE"}
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusPhantom {
		t.Errorf("status=%q, want phantom (missing invariant)", row.Status)
	}
}

func TestUnmapped(t *testing.T) {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = nil
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusUnmapped {
		t.Errorf("status=%q, want unmapped", row.Status)
	}
}

// A criterion mapped to several invariants holds only when all of them
// do: the worst one decides its status and is named in the reason, and
// the enforcers and verifiers are the union.
func TestCriterionMappedToSeveralInvariants(t *testing.T) {
	several := func() schema.KnowledgeGraph {
		kg := fixture()
		kg.Features[0].Invariants = append(kg.Features[0].Invariants,
			schema.Invariant{ID: "INV-2", Statement: "a refund is issued once"})
		kg.Symbols = append(kg.Symbols, schema.GraphSymbol{
			FQN: "Checkout\\Ledger", Feature: "checkout", EnforcesInvariants: []string{"INV-2"}})
		kg.Tests = append(kg.Tests, schema.GraphSymbol{
			FQN: "Tests\\Checkout\\LedgerTest", IsTest: true, Verifies: []string{"checkout:INV-2"}})
		kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = schema.InvariantRefs{"checkout:INV-1", "checkout:INV-2"}
		return kg
	}

	row := Build(several(), Options{}).Rows[0]
	if row.Status != StatusVerified || len(row.Enforcers) != 2 || len(row.Verifiers) != 2 {
		t.Errorf("both verified: status %q, enforcers %v, verifiers %v", row.Status, row.Enforcers, row.Verifiers)
	}
	if row.MapsTo != "checkout:INV-1, checkout:INV-2" || row.Invariant != "refund <= charge a refund is issued once" {
		t.Errorf("maps_to %q, invariant %q", row.MapsTo, row.Invariant)
	}

	kg := several()
	kg.Tests = kg.Tests[:1] // INV-2 loses its only test
	row = Build(kg, Options{}).Rows[0]
	if row.Status != StatusUnverified || row.Ref() != "checkout:INV-2" ||
		row.StatusReason != "checkout:INV-2: no test verifies INV-2" {
		t.Errorf("one unverified: status %q, ref %q, reason %q", row.Status, row.Ref(), row.StatusReason)
	}

	// Results: one red verifier fails the criterion, all green demonstrate it.
	results := map[string]bool{"Tests\\Checkout\\RefundTest": true, "Tests\\Checkout\\LedgerTest": false}
	resultOf := func(fqn string) (bool, bool) { p, ok := results[fqn]; return p, ok }
	if row = Build(several(), Options{ResultOf: resultOf}).Rows[0]; row.Status != StatusFailing || row.Ref() != "checkout:INV-2" {
		t.Errorf("one failing: status %q, ref %q", row.Status, row.Ref())
	}
	results["Tests\\Checkout\\LedgerTest"] = true
	if row = Build(several(), Options{ResultOf: resultOf}).Rows[0]; row.Status != StatusDemonstrated {
		t.Errorf("all passing: status %q, reason %q", row.Status, row.StatusReason)
	}

	kg = several()
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = append(kg.BRDs[0].SuccessCriteria[0].MapsToInvariant, "checkout:INV-9")
	if row = Build(kg, Options{}).Rows[0]; row.Status != StatusPhantom || row.Ref() != "checkout:INV-9" {
		t.Errorf("one phantom: status %q, ref %q", row.Status, row.Ref())
	}
}

func TestPartialBelowMutationThreshold(t *testing.T) {
	kg := fixture()
	kg.Features[0].MutationScores = map[string]float64{"INV-1": 0.6}
	row := Build(kg, Options{MutationThreshold: 0.8}).Rows[0]
	if row.Status != StatusPartial {
		t.Errorf("status=%q, want partial", row.Status)
	}
	if !row.HasMutation || row.MutationScore != 0.6 {
		t.Errorf("mutation not threaded: %+v", row)
	}
}

func TestVerifiedAboveMutationThreshold(t *testing.T) {
	kg := fixture()
	kg.Features[0].MutationScores = map[string]float64{"INV-1": 0.95}
	row := Build(kg, Options{MutationThreshold: 0.8}).Rows[0]
	if row.Status != StatusVerified {
		t.Errorf("status=%q, want verified", row.Status)
	}
}

func TestSummaryWorstStatusRollup(t *testing.T) {
	kg := fixture()
	// Add a second SC that's broken; summary worst-status should
	// surface the broken one, not the verified one.
	kg.BRDs[0].SuccessCriteria = append(kg.BRDs[0].SuccessCriteria, schema.BRDCriterion{
		ID: "SC-2", Statement: "x", MapsToInvariant: schema.InvariantRefs{"ghost:INV-1"},
	})
	m := Build(kg, Options{})
	if len(m.Summaries) != 1 || m.Summaries[0].WorstStatus != StatusPhantom {
		t.Errorf("worst=%+v, want phantom", m.Summaries[0])
	}
	if m.Summaries[0].Verified != 1 || m.Summaries[0].Phantom != 1 {
		t.Errorf("counts wrong: %+v", m.Summaries[0])
	}
}

func TestComputeCoverage(t *testing.T) {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria = append(kg.BRDs[0].SuccessCriteria,
		schema.BRDCriterion{ID: "SC-2", Statement: "x"}, // unmapped
		schema.BRDCriterion{ID: "SC-3", Statement: "x", // phantom
			MapsToInvariant: schema.InvariantRefs{"nope:INV-1"}},
	)
	c := ComputeCoverage(Build(kg, Options{}))
	if c.TotalCriteria != 3 || c.VerifiedCriteria != 1 {
		t.Errorf("counts wrong: %+v", c)
	}
	// 1/3 ≈ 0.3333
	if c.Ratio < 0.33 || c.Ratio > 0.34 {
		t.Errorf("ratio=%v, want ~0.333", c.Ratio)
	}
}

func TestStructuralOnlyInvariantNotUnverified(t *testing.T) {
	// An invariant whose verifiable_by is ["structural"] doesn't
	// need a test verifier — absence shouldn't flip to unverified.
	kg := fixture()
	kg.Features[0].Invariants[0].VerifiableBy = []schema.VerifiableBy{schema.VerifiableByStructural}
	kg.Tests = nil
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusVerified {
		t.Errorf("status=%q, want verified (structural-only invariant)", row.Status)
	}
}
