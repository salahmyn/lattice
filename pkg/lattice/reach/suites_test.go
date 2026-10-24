package reach_test

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func suiteGraph() schema.KnowledgeGraph {
	test := func(file, fqn string, verifies ...string) schema.GraphSymbol {
		return schema.GraphSymbol{FQN: fqn, File: file, IsTest: true, Verifies: verifies, Feature: "pay"}
	}
	return schema.KnowledgeGraph{
		Tests: []schema.GraphSymbol{
			test("e2e/checkout.spec.ts", "e2e.checkout.a", "pay:INV-1", "pay:INV-2"),
			test("e2e/checkout.spec.ts", "e2e.checkout.b", "INV-3"), // bare: the test's feature
			test("tests/e2e/flow.spec.ts", "tests.e2e.flow.a", "pay:INV-1", "pay:INV-2", "pay:INV-3"),
			test("tests/e2e/flow.spec.ts", "tests.e2e.flow.s", "brd.pay:US-1"),
			test("unit/x.test.ts", "unit.x.a", "pay:INV-9"),
		},
		EntryPoints: []schema.EntryPoint{{ID: "ep.http.checkout"}},
		BRDs: []schema.BRD{{ID: "brd.pay", UserScenarios: []schema.UserScenario{
			{ID: "US-1", VerifiedBy: []string{"tests.e2e.flow.s", "ep.http.checkout"}},
		}}},
	}
}

// SUITE_OVERLAP compares what suites verify: @verifies targets (a bare one
// qualified by the test's feature) and the entry points of the scenarios
// their tests verify. Default suites are the top-level test directories.
// @verifies reachability:INV-8
func TestSuiteOverlap(t *testing.T) {
	kg := suiteGraph()
	// Default suites: e2e and tests (unit/ is no test directory name).
	// e2e: {INV-1, INV-2, INV-3}; tests: {INV-1, INV-2, INV-3, US-1, ep}.
	// Jaccard 3/5 = 0.6.
	if v := reach.SuiteOverlap(kg, config.Coverage{}); len(v) != 0 {
		t.Errorf("0.6 is below the default 0.8: %+v", v)
	}
	v := reach.SuiteOverlap(kg, config.Coverage{Overlap: 0.6})
	if len(v) != 1 || v[0].Subject != "e2e~tests" || v[0].Code != schema.CodeSuiteOverlap ||
		!strings.Contains(v[0].Message, "Jaccard 0.60") || !strings.Contains(v[0].Message, "evidence: static") {
		t.Fatalf("overlap at 0.6: %+v", v)
	}

	// Configured suites: the flow suite without its scenario test overlaps
	// the checkout suite fully.
	cfg := config.Coverage{Suites: map[string][]string{
		"checkout": {"e2e/**"},
		"flow":     {"tests/e2e/flow.spec.ts"},
		"unit":     {"unit/**"},
	}}
	kg.Tests = kg.Tests[:3]
	kg.Tests = append(kg.Tests, schema.GraphSymbol{FQN: "unit.x.a", File: "unit/x.test.ts", IsTest: true, Verifies: []string{"pay:INV-9"}})
	v = reach.SuiteOverlap(kg, cfg)
	if len(v) != 1 || v[0].Subject != "checkout~flow" || !strings.Contains(v[0].Message, "Jaccard 1.00") {
		t.Errorf("configured suites: %+v", v)
	}
}
