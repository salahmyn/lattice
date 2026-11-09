package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// fakeCoverage is hand-built per-test coverage: per test, the symbols its own
// run executed. A test with no entry has no coverage.
type fakeCoverage map[string][]string

func (f fakeCoverage) TestRan(test, symbol string) (bool, string) {
	syms, ok := f[test]
	if !ok {
		return false, rtm.EvidenceNone
	}
	for _, s := range syms {
		if s == symbol {
			return true, rtm.EvidencePerTest
		}
	}
	return false, rtm.EvidencePerTest
}
func (f fakeCoverage) SuiteRan(string) (bool, bool) { return false, false }
func (f fakeCoverage) EnforcerLines(_, _ []string) (int, int, bool, bool) {
	return 0, 0, false, false
}
func (f fakeCoverage) HasPerTest() bool { return len(f) > 0 }

// graph builds one feature "f" with the given invariants. Each is enforced by
// symbol "f.EnfN" when enforcers lists it, and verified by tests "f.TestN"
// per the verifiers map.
func graph(invs []schema.Invariant, enforced []string, verifiers map[string][]string) schema.KnowledgeGraph {
	kg := schema.KnowledgeGraph{
		GeneratedFromCommit: "abc123",
		Features:            []schema.Manifest{{ID: "f", Version: 1, Status: schema.StatusProduction, Invariants: invs}},
	}
	for _, inv := range enforced {
		kg.Symbols = append(kg.Symbols, schema.GraphSymbol{
			FQN: "f.Enf" + inv, File: "f.go", Line: 3, Feature: "f",
			EnforcesInvariants: []string{inv}, Reach: schema.ReachEntry,
		})
	}
	for inv, tests := range verifiers {
		for _, t := range tests {
			kg.Tests = append(kg.Tests, schema.GraphSymbol{FQN: t, File: "f_test.go", Feature: "f", IsTest: true, Verifies: []string{inv}})
		}
	}
	return kg
}

func inv(ids ...string) []schema.Invariant {
	var out []schema.Invariant
	for _, id := range ids {
		out = append(out, schema.Invariant{ID: id, Statement: "statement " + id})
	}
	return out
}

func pass(tests ...string) results.Set {
	set := results.Set{Results: map[string]results.Outcome{}}
	for _, t := range tests {
		set.Results[strings.ToLower(t)] = results.Pass
	}
	return set
}

func rowOf(t *testing.T, rep Report, promise string) Row {
	t.Helper()
	for _, r := range rep.Rows {
		if r.Promise == promise {
			return r
		}
	}
	t.Fatalf("no row %s in %+v", promise, rep.Rows)
	return Row{}
}

func breaks(commit string, runs ...BreakRun) *BreaksFile {
	return &BreaksFile{Schema: BreaksSchema, Commit: commit, Runs: runs}
}

func run(promise, enforcer, outcome string) BreakRun {
	return BreakRun{Promise: promise, Enforcer: enforcer, Method: "early_return", Outcome: outcome, Baseline: "pass"}
}

// Every class is reachable, each from the evidence that defines it.
// @verifies audit:INV-1
func TestEveryClass(t *testing.T) {
	kg := graph(inv("INV-1", "INV-2", "INV-3", "INV-4", "INV-5", "INV-6"),
		[]string{"INV-1", "INV-2", "INV-3", "INV-5", "INV-6"},
		map[string][]string{"INV-2": {"f.TestTwo"}, "INV-3": {"f.TestThree"}, "INV-4": {"f.TestFour"}, "INV-5": {"f.TestFive"}, "INV-6": {"f.TestSix"}})
	// INV-1 enforced, no verifier. INV-2 passes, covered. INV-3 passes, no
	// coverage entry. INV-4 has a verifier but no enforcer. INV-5 has a
	// verifier with no result. INV-6 has a red verifier.
	set := pass("f.TestTwo", "f.TestThree", "f.TestFour")
	set.Results["f.testsix"] = results.Fail
	cov := fakeCoverage{"f.TestTwo": {"f.EnfINV-2"}, "f.TestSix": {"f.EnfINV-6"}}
	rep := Compute(Sources{KG: kg, Results: set, Coverage: cov})

	want := map[string][2]string{
		"f:INV-1": {ClassUnverified, ""},
		"f:INV-2": {ClassExercised, ""},
		"f:INV-3": {ClassDemonstratedOnly, KindPartialCoverage},
		"f:INV-4": {ClassUnenforced, ""},
		"f:INV-5": {ClassNotDemonstrated, KindNoResults},
		"f:INV-6": {ClassNotDemonstrated, KindFailing},
	}
	for p, w := range want {
		if r := rowOf(t, rep, p); r.Class != w[0] || r.Kind != w[1] {
			t.Errorf("%s = %s/%s, want %s/%s (%s)", p, r.Class, r.Kind, w[0], w[1], r.Reason)
		}
	}
	if rep.Summary.Total != 6 || rep.Summary.ByClass[ClassExercised] != 1 {
		t.Errorf("summary = %+v", rep.Summary)
	}
}

