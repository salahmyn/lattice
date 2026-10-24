package coverage

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// findingGraph: f:INV-1 enforced by E1 and verified by T1 and T2; f:INV-2
// enforced by E2 and verified by T3. The sets below are hand-built: they
// state exactly the evidence each case needs.
func findingGraph() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		GeneratedFromCommit: "c1",
		Features: []schema.Manifest{{ID: "f", SourcePath: "lattice/features/f.yaml",
			Invariants: []schema.Invariant{{ID: "INV-1"}, {ID: "INV-2"}}}},
		Symbols: []schema.GraphSymbol{
			{FQN: "E1", Kind: "function", File: "a.js", Line: 1, EndLine: 5, Feature: "f", EnforcesInvariants: []string{"INV-1"}},
			{FQN: "E2", Kind: "function", File: "a.js", Line: 10, EndLine: 12, Feature: "f", EnforcesInvariants: []string{"INV-2"}},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "T1", IsTest: true, File: "t.js", Verifies: []string{"f:INV-1"}},
			{FQN: "T2", IsTest: true, File: "t.js", Verifies: []string{"f:INV-1"}},
			{FQN: "T3", IsTest: true, File: "u.js", Verifies: []string{"f:INV-2"}},
		},
	}
}

func codesOf(v []schema.Violation, code string) []string {
	var out []string
	for _, f := range v {
		if f.Code == code {
			out = append(out, f.Subject)
		}
	}
	return out
}

func messageOf(v []schema.Violation, code, subject string) string {
	for _, f := range v {
		if f.Code == code && f.Subject == subject {
			return f.Message
		}
	}
	return ""
}

// VERIFIER_MISSES_ENFORCER needs evidence for every verifier; one verifier
// without evidence makes it unknown (VERIFIER_UNPROVEN names it), never a pass.
// @verifies coverage:INV-5, coverage:INV-8
func TestVerifierMissesEnforcer(t *testing.T) {
	kg := findingGraph()
	run := func(set Set) []schema.Violation { return Findings(kg, NewIndex(set, kg), config.Coverage{}) }
	commit := func(s Set) Set { s.Commit = "c1"; return s }

	// Fires: both verifiers of INV-1 have per-test data, neither ran E1.
	v := run(commit(Set{PerTest: map[string][]string{"T1": {"other"}, "T2": {}, "T3": {"E2"}}}))
	if got := codesOf(v, schema.CodeVerifierMissesEnforcer); strings.Join(got, ",") != "f:INV-1" {
		t.Fatalf("fires: %v", got)
	}
	if m := messageOf(v, schema.CodeVerifierMissesEnforcer, "f:INV-1"); !strings.Contains(m, "evidence: per_test") {
		t.Errorf("the finding states its basis: %q", m)
	}
	if len(codesOf(v, schema.CodeVerifierUnproven)) != 0 {
		t.Errorf("every verifier has per-test data: no VERIFIER_UNPROVEN, got %v", codesOf(v, schema.CodeVerifierUnproven))
	}

	// Does not fire: one verifier ran the enforcer.
	v = run(commit(Set{PerTest: map[string][]string{"T1": {}, "T2": {"E1"}, "T3": {"E2"}}}))
	if got := codesOf(v, schema.CodeVerifierMissesEnforcer); len(got) != 0 {
		t.Errorf("T2 ran E1: %v", got)
	}

	// Unknown: T2 has no per-test data and the suite ran E1, so the miss
	// cannot be claimed. VERIFIER_UNPROVEN names T2.
	v = run(commit(Set{PerTest: map[string][]string{"T1": {}, "T3": {"E2"}},
		Symbols: map[string]SymbolCov{"E1": {Lines: 4, Hit: 2, Executed: true}}}))
	if got := codesOf(v, schema.CodeVerifierMissesEnforcer); len(got) != 0 {
		t.Errorf("T2 has no evidence: no miss can be claimed, got %v", got)
	}
	if m := messageOf(v, schema.CodeVerifierUnproven, "f:INV-1"); !strings.Contains(m, "T2") || !strings.Contains(m, "evidence: suite") {
		t.Errorf("VERIFIER_UNPROVEN should name T2 and its basis: %q", m)
	}

	// Suite basis: no test in the run executed E1 at all, so no verifier did.
	v = run(commit(Set{Symbols: map[string]SymbolCov{"E1": {Lines: 4}, "E2": {Lines: 2, Hit: 2, Executed: true}}}))
	if m := messageOf(v, schema.CodeVerifierMissesEnforcer, "f:INV-1"); !strings.Contains(m, "evidence: suite") {
		t.Errorf("suite basis: %q", m)
	}
	if len(codesOf(v, schema.CodeVerifierUnproven)) != 0 {
		t.Error("no per-test coverage was ingested: VERIFIER_UNPROVEN has no data to rest on")
	}

	// Per-file basis: a run of the verifiers' test file never ran E1.
	v = run(commit(Set{PerFile: map[string][]string{"t.js": {"other"}}}))
	if m := messageOf(v, schema.CodeVerifierMissesEnforcer, "f:INV-1"); !strings.Contains(m, "evidence: per_file") {
		t.Errorf("per-file basis: %q", m)
	}

	// No data: no set, no finding.
	if v := Findings(kg, nil, config.Coverage{Floors: config.CoverageFloors{Feature: 90, Invariant: 90}}); len(v) != 0 {
		t.Errorf("no coverage ingested: %v", v)
	}
	// An empty set has no evidence for anyone: nothing fires but staleness.
	if v := run(commit(Set{})); len(v) != 0 {
		t.Errorf("empty set: %v", subjects(v))
	}
}

