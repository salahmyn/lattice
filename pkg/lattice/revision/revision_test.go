package revision

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestSaveLoadNextID(t *testing.T) {
	dir := t.TempDir()
	revs, viol := LoadAll(dir)
	if len(revs) != 0 || len(viol) != 0 {
		t.Fatal("expected empty store")
	}
	if id := NextID(revs); id != "CR-1" {
		t.Fatalf("NextID=%s, want CR-1", id)
	}
	r := schema.Revision{
		ID: "CR-1", Status: schema.RevisionProposed,
		Targets: []string{"brd.x.y/SC-1"}, ProposedText: "new text",
	}
	if _, err := Save(dir, r); err != nil {
		t.Fatal(err)
	}
	revs, _ = LoadAll(dir)
	if len(revs) != 1 || revs[0].ID != "CR-1" {
		t.Fatalf("loaded %+v", revs)
	}
	if id := NextID(revs); id != "CR-2" {
		t.Fatalf("NextID=%s, want CR-2", id)
	}
}

func TestRetirementCovered(t *testing.T) {
	revs := []schema.Revision{
		{ID: "CR-1", Status: schema.RevisionApproved,
			RetirementItems: []string{"tests.checkout.test_hard_delete"}},
		{ID: "CR-2", Status: schema.RevisionRejected,
			RetirementItems: []string{"tests.other.test_x"}},
	}
	if _, ok := RetirementCovered(revs, "tests.checkout.test_hard_delete"); !ok {
		t.Fatal("expected coverage from approved CR-1")
	}
	if _, ok := RetirementCovered(revs, "tests.other.test_x"); ok {
		t.Fatal("rejected CR must not legalize a retirement")
	}
}

func TestComputeImpactWalksTheChain(t *testing.T) {
	m := rtm.Matrix{
		Rows: []rtm.Row{{
			BRDID: "brd.teams.core", CriterionID: "SC-1", Tier: 2,
			FeatureID: "teams", InvariantID: "INV-1",
			Enforcers: []string{"src.guards.assertRole"},
			Verifiers: []string{"tests.teams.role_matrix"},
		}},
		Scenarios: []rtm.ScenarioRow{{
			BRDID: "brd.teams.core", ScenarioID: "US-1",
			Verifiers: []string{"tests.teams.journey"},
		}},
	}
	kg := schema.KnowledgeGraph{EntryPoints: []schema.EntryPoint{{
		ID: "ep.http.invite", Flow: []schema.FlowStep{{Feature: "teams"}},
	}}}
	leases := []lease.Lease{{Unit: "teams", Actor: "agent:dev-2"}}

	imp := ComputeImpact(m, kg, leases, []string{"brd.teams.core/SC-1"})
	if imp.MaxTier != 2 {
		t.Fatalf("MaxTier=%d, want 2", imp.MaxTier)
	}
	if len(imp.AffectedInvariants) != 1 || imp.AffectedInvariants[0] != "teams:INV-1" {
		t.Fatalf("invariants=%v", imp.AffectedInvariants)
	}
	if len(imp.AffectedEntryPoints) != 1 || imp.AffectedEntryPoints[0] != "ep.http.invite" {
		t.Fatalf("entry points=%v", imp.AffectedEntryPoints)
	}
	if len(imp.InFlightConflicts) != 1 {
		t.Fatalf("conflicts=%v, want the active lease on teams", imp.InFlightConflicts)
	}
	if len(imp.AffectedScenarios) != 1 {
		t.Fatalf("scenarios=%v", imp.AffectedScenarios)
	}
}

func TestSpawnItems(t *testing.T) {
	imp := schema.RevisionImpact{
		AffectedTests:   []string{"tests.t1"},
		AffectedSymbols: []string{"src.s1"},
	}
	work, ret := SpawnItems(schema.RevisionWidening, imp, []string{"brd.x/SC-1"})
	if len(work) != 1 || len(ret) != 0 {
		t.Fatalf("widening: work=%v ret=%v", work, ret)
	}
	work, ret = SpawnItems(schema.RevisionNarrowing, imp, []string{"brd.x/SC-1"})
	if len(ret) != 2 || len(work) != 0 {
		t.Fatalf("narrowing: work=%v ret=%v", work, ret)
	}
}