// Fake, then break_tested, then exercised, then demonstrated_only: the first
// that holds wins.
// @verifies audit:INV-1
func TestPrecedence(t *testing.T) {
	one := func(cov fakeCoverage, set results.Set, bf *BreaksFile) Row {
		kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
		return Compute(Sources{KG: kg, Results: set, Coverage: cov, Breaks: bf}).Rows[0]
	}
	ran := fakeCoverage{"f.TestOne": {"f.EnfINV-1"}}
	idle := fakeCoverage{"f.TestOne": {"f.Other"}}
	ok := pass("f.TestOne")
	enf := "f.EnfINV-1"

	cases := []struct {
		name  string
		row   Row
		class string
		kind  string
	}{
		{"never runs beats a kill", one(idle, ok, breaks("abc123", run("f:INV-1", enf, "killed"))), ClassFake, KindNeverRunsEnforcer},
		{"never runs beats exercised", one(idle, ok, nil), ClassFake, KindNeverRunsEnforcer},
		{"survived beats exercised", one(ran, ok, breaks("abc123", run("f:INV-1", enf, "survived"))), ClassFake, KindPassesWhenBroken},
		{"killed beats exercised", one(ran, ok, breaks("abc123", run("f:INV-1", enf, "killed"))), ClassBreakTested, ""},
		{"killed with no coverage", one(nil, ok, breaks("abc123", run("f:INV-1", enf, "killed"))), ClassBreakTested, ""},
		{"exercised", one(ran, ok, nil), ClassExercised, ""},
		{"unknown break leaves exercised", one(ran, ok, breaks("abc123", run("f:INV-1", enf, "unknown"))), ClassExercised, ""},
		{"demonstrated only", one(nil, ok, nil), ClassDemonstratedOnly, KindNoTool},
	}
	for _, c := range cases {
		if c.row.Class != c.class || c.row.Kind != c.kind {
			t.Errorf("%s: %s/%s, want %s/%s (%s)", c.name, c.row.Class, c.row.Kind, c.class, c.kind, c.row.Reason)
		}
	}
}

// With no per-test coverage and no break run, a promise is never fake: "no
// tool" stays apart from "fake".
// @verifies audit:INV-2
func TestNoToolIsNotFake(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	for name, src := range map[string]Sources{
		"no coverage":        {KG: kg, Results: pass("f.TestOne")},
		"no results either":  {KG: kg},
		"break unknown only": {KG: kg, Results: pass("f.TestOne"), Breaks: breaks("abc123", run("f:INV-1", "f.EnfINV-1", "unknown"))},
	} {
		r := Compute(src).Rows[0]
		if r.Class == ClassFake || Positive(r.Class) {
			t.Errorf("%s: class %s must be neither fake nor positive", name, r.Class)
		}
	}
	// Coverage for only one of two verifiers cannot say "never runs".
	kg2 := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne", "f.TestTwo"}})
	cov := fakeCoverage{"f.TestOne": {"f.Other"}}
	r := Compute(Sources{KG: kg2, Results: pass("f.TestOne", "f.TestTwo"), Coverage: cov}).Rows[0]
	if r.Class != ClassDemonstratedOnly || r.Kind != KindPartialCoverage || r.Signals.Exercised.Value != "unknown" {
		t.Errorf("partial coverage: %s/%s exercised=%s", r.Class, r.Kind, r.Signals.Exercised.Value)
	}
}

