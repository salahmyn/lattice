package validate

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func codes(vs []schema.Violation) map[string]bool {
	m := map[string]bool{}
	for _, v := range vs {
		m[v.Code] = true
	}
	return m
}

func TestValidateCleanFeature(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{
			ID: "checkout.refund", Version: 1, Status: schema.StatusProduction,
			Purpose: "Refunds.", Owners: schema.Owners{Business: "b", Engineering: "e"},
			Invariants: []schema.Invariant{{ID: "INV-1", Statement: "never exceeds"}},
		}, {
			ID: "checkout", Version: 1, Status: schema.StatusProduction,
			Purpose: "Parent.", Owners: schema.Owners{Business: "b", Engineering: "e"},
		}},
		Symbols: []schema.GraphSymbol{{
			FQN: "m.f", Feature: "checkout.refund", EnforcesInvariants: []string{"INV-1"},
		}},
		Tests: []schema.GraphSymbol{{
			FQN: "t.t", IsTest: true, Verifies: []string{"checkout.refund:INV-1"},
		}},
	}
	got := codes(Validate(kg, config.Default()))
	for _, unwanted := range []string{
		schema.CodeUnverifiedInvariant, schema.CodeUnenforcedInvariant,
		schema.CodeSubfeatureParentMissing,
	} {
		if got[unwanted] {
			t.Errorf("unexpected violation %s on a clean feature", unwanted)
		}
	}
}

func TestValidateUnverifiedAndUnenforced(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{
			ID: "wallet", Version: 1, Status: schema.StatusProduction,
			Purpose: "Wallet.", Owners: schema.Owners{Business: "b", Engineering: "e"},
			Invariants: []schema.Invariant{{ID: "INV-1", Statement: "never negative"}},
		}},
	}
	got := codes(Validate(kg, config.Default()))
	if !got[schema.CodeUnenforcedInvariant] {
		t.Error("expected UNENFORCED_INVARIANT")
	}
	if !got[schema.CodeUnverifiedInvariant] {
		t.Error("expected UNVERIFIED_INVARIANT")
	}
}

func TestValidateDependencyCycle(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{
			{ID: "a", Version: 1, Status: schema.StatusProduction, Purpose: "A",
				Owners: schema.Owners{Business: "b", Engineering: "e"}, DependsOn: []string{"b"}},
			{ID: "b", Version: 1, Status: schema.StatusProduction, Purpose: "B",
				Owners: schema.Owners{Business: "b", Engineering: "e"}, DependsOn: []string{"a"}},
		},
	}
	if !codes(Validate(kg, config.Default()))[schema.CodeDependsOnCycle] {
		t.Error("expected DEPENDS_ON_CYCLE")
	}
}

func TestValidateBadIDFormat(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{
			ID: "Bad-ID", Version: 1, Status: schema.StatusProduction, Purpose: "X",
			Owners: schema.Owners{Business: "b", Engineering: "e"},
		}},
	}
	if !codes(Validate(kg, config.Default()))[schema.CodeManifestIDFormat] {
		t.Error("expected MANIFEST_ID_FORMAT")
	}
}
