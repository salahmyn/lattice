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
					MapsToInvariant: "checkout:INV-1"},
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
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = "nonexistent:INV-1"
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusPhantom {
		t.Errorf("status=%q, want phantom (missing feature)", row.Status)
	}
}

func TestPhantomInvariant(t *testing.T) {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = "checkout:INV-NOPE"
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusPhantom {
		t.Errorf("status=%q, want phantom (missing invariant)", row.Status)
	}
}

func TestUnmapped(t *testing.T) {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = ""
	row := Build(kg, Options{}).Rows[0]
	if row.Status != StatusUnmapped {
		t.Errorf("status=%q, want unmapped", row.Status)
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
		ID: "SC-2", Statement: "x", MapsToInvariant: "ghost:INV-1",
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
		schema.BRDCriterion{ID: "SC-2", Statement: "x"},     // unmapped
		schema.BRDCriterion{ID: "SC-3", Statement: "x",      // phantom
			MapsToInvariant: "nope:INV-1"},
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