// breaks.json outcomes map to break_tested and passes_when_broken, a
// survived run on any enforcer wins over a killed one, and a stale file is
// set aside.
// @verifies audit:INV-3
func TestBreakOutcomes(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "f.Second", Feature: "f", EnforcesInvariants: []string{"INV-1"}})
	set := pass("f.TestOne")

	killed := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123", run("f:INV-1", "f.EnfINV-1", "killed"))}).Rows[0]
	if killed.Class != ClassBreakTested || killed.Signals.BreakTested.Value != "killed" || killed.Signals.BreakTested.Method != "early_return" {
		t.Errorf("killed: %s %+v", killed.Class, killed.Signals.BreakTested)
	}
	mixed := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123",
		run("f:INV-1", "f.EnfINV-1", "killed"), run("f:INV-1", "f.Second", "survived"))}).Rows[0]
	if mixed.Class != ClassFake || mixed.Kind != KindPassesWhenBroken || mixed.Signals.BreakTested.Items[0] != "f.Second" {
		t.Errorf("mixed: %s/%s %+v", mixed.Class, mixed.Kind, mixed.Signals.BreakTested)
	}
	unk := BreakRun{Promise: "f:INV-1", Enforcer: "f.EnfINV-1", Outcome: "unknown", Reason: "no runner configured"}
	u := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123", unk)}).Rows[0]
	if u.Signals.BreakTested.Value != "unknown" || !strings.Contains(u.Signals.BreakTested.Detail, "no runner configured") {
		t.Errorf("unknown: %+v", u.Signals.BreakTested)
	}
	stale := Compute(Sources{KG: kg, Results: set, Breaks: breaks("old999", run("f:INV-1", "f.EnfINV-1", "killed"))})
	if r := stale.Rows[0]; r.Class == ClassBreakTested || r.Signals.BreakTested.Value != "unknown" {
		t.Errorf("a break result of another commit must not count: %s", r.Class)
	}
	if !strings.Contains(stale.Inputs.Breaks.Ignored, "old999") {
		t.Errorf("ignored = %q", stale.Inputs.Breaks.Ignored)
	}
	bad := &BreaksFile{Schema: "lattice.breaks/9", Runs: []BreakRun{run("f:INV-1", "f.EnfINV-1", "killed")}}
	if r := Compute(Sources{KG: kg, Results: set, Breaks: bad}).Rows[0]; r.Class == ClassBreakTested {
		t.Error("an unknown breaks schema must not count")
	}
}

