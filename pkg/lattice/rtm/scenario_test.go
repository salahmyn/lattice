package rtm

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// scenarioFixture builds a graph whose BRD carries one user_scenario and
// one declared entry point, plus a test tagged @verifies brd.x:US-1.
func scenarioFixture() schema.KnowledgeGraph {
	kg := fixture()
	kg.BRDs[0].UserScenarios = []schema.UserScenario{{
		ID: "US-1", Actor: "Solo user",
		Narrative:  "adds three, lists, sees all three in order",
		VerifiedBy: []string{"ep.cli.todo", "Tests\\JourneyUS1"},
	}}
	kg.EntryPoints = []schema.EntryPoint{{
		ID: "ep.cli.todo", Kind: schema.EntryPointKindCLI,
		Flow: []schema.FlowStep{{Feature: "checkout"}},
	}}
	kg.Tests = append(kg.Tests, schema.GraphSymbol{
		FQN: "Tests\\JourneyUS1", IsTest: true, Verifies: []string{"brd.checkout:US-1"},
	})
	return kg
}

func TestScenarioDeclaredWithoutResults(t *testing.T) {
	m := Build(scenarioFixture(), Options{})
	if len(m.Scenarios) != 1 {
		t.Fatalf("scenarios=%d, want 1", len(m.Scenarios))
	}
	s := m.Scenarios[0]
	if s.Status != StatusVerified {
		t.Errorf("status=%q, want verified (declared, no results); reason=%q", s.Status, s.StatusReason)
	}
	if !s.TouchesEntryPoint {
		t.Error("expected scenario to touch the declared entry point")
	}
}

func TestScenarioDemonstratedWithGreenResult(t *testing.T) {
	m := Build(scenarioFixture(), Options{
		ResultOf: func(fqn string) (bool, bool) {
			if fqn == "Tests\\JourneyUS1" {
				return true, true
			}
			return false, false
		},
	})
	if got := m.Scenarios[0].Status; got != StatusDemonstrated {
		t.Errorf("status=%q, want demonstrated", got)
	}
	jc := ComputeJourneyCoverage(m)
	if jc.TotalScenarios != 1 || jc.ReachedScenarios != 1 || jc.Demonstrated != 1 {
		t.Errorf("journey coverage = %+v, want 1/1 reached, 1 demonstrated", jc)
	}
}

func TestScenarioFailingWithRedResult(t *testing.T) {
	m := Build(scenarioFixture(), Options{
		ResultOf: func(fqn string) (bool, bool) { return false, true },
	})
	if got := m.Scenarios[0].Status; got != StatusFailing {
		t.Errorf("status=%q, want failing", got)
	}
}

func TestScenarioUnmappedAndJourneyZero(t *testing.T) {
	kg := scenarioFixture()
	kg.BRDs[0].UserScenarios[0].VerifiedBy = nil // declines to claim a verifier
	kg.Tests = kg.Tests[:len(kg.Tests)-1]        // and drop the reverse-tagged test
	m := Build(kg, Options{})
	if got := m.Scenarios[0].Status; got != StatusUnmapped {
		t.Errorf("status=%q, want unmapped", got)
	}
	if jc := ComputeJourneyCoverage(m); jc.ReachedScenarios != 0 {
		t.Errorf("journey reached=%d, want 0", jc.ReachedScenarios)
	}
}

func TestCriterionDemonstratedFoldsResults(t *testing.T) {
	// The SC-1 verifier passing on the commit upgrades verified→demonstrated.
	m := Build(fixture(), Options{
		ResultOf: func(fqn string) (bool, bool) {
			if fqn == "Tests\\Checkout\\RefundTest" {
				return true, true
			}
			return false, false
		},
	})
	if got := m.Rows[0].Status; got != StatusDemonstrated {
		t.Errorf("criterion status=%q, want demonstrated", got)
	}
	// A red result downgrades to failing.
	m = Build(fixture(), Options{ResultOf: func(string) (bool, bool) { return false, true }})
	if got := m.Rows[0].Status; got != StatusFailing {
		t.Errorf("criterion status=%q, want failing", got)
	}
}
