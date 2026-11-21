package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// failingGraph has a fees feature with four invariants: INV-1 that no BRD
// maps, INV-2 that criterion SC-1 maps, INV-3 whose verifier is tagged on
// case a, and INV-4 that SC-2 maps but nothing enforces.
func failingGraph() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		GeneratedFromCommit: "head",
		BRDs: []schema.BRD{{
			ID: "brd.fees", Title: "Fees", Status: schema.BRDApproved, Version: 1, BusinessProblem: "p",
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "a fee is capped", MapsToInvariant: schema.InvariantRefs{"fees:INV-2"}},
				{ID: "SC-2", Statement: "a fee is logged", MapsToInvariant: schema.InvariantRefs{"fees:INV-4"}},
			},
		}},
		Features: []schema.Manifest{{
			ID: "fees", Version: 1, Status: schema.StatusProduction, Purpose: "fees", SourcePath: "lattice/features/fees.yaml",
			Owners:        schema.Owners{Business: "b", Engineering: "e"},
			ImplementsBRD: "brd.fees",
			Invariants: []schema.Invariant{
				{ID: "INV-1", Statement: "a fee is rounded to the cent"},
				{ID: "INV-2", Statement: "a fee never exceeds five percent"},
				{ID: "INV-3", Statement: "a fee is never negative",
					Cases: []schema.InvariantCase{{ID: "a", Kind: "edge", Statement: "a zero amount has a zero fee"}}},
				{ID: "INV-4", Statement: "a fee is logged"},
			},
		}},
		Symbols: []schema.GraphSymbol{
			{FQN: "Fees.round", Kind: "function", Feature: "fees", EnforcesInvariants: []string{"INV-1"}},
			{FQN: "Fees.cap", Kind: "function", Feature: "fees", EnforcesInvariants: []string{"INV-2"}},
			{FQN: "Fees.floor", Kind: "function", Feature: "fees", EnforcesInvariants: []string{"INV-3"}},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "FeesTest.rounds", IsTest: true, Feature: "fees", Verifies: []string{"fees:INV-1"}},
			{FQN: "FeesTest.caps", IsTest: true, Feature: "fees", Verifies: []string{"fees:INV-2"}},
			{FQN: "FeesTest.zero", IsTest: true, Feature: "fees", Verifies: []string{"fees:INV-3"},
				VerifiesCases: []string{"fees:INV-3#a"}},
			{FQN: "FeesTest.logs", IsTest: true, Feature: "fees", Verifies: []string{"fees:INV-4"}},
		},
	}
}

// redFor reports the named tests as failed and every other test as passed.
func redFor(names ...string) func(string) (bool, bool) {
	red := map[string]bool{}
	for _, n := range names {
		red[n] = true
	}
	return func(fqn string) (bool, bool) { return !red[fqn], true }
}

// failingFindings returns the VERIFIER_FAILING findings by subject.
func failingFindings(v []schema.Violation) map[string]schema.Violation {
	out := map[string]schema.Violation{}
	for _, x := range v {
		if x.Code == schema.CodeVerifierFailing {
			out[x.Subject] = x
		}
	}
	return out
}

// @verifies validation:INV-16
func TestVerifierFailingUnmappedInvariant(t *testing.T) {
	v := Validate(failingGraph(), config.Default(), Options{ResultOf: redFor("FeesTest.rounds"), ResultsCommit: "head"})
	got := failingFindings(v)
	f, ok := got["fees:INV-1"]
	if !ok {
		t.Fatalf("an unmapped invariant with a red verifier must raise VERIFIER_FAILING, got %v", got)
	}
	if f.Severity != schema.SeverityWarning || f.FeatureID != "fees" ||
		f.Location == nil || f.Location.File != "lattice/features/fees.yaml" {
		t.Errorf("finding = %+v", f)
	}
	if !strings.Contains(f.Message, "fees:INV-1") || !strings.Contains(f.Message, "FeesTest.rounds") {
		t.Errorf("the message must name the invariant and the test: %q", f.Message)
	}
	if f.NextAction == nil || f.NextAction.Kind != "fix_test" || f.NextAction.Ref != "fees:INV-1" {
		t.Errorf("next action = %+v", f.NextAction)
	}
	if len(got) != 1 {
		t.Errorf("only INV-1 is red, got %v", got)
	}
}