// A survived run is fake only when every verifier ran. With a verifier that
// could not run (a journey the runner excludes), it reads unknown/partial_run
// and names the verifier; a killed enforcer still makes the promise
// break_tested; a file that predates the fields reads as before.
// @verifies audit:INV-3
func TestPartialRunIsNotFake(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne", "f.TestJourney"}})
	kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "f.Second", Feature: "f", EnforcesInvariants: []string{"INV-1"}})
	set := pass("f.TestOne", "f.TestJourney")
	partial := func(enforcer string) BreakRun {
		r := run("f:INV-1", enforcer, "survived")
		r.RanVerifiers = []string{"f.TestOne"}
		r.NotRunVerifiers = []string{"f.TestJourney"}
		return r
	}

	row := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123", partial("f.EnfINV-1"))}).Rows[0]
	if row.Class != ClassUnknown || row.Kind != KindPartialRun || row.Signals.BreakTested.Value != "partial_run" {
		t.Fatalf("partial: %s/%s %+v", row.Class, row.Kind, row.Signals.BreakTested)
	}
	if !strings.Contains(row.Reason, "f.TestJourney") || Positive(row.Class) {
		t.Errorf("the reason should name the verifier that could not run: %q", row.Reason)
	}
	if len(row.Evidence.Breaks) != 1 || row.Evidence.Breaks[0].NotRunVerifiers[0] != "f.TestJourney" {
		t.Errorf("evidence should carry the run: %+v", row.Evidence.Breaks)
	}

	// One enforcer killed, the other survived partially: still break_tested,
	// and the other enforcer's outcome is in the detail.
	mixed := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123",
		run("f:INV-1", "f.EnfINV-1", "killed"), partial("f.Second"))}).Rows[0]
	if mixed.Class != ClassBreakTested || !strings.Contains(mixed.Signals.BreakTested.Detail, "f.Second: survived") {
		t.Errorf("mixed: %s %+v", mixed.Class, mixed.Signals.BreakTested)
	}

	// A full survival on one enforcer still makes it fake, partial or not on the other.
	both := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123",
		run("f:INV-1", "f.EnfINV-1", "survived"), partial("f.Second"))}).Rows[0]
	if both.Class != ClassFake || both.Kind != KindPassesWhenBroken || len(both.Signals.BreakTested.Items) != 1 || both.Signals.BreakTested.Items[0] != "f.EnfINV-1" {
		t.Errorf("full survival: %s/%s %+v", both.Class, both.Kind, both.Signals.BreakTested)
	}

	// An older breaks.json has no not_run_verifiers: today's reading.
	old := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123", run("f:INV-1", "f.EnfINV-1", "survived"))}).Rows[0]
	if old.Class != ClassFake || old.Kind != KindPassesWhenBroken {
		t.Errorf("old file: %s/%s", old.Class, old.Kind)
	}
	var bf BreaksFile
	if err := json.Unmarshal([]byte(`{"schema":"lattice.breaks/1","commit":"abc123","runs":[{"promise":"f:INV-1","enforcer":"f.EnfINV-1","outcome":"survived"}]}`), &bf); err != nil || len(bf.Runs[0].NotRunVerifiers) != 0 {
		t.Errorf("old json: %v %+v", err, bf)
	}
}

// A structural-only invariant has no verifier to audit: unknown, not fake.
// @verifies audit:INV-2
func TestStructuralOnlyIsUnknown(t *testing.T) {
	invs := []schema.Invariant{{ID: "INV-1", Statement: "s", VerifiableBy: []schema.VerifiableBy{schema.VerifiableByStructural}}}
	kg := graph(invs, []string{"INV-1"}, nil)
	r := Compute(Sources{KG: kg}).Rows[0]
	if r.Class != ClassUnknown || r.Kind != KindStructuralOnly {
		t.Errorf("= %s/%s %s", r.Class, r.Kind, r.Reason)
	}
}

// Only break_tested and exercised are drawn positive; every other class,
// unknown and the gaps included, is not.
// @verifies audit:INV-2
func TestPositiveClasses(t *testing.T) {
	for _, c := range Classes {
		want := c == ClassBreakTested || c == ClassExercised
		if got := Positive(c); got != want {
			t.Errorf("Positive(%s) = %v, want %v", c, got, want)
		}
	}
}

// The live signal folds the enforcers' reach classes.
// @verifies audit:INV-1
func TestLive(t *testing.T) {
	cases := []struct {
		reach []string
		want  string
	}{
		{[]string{schema.ReachEntry}, schema.ReachEntry},
		{[]string{schema.ReachUnreached, schema.ReachEntry}, schema.ReachEntry},
		{[]string{schema.ReachTestOnly, schema.ReachUnreached}, schema.ReachTestOnly},
		{[]string{schema.ReachUnreached}, schema.ReachUnreached},
		{[]string{schema.ReachUnreached, schema.ReachUnknown}, schema.ReachUnknown},
		{[]string{""}, schema.ReachUnknown},
		{nil, schema.ReachUnknown},
	}
	for _, c := range cases {
		var enf []EnforcerEvidence
		for _, r := range c.reach {
			enf = append(enf, EnforcerEvidence{Reach: r})
		}
		if s := live(enf); s.Value != c.want || s.Honesty != UpperBound {
			t.Errorf("%v = %s/%s, want %s", c.reach, s.Value, s.Honesty, c.want)
		}
	}
}

