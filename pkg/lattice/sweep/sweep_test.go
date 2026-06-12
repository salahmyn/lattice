package sweep

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestInventoryAndDisappeared(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Tests: []schema.GraphSymbol{
			{FQN: "tests.a", Verifies: []string{"f:INV-1"}},
			{FQN: "tests.b"}, // not a verifier
			{FQN: "tests.journey"},
		},
		BRDs: []schema.BRD{{
			ID: "brd.x",
			UserScenarios: []schema.UserScenario{
				{ID: "US-1", VerifiedBy: []string{"tests.journey", "ep.http.x"}},
			},
		}},
	}
	inv := Inventory(kg)
	if len(inv) != 2 || inv[0] != "tests.a" || inv[1] != "tests.journey" {
		t.Fatalf("inventory=%v", inv)
	}

	gone := Disappeared(Baseline{Verifiers: []string{"tests.a", "tests.deleted"}}, inv)
	if len(gone) != 1 || gone[0] != "tests.deleted" {
		t.Fatalf("disappeared=%v", gone)
	}
}

func TestBaselineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Load(dir); ok {
		t.Fatal("expected no baseline yet")
	}
	if err := Save(dir, Baseline{Commit: "abc", Verifiers: []string{"z", "a"}}); err != nil {
		t.Fatal(err)
	}
	b, ok := Load(dir)
	if !ok || b.Commit != "abc" || len(b.Verifiers) != 2 || b.Verifiers[0] != "a" {
		t.Fatalf("loaded %+v ok=%v", b, ok)
	}
}