// @verifies coverage:INV-6
func TestCoverageStale(t *testing.T) {
	kg := findingGraph()
	for _, tc := range []struct {
		set  Set
		want string
	}{
		{Set{Commit: "c1"}, ""},
		{Set{Commit: "c0"}, "covers commit c0, the graph is at c1"},
		{Set{Commit: "c1", Dirty: true, DirtyFiles: []string{"a.js"}}, "uncommitted changes when it was ingested (a.js)"},
		{Set{}, "covers commit (none)"},
	} {
		v := Findings(kg, NewIndex(tc.set, kg), config.Coverage{})
		m := messageOf(v, schema.CodeCoverageStale, "coverage")
		if (tc.want == "") != (m == "") || !strings.Contains(m, tc.want) {
			t.Errorf("%+v: COVERAGE_STALE %q, want %q", tc.set, m, tc.want)
		}
	}
	// Outside git both commits are empty: not stale.
	kg.GeneratedFromCommit = ""
	if v := Findings(kg, NewIndex(Set{}, kg), config.Coverage{}); len(codesOf(v, schema.CodeCoverageStale)) != 0 {
		t.Error("no commit on either side is not stale")
	}
}

// Floors fire only when declared; the invariant floor is measured over the
// verifiers' own runs when each has per-test data, else over the suite.
// @verifies coverage:INV-7
func TestCoverageBelowFloor(t *testing.T) {
	kg := findingGraph()
	kg.BRDs = []schema.BRD{{ID: "brd.x", SourcePath: "lattice/brds/brd.x.yaml", SuccessCriteria: []schema.BRDCriterion{
		{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"f:INV-1"}, Tier: 3},
		{ID: "SC-2", MapsToInvariant: schema.InvariantRefs{"f:INV-2"}},
	}}}
	suite := map[string]SymbolCov{"E1": {Lines: 4, Hit: 4, Executed: true}, "E2": {Lines: 2, Hit: 1, Executed: true}}
	floors := config.CoverageFloors{Feature: 80, Invariant: 80, Tier: map[int]float64{3: 100}}

	// Suite only: E1 is fully run by the suite; E2 half; f has 5/6.
	set := Set{Commit: "c1", Symbols: suite}
	v := Findings(kg, NewIndex(set, kg), config.Coverage{Floors: floors})
	if got := strings.Join(codesOf(v, schema.CodeCoverageBelowFloor), ","); got != "f:INV-2" {
		t.Fatalf("suite basis: %s", got)
	}
	if m := messageOf(v, schema.CodeCoverageBelowFloor, "f:INV-2"); !strings.Contains(m, "the whole suite") || !strings.Contains(m, "evidence: suite") {
		t.Errorf("says which basis: %q", m)
	}

	// Per-test: T1 and T2 ran only line 2 of E1's four, so INV-1 is at 25%
	// over its verifiers, though the suite ran all of it; tier 3 floors SC-1.
	set.PerTest = map[string][]string{"T1": {"E1"}, "T2": {}, "T3": {"E2"}}
	set.EnforcerLines = map[string]map[string]LineSet{
		"T1": {"E1": {Body: []int{2, 3, 4, 5}, Hit: []int{2}}},
		"T2": {"E1": {Body: []int{2, 3, 4, 5}}},
		"T3": {"E2": {Body: []int{11, 12}, Hit: []int{11, 12}}},
	}
	v = Findings(kg, NewIndex(set, kg), config.Coverage{Floors: floors})
	if got := strings.Join(codesOf(v, schema.CodeCoverageBelowFloor), ","); got != "f:INV-1,brd.x:SC-1" {
		t.Fatalf("per-test basis: %s", got)
	}
	if m := messageOf(v, schema.CodeCoverageBelowFloor, "f:INV-1"); !strings.Contains(m, "25.0%") || !strings.Contains(m, "evidence: per_test") {
		t.Errorf("INV-1 over its verifiers: %q", m)
	}

	// Feature floor: the suite ran 5 of the feature's 6 body lines (83.3%).
	floors.Feature = 90
	v = Findings(kg, NewIndex(set, kg), config.Coverage{Floors: floors})
	if m := messageOf(v, schema.CodeCoverageBelowFloor, "f"); !strings.Contains(m, "83.3%") || !strings.Contains(m, "evidence: suite") {
		t.Errorf("feature floor: %q", m)
	}

	// Not declared: no floor finding at all.
	if got := codesOf(Findings(kg, NewIndex(set, kg), config.Coverage{}), schema.CodeCoverageBelowFloor); len(got) != 0 {
		t.Errorf("no floors declared: %v", got)
	}
}