// @verifies validation:INV-16
func TestVerifierFailingNotOnPass(t *testing.T) {
	v := Validate(failingGraph(), config.Default(), Options{ResultOf: redFor(), ResultsCommit: "head"})
	if got := failingFindings(v); len(got) != 0 {
		t.Errorf("all verifiers pass: no VERIFIER_FAILING, got %v", got)
	}
}

// Results for another commit are stale: they raise nothing. A set with no
// stamp, or a graph outside git, is read as current, as the audit does.
// @verifies validation:INV-16
func TestVerifierFailingNotOnStaleResults(t *testing.T) {
	stale := Validate(failingGraph(), config.Default(), Options{ResultOf: redFor("FeesTest.rounds"), ResultsCommit: "older"})
	if _, ok := failingFindings(stale)["fees:INV-1"]; ok {
		t.Errorf("results for another commit must not raise VERIFIER_FAILING")
	}
	unstamped := Validate(failingGraph(), config.Default(), Options{ResultOf: redFor("FeesTest.rounds")})
	if _, ok := failingFindings(unstamped)["fees:INV-1"]; !ok {
		t.Errorf("an unstamped set is current, as the audit reads it")
	}
	kg := failingGraph()
	kg.GeneratedFromCommit = ""
	nogit := Validate(kg, config.Default(), Options{ResultOf: redFor("FeesTest.rounds"), ResultsCommit: "older"})
	if _, ok := failingFindings(nogit)["fees:INV-1"]; !ok {
		t.Errorf("a graph with no commit reads the results as current")
	}
}

// @verifies validation:INV-16
func TestVerifierFailingCaseVerifier(t *testing.T) {
	v := Validate(failingGraph(), config.Default(), Options{ResultOf: redFor("FeesTest.zero"), ResultsCommit: "head"})
	f, ok := failingFindings(v)["fees:INV-3"]
	if !ok || !strings.Contains(f.Message, "FeesTest.zero") {
		t.Errorf("a red case verifier fails its invariant, got %v", failingFindings(v))
	}
}

// A criterion that reports the invariant names its tests; the per-invariant
// finding is not raised beside it. A mapped invariant whose criterion does
// not read failing (nothing enforces it) still gets its own finding.
// @verifies validation:INV-16
func TestVerifierFailingNoDoubleReport(t *testing.T) {
	v := Validate(failingGraph(), config.Default(), Options{ResultOf: redFor("FeesTest.caps", "FeesTest.logs"), ResultsCommit: "head"})
	got := failingFindings(v)
	if _, ok := got["fees:INV-2"]; ok {
		t.Errorf("fees:INV-2 is reported by brd.fees:SC-1; no second finding, got %v", got)
	}
	sc, ok := got["brd.fees:SC-1"]
	if !ok || !strings.Contains(sc.Message, "fees:INV-2: FeesTest.caps") {
		t.Errorf("the criterion finding must name the red test, got %v", got)
	}
	if _, ok := got["fees:INV-4"]; !ok {
		t.Errorf("SC-2 reads unenforced, not failing: fees:INV-4 needs its own finding, got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("want one finding per red invariant, got %v", got)
	}
}

// @verifies validation:INV-16
func TestVerifierFailingNamesAFew(t *testing.T) {
	kg := failingGraph()
	names := []string{"FeesTest.rounds"}
	for _, n := range []string{"a", "b", "c", "d"} {
		fqn := "FeesTest.rounds_" + n
		names = append(names, fqn)
		kg.Tests = append(kg.Tests, schema.GraphSymbol{FQN: fqn, IsTest: true, Feature: "fees", Verifies: []string{"INV-1"}})
	}
	v := Validate(kg, config.Default(), Options{ResultOf: redFor(names...), ResultsCommit: "head"})
	f := failingFindings(v)["fees:INV-1"]
	if !strings.Contains(f.Message, "FeesTest.rounds, FeesTest.rounds_a, FeesTest.rounds_b and 2 more") {
		t.Errorf("message = %q", f.Message)
	}
}
