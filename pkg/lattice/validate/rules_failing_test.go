package validate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
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

// staleFindings returns the RESULTS_STALE findings.
func staleFindings(v []schema.Violation) []schema.Violation {
	var out []schema.Violation
	for _, x := range v {
		if x.Code == schema.CodeResultsStale {
			out = append(out, x)
		}
	}
	return out
}

// Results ingested at another commit than the graph's are named once, as
// info, with both short commits and what they still drive.
// @verifies validation:INV-17
func TestResultsStale(t *testing.T) {
	kg := failingGraph()
	kg.GeneratedFromCommit = "bbbbbbbbbb222222"
	v := Validate(kg, config.Default(), Options{ResultOf: redFor(), ResultsCommit: "aaaaaaaaaa111111"})
	got := staleFindings(v)
	if len(got) != 1 {
		t.Fatalf("want exactly one RESULTS_STALE, got %v", got)
	}
	f := got[0]
	if f.Severity != schema.SeverityInfo || f.Subject != "results" {
		t.Errorf("finding = %+v, want info on subject results", f)
	}
	for _, want := range []string{"aaaaaaaa", "bbbbbbbb", "DEMONSTRATED", "VERIFIER_FAILING", "audit"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q does not name %q", f.Message, want)
		}
	}
	if strings.Contains(f.Message, "aaaaaaaaaa111111") {
		t.Errorf("message %q should name the short commit", f.Message)
	}
	if f.Location == nil || f.Location.File != results.CacheFile {
		t.Errorf("location = %+v, want %s", f.Location, results.CacheFile)
	}
	if f.NextAction == nil || f.NextAction.Kind != "run_command" ||
		strings.Join(f.NextAction.Command, " ") != "lattice results ingest" ||
		f.NextAction.Detail != "rerun the tests at this commit and ingest their reports" {
		t.Errorf("next action = %+v", f.NextAction)
	}
}

// Results of the graph's commit, a set or a graph with no commit, and no
// results at all raise nothing.
// @verifies validation:INV-17
func TestResultsStaleNotWhenCurrentOrUnknown(t *testing.T) {
	nogit := failingGraph()
	nogit.GeneratedFromCommit = ""
	for name, tc := range map[string]struct {
		kg   schema.KnowledgeGraph
		opts Options
	}{
		"same commit":     {failingGraph(), Options{ResultOf: redFor(), ResultsCommit: "head"}},
		"unstamped set":   {failingGraph(), Options{ResultOf: redFor()}},
		"graph no commit": {nogit, Options{ResultOf: redFor(), ResultsCommit: "older"}},
		"no results":      {failingGraph(), Options{ResultsCommit: "older"}},
	} {
		if got := staleFindings(Validate(tc.kg, config.Default(), tc.opts)); len(got) != 0 {
			t.Errorf("%s: want no RESULTS_STALE, got %v", name, got)
		}
	}
}

// validate.severity raises it like any other code, and fail_on treats it
// as the severity it ends up with.
// @verifies validation:INV-17
func TestResultsStaleSeverity(t *testing.T) {
	opts := Options{ResultOf: redFor(), ResultsCommit: "older"}
	def := staleFindings(Validate(failingGraph(), config.Default(), opts))
	if len(def) != 1 || Fails(def[0], schema.SeverityError) || Fails(def[0], schema.SeverityWarning) ||
		!Fails(def[0], schema.SeverityInfo) {
		t.Errorf("default: an info finding fails only fail_on: info, got %v", def)
	}
	cfg := config.Default()
	cfg.Validate.Severity = map[string]string{schema.CodeResultsStale: "warning"}
	got := staleFindings(Validate(failingGraph(), cfg, opts))
	if len(got) != 1 || got[0].Severity != schema.SeverityWarning || !Fails(got[0], schema.SeverityWarning) ||
		Fails(got[0], schema.SeverityError) {
		t.Errorf("validate.severity RESULTS_STALE: warning = %v", got)
	}
	cfg.Validate.Severity = map[string]string{schema.CodeResultsStale: "off"}
	if got := staleFindings(Validate(failingGraph(), cfg, opts)); len(got) != 0 {
		t.Errorf("validate.severity RESULTS_STALE: off = %v", got)
	}
}

// Naming stale results changes nothing else: a criterion still reads
// demonstrated from them, require_level judges them as before, and every
// other finding is the same as with current results.
// @verifies validation:INV-17
func TestResultsStaleKeepsLevels(t *testing.T) {
	kg := levelGraph()
	kg.GeneratedFromCommit = "head"
	pass := func(fqn string) (bool, bool) { return true, fqn == "T1" }
	cfg := config.Default()
	cfg.Validate.RequireLevel = config.RequireLevel{Default: "demonstrated"}

	current := Validate(kg, cfg, Options{ResultOf: pass, ResultsCommit: "head"})
	stale := Validate(kg, cfg, Options{ResultOf: pass, ResultsCommit: "older"})
	if len(staleFindings(stale)) != 1 {
		t.Fatalf("want one RESULTS_STALE, got %v", staleFindings(stale))
	}
	var rest []schema.Violation
	for _, f := range stale {
		if f.Code != schema.CodeResultsStale {
			rest = append(rest, f)
		}
	}
	if !reflect.DeepEqual(rest, current) {
		t.Errorf("stale results changed other findings:\n got %v\nwant %v", rest, current)
	}
	if got := belowLevel(stale); got != "brd.x:SC-2 brd.x:SC-3" {
		t.Errorf("BRD_BELOW_LEVEL = %q, want SC-1 and US-1 demonstrated from the stale results", got)
	}
	c := newCorpus(kg, cfg, Options{ResultOf: pass, ResultsCommit: "older"})
	for _, row := range rtm.Build(kg, c.rtmOptions()).Rows {
		if row.Ref() == "brd.x:SC-1" && row.Status != rtm.StatusDemonstrated {
			t.Errorf("brd.x:SC-1 = %s, want demonstrated", row.Status)
		}
	}
}

// Results of another commit that the caller found to have the graph's tree,
// apart from Lattice's own ledger and cache, are current: no RESULTS_STALE,
// and VERIFIER_FAILING reads them and names the commit they are of.
// @verifies validation:INV-16
// @verifies validation:INV-17
func TestResultsEquivalentAreCurrent(t *testing.T) {
	kg := failingGraph()
	kg.GeneratedFromCommit = "bbbbbbbbbb222222"
	v := Validate(kg, config.Default(), Options{ResultOf: redFor("FeesTest.rounds"),
		ResultsCommit: "aaaaaaaaaa111111", ResultsEquivalent: true})
	if got := staleFindings(v); len(got) != 0 {
		t.Errorf("equivalent results are current: want no RESULTS_STALE, got %v", got)
	}
	f, ok := failingFindings(v)["fees:INV-1"]
	if !ok {
		t.Fatalf("equivalent results must raise VERIFIER_FAILING, got %v", failingFindings(v))
	}
	if !strings.Contains(f.Message, "evidence: ingested results of commit aaaaaaaa, whose tree matches the graph's apart from Lattice's ledger and cache") {
		t.Errorf("message = %q", f.Message)
	}
	same := Validate(failingGraph(), config.Default(), Options{ResultOf: redFor("FeesTest.rounds"), ResultsCommit: "head"})
	if f := failingFindings(same)["fees:INV-1"]; !strings.HasSuffix(f.Message, "(evidence: ingested results)") {
		t.Errorf("results of the graph's own commit keep the plain evidence: %q", f.Message)
	}
}