// A second reader's note is shown on the row and never changes its class.
// @verifies audit:INV-1
func TestNotesNeverChangeTheClass(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	cov := fakeCoverage{"f.TestOne": {"f.Other"}}
	base := Compute(Sources{KG: kg, Results: pass("f.TestOne"), Coverage: cov}).Rows[0]
	for _, verdict := range []string{VerdictNotFake, VerdictConfirmedFake} {
		r := Compute(Sources{KG: kg, Results: pass("f.TestOne"), Coverage: cov,
			Notes: []Note{{Promise: "f:INV-1", Reader: "ana", Verdict: verdict, Note: "read it"}}}).Rows[0]
		if r.Class != base.Class || r.Kind != base.Kind || len(r.Notes) != 1 || r.Notes[0].Verdict != verdict {
			t.Errorf("%s: %s/%s notes=%v", verdict, r.Class, r.Kind, r.Notes)
		}
	}
}

// Collect reads the evidence files; the notes loader drops bad entries.
// @verifies audit:INV-4
func TestCollectReadsFiles(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".cache", "results")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	bf, _ := json.Marshal(breaks("abc123", run("f:INV-1", "f.EnfINV-1", "killed")))
	if err := os.WriteFile(filepath.Join(cache, "breaks.json"), bf, 0o644); err != nil {
		t.Fatal(err)
	}
	notes := "- {promise: 'f:INV-1', reader: ana, verdict: not_fake, note: fine}\n" +
		"- {promise: 'f:INV-2', reader: ana, verdict: maybe}\n- {reader: bo, verdict: not_fake}\n"
	if err := os.WriteFile(filepath.Join(dir, "audit-notes.yaml"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	src := Collect(dir, kg, true)
	if src.Breaks == nil || !src.Stamps.Breaks.Present || len(src.Notes) != 1 || len(src.Stamps.Notes.Errors) != 2 {
		t.Fatalf("collect: breaks=%v notes=%v stamps=%+v", src.Breaks, src.Notes, src.Stamps)
	}
	rep := Compute(src)
	if !rep.Dirty || rep.Schema != Schema || rep.Rows[0].Signals.BreakTested.Value != "killed" || len(rep.Rows[0].Notes) != 1 {
		t.Errorf("report = %+v", rep.Rows[0])
	}
	// No files at all: nothing present, nothing fails.
	empty := Compute(Collect(t.TempDir(), kg, false))
	if empty.Inputs.Breaks.Present || empty.Inputs.Results.Present || empty.Inputs.Coverage.Present || empty.Inputs.Notes.Present {
		t.Errorf("inputs = %+v", empty.Inputs)
	}
}

// The JSON report carries the contract fields, the signals with honesty
// labels, and the filters keep the summary in step.
// @verifies audit:INV-1
func TestReportShapeAndFilter(t *testing.T) {
	kg := graph(inv("INV-1", "INV-2"), []string{"INV-1", "INV-2"}, map[string][]string{"INV-1": {"f.TestOne"}, "INV-2": {"f.TestTwo"}})
	cov := fakeCoverage{"f.TestOne": {"f.Other"}, "f.TestTwo": {"f.EnfINV-2"}}
	rep := Compute(Sources{KG: kg, Results: pass("f.TestOne", "f.TestTwo"), Coverage: cov})
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(data, &m)
	for _, k := range []string{"schema", "commit", "dirty", "inputs", "summary", "rows"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	var rows []map[string]json.RawMessage
	_ = json.Unmarshal(m["rows"], &rows)
	var sig map[string]map[string]interface{}
	_ = json.Unmarshal(rows[0]["signals"], &sig)
	for _, s := range []string{"declared", "enforced", "verified", "demonstrated", "exercised", "live", "break_tested", "confirmed"} {
		if h, _ := sig[s]["honesty"].(string); h != Fact && h != UpperBound && h != Proxy {
			t.Errorf("signal %s honesty %q", s, h)
		}
	}
	if rep.Rows[0].Class != ClassFake {
		t.Errorf("worst class first: %s", rep.Rows[0].Class)
	}
	f := Filter{Class: ClassExercised}.Apply(rep)
	if len(f.Rows) != 1 || f.Summary.Total != 1 || f.Summary.Fakes != 0 {
		t.Errorf("filter = %+v", f.Summary)
	}
	var sb strings.Builder
	WriteText(&sb, rep)
	WriteMarkdown(&sb, rep)
	for _, want := range []string{"never_runs_enforcer", "### Fakes", "f:INV-1", "not proof the intent is right"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("render lacks %q", want)
		}
	}
}
