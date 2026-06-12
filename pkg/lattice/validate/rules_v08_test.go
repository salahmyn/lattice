package validate

import (
	"testing"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func codeCounts(vs []schema.Violation) map[string]int {
	m := map[string]int{}
	for _, v := range vs {
		m[v.Code]++
	}
	return m
}

// v08Graph is a BRD with one criterion (verified) and one scenario that
// declares no verifier, plus a production feature reachable from a
// declared entry point.
func v08Graph() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.x", Title: "X", Status: schema.BRDApproved, Version: 1,
			BusinessProblem: "p",
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "refund never exceeds charge", MapsToInvariant: "pay:INV-1"},
			},
			UserScenarios: []schema.UserScenario{
				{ID: "US-1", Narrative: "user pays and sees a receipt"},
			},
		}},
		Features: []schema.Manifest{{
			ID: "pay", Version: 1, Status: schema.StatusProduction, Purpose: "pay",
			Owners:     schema.Owners{Business: "b", Engineering: "e"},
			Invariants: []schema.Invariant{{ID: "INV-1", Statement: "refund <= charge"}},
		}},
		Symbols: []schema.GraphSymbol{
			{FQN: "Pay.refund", Kind: "method", Feature: "pay", EnforcesInvariants: []string{"INV-1"}},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "PayTest.refund", IsTest: true, Verifies: []string{"pay:INV-1"}},
		},
		EntryPoints: []schema.EntryPoint{{
			ID: "ep.http.pay", Kind: schema.EntryPointKindHTTP,
			Flow: []schema.FlowStep{{Feature: "pay"}},
		}},
	}
}

func TestScenarioUnmappedFires(t *testing.T) {
	v := Validate(v08Graph(), config.Default(), Options{})
	if codeCounts(v)[schema.CodeBRDScenarioUnmapped] != 1 {
		t.Errorf("expected one BRD_SCENARIO_UNMAPPED, got %v", codeCounts(v))
	}
}

func TestFeatureUnreachedFiresForOrphan(t *testing.T) {
	kg := v08Graph()
	kg.Features = append(kg.Features, schema.Manifest{
		ID: "orphan", Version: 1, Status: schema.StatusProduction, Purpose: "o",
		Owners: schema.Owners{Business: "b", Engineering: "e"},
	})
	v := Validate(kg, config.Default(), Options{})
	if codeCounts(v)[schema.CodeFeatureUnreached] != 1 {
		t.Errorf("expected FEATURE_UNREACHED for the orphan feature, got %v", codeCounts(v))
	}
}

func TestEnforcerNotGuardFiresForTagOnly(t *testing.T) {
	kg := v08Graph()
	// Re-tag the enforcer as a class — a tag, not a guard.
	kg.Symbols[0].Kind = "class"
	v := Validate(kg, config.Default(), Options{})
	if codeCounts(v)[schema.CodeEnforcerNotGuard] == 0 {
		t.Errorf("expected ENFORCER_NOT_GUARD for a class-kind enforcer, got %v", codeCounts(v))
	}
}

func TestVerifierFailingFromResults(t *testing.T) {
	v := Validate(v08Graph(), config.Default(), Options{
		ResultOf: func(fqn string) (bool, bool) { return false, true }, // all red
	})
	if codeCounts(v)[schema.CodeVerifierFailing] == 0 {
		t.Errorf("expected VERIFIER_FAILING with a red result, got %v", codeCounts(v))
	}
}

func TestLeaseScopeOverlapFires(t *testing.T) {
	leases := []lease.Lease{
		{Unit: "pay", Actor: "A", Scope: []string{"src/Core/"}, Expires: future()},
		{Unit: "list", Actor: "B", Scope: []string{"src/Core/Store.ts"}, Expires: future()},
	}
	v := Validate(v08Graph(), config.Default(), Options{Leases: leases})
	if codeCounts(v)[schema.CodeLeaseScopeOverlap] != 1 {
		t.Errorf("expected LEASE_SCOPE_OVERLAP, got %v", codeCounts(v))
	}
}

func TestScenarioVerifyTagIsNotOrphan(t *testing.T) {
	// A journey test tagged @verifies brd.x:US-1 must resolve to the BRD
	// scenario, not read as an orphan annotation (v0.8 α integration).
	kg := v08Graph()
	kg.Tests = append(kg.Tests, schema.GraphSymbol{
		FQN: "JourneyTest.us1", IsTest: true,
		Verifies: []string{"brd.x:US-1"},
	})
	v := Validate(kg, config.Default(), Options{})
	if codeCounts(v)[schema.CodeOrphanAnnotationCapability] != 0 ||
		codeCounts(v)[schema.CodeOrphanAnnotationInvariant] != 0 {
		t.Errorf("@verifies brd.x:US-1 must not be an orphan annotation, got %v", codeCounts(v))
	}
}

func TestNarrowingNoOpWithoutLLM(t *testing.T) {
	// δ entailment is a deterministic no-op unless agentic.llm.enabled.
	v := Validate(v08Graph(), config.Default(), Options{})
	if codeCounts(v)[schema.CodeCriterionInvariantNarrower] != 0 {
		t.Errorf("CRITERION_INVARIANT_NARROWER must not fire with LLM disabled, got %v", codeCounts(v))
	}
}

func future() string {
	return time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
}
