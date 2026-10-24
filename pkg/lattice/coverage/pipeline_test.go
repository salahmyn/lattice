package coverage

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// loadGraph runs the real extract pipeline on a workspace: the adapters,
// graph builder and entry-point loader `lattice extract` uses, so symbols,
// extents and enforces/verifies edges are what Lattice produces.
func loadGraph(t *testing.T, dir string) schema.KnowledgeGraph {
	t.Helper()
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	adCfg, err := config.LoadAdapters(ws.LatticeDir)
	if err != nil {
		t.Fatalf("adapters: %v", err)
	}
	res, err := extract.Extract(context.Background(), ws, all.Registry(adCfg), extract.Options{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	kg := graph.Build(graph.Input{BRDs: res.BRDs, Manifests: res.Manifests, Modules: res.Modules}, graph.Options{})
	eps, _, err := entrypoints.LoadEntryPointsReport(ws.EntryPointsDir())
	if err != nil {
		t.Fatalf("entry points: %v", err)
	}
	kg.EntryPoints = eps
	return kg
}

// resultsOf reads a JUnit report into the RTM's ResultOf shape.
func resultsOf(t *testing.T, p string) func(string) (bool, bool) {
	t.Helper()
	set, err := results.ParseJUnit(read(t, p))
	if err != nil {
		t.Fatal(err)
	}
	return func(fqn string) (bool, bool) {
		o, ok := set.Lookup(fqn)
		return o == results.Pass, ok && o != results.Skip
	}
}

func subjects(v []schema.Violation) []string {
	var out []string
	for _, f := range v {
		out = append(out, f.Code+" "+f.Subject)
	}
	sort.Strings(out)
	return out
}

func levels(m rtm.Matrix) map[string]rtm.Status {
	out := map[string]rtm.Status{}
	for _, r := range m.Rows {
		out[r.Ref()] = r.Status
	}
	return out
}

// The example the note describes, end to end on Node's real LCOV: the stubbed
// verifier misses its enforcer and stays demonstrated, the honest ones are
// exercised, and an exercised invariant can still be below its floor.
func TestExampleWorkspace(t *testing.T) {
	kg := loadGraph(t, example)
	set, notes, err := Build(kg, []Input{
		{Path: filepath.Join(example, "coverage/all.lcov"), Include: []string{"src/**"}},
		{Path: filepath.Join(example, "coverage/per-test"), Basis: BasisPerTest, Include: []string{"src/**"}},
	}, Options{OnlyVerifiers: true, Commit: kg.GeneratedFromCommit})
	if err != nil {
		t.Fatal(err)
	}
	// formats-a-statement verifies nothing, so only_verifiers skips it.
	if notes.NonVerifiers != 1 || len(set.PerTest) != 3 || len(notes.Unmatched)+len(notes.Ambiguous) > 0 {
		t.Fatalf("per-test = %v, notes = %+v", set.PerTest, notes)
	}
	if got := set.PerTest["tests.wallet.test.test.rejects-an-overdraft"]; strings.Join(got, " ") != "src.wallet.withdraw" {
		t.Errorf("the stubbed test executed %v, want only src.wallet.withdraw", got)
	}
	ix := NewIndex(set, kg)

	m := rtm.Build(kg, rtm.Options{ResultOf: resultsOf(t, filepath.Join(example, "coverage/junit.xml")), Coverage: ix})
	want := map[string]rtm.Status{
		"wallet:INV-1": rtm.StatusDemonstrated, // today's top rung, and the most it can claim
		"wallet:INV-2": rtm.StatusExercised,
		"fees:INV-1":   rtm.StatusExercised, // exercised, and still below its floor
	}
	for ref, w := range want {
		if got := levels(m)[ref]; got != w {
			t.Errorf("%s = %s, want %s", ref, got, w)
		}
	}
	for _, r := range m.Rows {
		t.Logf("%-13s %-12s evidence=%s %s%s", r.Ref(), r.Status, r.Evidence, r.CoverageReason, strings.Join(r.ExercisedBy, ","))
		if r.Ref() == "fees:INV-1" && (r.EnforcerCoverage == nil || r.EnforcerCoverage.Hit != 8 || r.EnforcerCoverage.Lines != 15) {
			t.Errorf("fees:INV-1 enforcer coverage = %+v, want 8/15 over verifiers", r.EnforcerCoverage)
		}
	}
	if s := m.Summaries[0]; s.Demonstrated != 3 || s.Exercised != 2 {
		t.Errorf("summary dem %d ex %d, want 3 and 2: exercised never lowers demonstrated", s.Demonstrated, s.Exercised)
	}

	got := subjects(Findings(kg, ix, config.Coverage{Floors: config.CoverageFloors{Feature: 50, Invariant: 80, Tier: map[int]float64{3: 90}}}))
	for _, f := range Findings(kg, ix, config.Coverage{}) {
		t.Logf("%s %s", f.Code, f.Message)
	}
	wantFindings := []string{
		"COVERAGE_BELOW_FLOOR brd.wallet:SC-1",
		"COVERAGE_BELOW_FLOOR brd.wallet:SC-3",
		"COVERAGE_BELOW_FLOOR fees:INV-1",
		"COVERAGE_BELOW_FLOOR wallet",
		"COVERAGE_BELOW_FLOOR wallet:INV-1",
		"VERIFIER_MISSES_ENFORCER wallet:INV-1",
	}
	if strings.Join(got, "\n") != strings.Join(wantFindings, "\n") {
		t.Errorf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantFindings, "\n"))
	}
}

// Go: real profiles from one binary run per test, joined by import path.
func TestGoFixture(t *testing.T) {
	dir := "testdata/gomod"
	kg := loadGraph(t, dir)
	set, notes, err := Build(kg, []Input{
		{Path: filepath.Join(dir, "coverage/all.cover")},
		{Path: filepath.Join(dir, "coverage/per-test"), Basis: BasisPerTest},
	}, Options{OnlyVerifiers: true})
	if err != nil {
		t.Fatal(err)
	}
	if notes.NonVerifiers != 1 || len(set.PerTest) != 2 {
		t.Fatalf("per-test %v, notes %+v", set.PerTest, notes)
	}
	ix := NewIndex(set, kg)
	if ran, b := ix.TestRan("wallet.TestRejectsAnOverdraft", "wallet.AssertSufficientFunds"); ran || b != BasisPerTest {
		t.Errorf("stubbed Go test ran the guard: %v %s", ran, b)
	}
	if ran, _ := ix.TestRan("wallet.TestRejectsANonPositiveDeposit", "wallet.AssertPositive"); !ran {
		t.Error("the deposit test runs the one-line guard AssertPositive")
	}
	got := subjects(Findings(kg, ix, config.Coverage{}))
	if strings.Join(got, ",") != "VERIFIER_MISSES_ENFORCER wallet:INV-1" {
		t.Errorf("findings = %v", got)
	}
}

// A per-file directory names each report by its test file. The evidence
// covers every test in the file, can show a miss, and never promotes.
func TestPerFileDir(t *testing.T) {
	kg := loadGraph(t, example)
	dir := t.TempDir()
	// A run of tests/wallet.test.js is stood in for by its two verifiers'
	// real reports back to back (LCOV records of one file merge): the
	// deposit guard ran, the overdraft guard did not.
	file := append(read(t, filepath.Join(example, "coverage/per-test/rejects-an-overdraft.lcov")),
		read(t, filepath.Join(example, "coverage/per-test/rejects-a-non-positive-deposit.lcov"))...)
	if err := os.WriteFile(filepath.Join(dir, "wallet-test.lcov"), file, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated.lcov"), []byte("SF:x.js\nDA:1,1\nend_of_record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, notes, err := Build(kg, []Input{{Path: dir, Basis: BasisPerFile, Include: []string{"src/**"}}}, Options{OnlyVerifiers: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.PerFile["tests/wallet.test.js"]) == 0 || len(notes.Unmatched) != 1 {
		t.Fatalf("per-file = %v, notes = %+v", set.PerFile, notes)
	}
	ix := NewIndex(set, kg)
	if ran, b := ix.TestRan("tests.wallet.test.test.rejects-a-non-positive-deposit", "src.wallet.withdraw"); !ran || b != BasisPerFile {
		t.Errorf("a test in the file gets the file's evidence: %v %s", ran, b)
	}
	m := rtm.Build(kg, rtm.Options{ResultOf: resultsOf(t, filepath.Join(example, "coverage/junit.xml")), Coverage: ix})
	for _, r := range m.Rows {
		if r.Status == rtm.StatusExercised {
			t.Errorf("%s exercised on per-file evidence", r.Ref())
		}
	}
	// No per-test data was ingested, so VERIFIER_UNPROVEN has nothing to
	// rest on; the miss rests on the file's run and says so.
	v := Findings(kg, ix, config.Coverage{})
	if got := subjects(v); strings.Join(got, ",") != "VERIFIER_MISSES_ENFORCER wallet:INV-1" {
		t.Errorf("findings = %v", got)
	}
	if m := messageOf(v, "VERIFIER_MISSES_ENFORCER", "wallet:INV-1"); !strings.Contains(m, "evidence: per_file") {
		t.Errorf("basis: %q", m)
	}
}

// Postgres: call counts per pgTAP test and per test file, joined to
// sql.<schema>.<fn> symbols. The export is hand-written (no Postgres server
// here) and the graph is hand-built in the postgres adapter's shape.
func TestPGCalls(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{ID: "wallet", Invariants: []schema.Invariant{{ID: "INV-3"}}}},
		Symbols: []schema.GraphSymbol{
			{FQN: "sql.public.debit", Kind: "function", File: "db/schema.sql", Line: 2, EndLine: 7, Feature: "wallet", EnforcesInvariants: []string{"INV-3"}},
			{FQN: "sql.public.debit_v1", Kind: "function", File: "db/schema.sql", Line: 12, EndLine: 17, Feature: "wallet"},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "supabase.tests.wallet_test.test.a-debit-lowers-the-balance", IsTest: true, File: "supabase/tests/wallet_test.sql", Verifies: []string{"wallet:INV-3"}},
		},
	}
	for _, f := range []string{"testdata/pg/calls.json", "testdata/pg/calls.csv"} {
		set, notes, err := Build(kg, []Input{{Path: f}}, Options{OnlyVerifiers: true})
		if err != nil || len(notes.Unmatched) > 0 {
			t.Fatalf("%s: %v %+v", f, err, notes)
		}
		if got := set.PerTest["supabase.tests.wallet_test.test.a-debit-lowers-the-balance"]; strings.Join(got, ",") != "sql.public.debit" {
			t.Errorf("%s: per-test = %v", f, set.PerTest)
		}
		if got := set.PerFile["supabase/tests/wallet_test.sql"]; strings.Join(got, ",") != "sql.public.debit" {
			t.Errorf("%s: per-file = %v", f, set.PerFile)
		}
		if c := set.Symbols["sql.public.debit_v1"]; c.Executed {
			t.Errorf("%s: debit_v1 was never called: %+v", f, c)
		}
		if ran, b := NewIndex(set, kg).TestRan("supabase.tests.wallet_test.test.a-debit-lowers-the-balance", "sql.public.debit"); !ran || b != BasisPerTest {
			t.Errorf("%s: %v %s", f, ran, b)
		}
	}
}

// Python: one coverage.py run with contexts gives every test's data, which
// is kept whole even under only_verifiers.
// @verifies coverage:INV-4
func TestPythonContexts(t *testing.T) {
	dir := "testdata/python"
	kg := loadGraph(t, dir)
	set, notes, err := Build(kg, []Input{{Path: filepath.Join(dir, "coverage.json")}}, Options{OnlyVerifiers: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.PerTest) != 3 || len(notes.Unmatched) > 0 {
		t.Fatalf("per-test = %v (all three tests, the non-verifier too), notes %+v", set.PerTest, notes)
	}
	ix := NewIndex(set, kg)
	got := subjects(Findings(kg, ix, config.Coverage{}))
	if strings.Join(got, ",") != "VERIFIER_MISSES_ENFORCER wallet:INV-1" {
		t.Errorf("findings = %v", got)
	}
	if ran, _ := ix.TestRan("tests.test_wallet.test_rejects_a_non_positive_deposit", "src.wallet.assert_positive"); !ran {
		t.Error("the deposit test runs assert_positive")
	}

	// A per-test directory is filtered to verifiers by default, and kept
	// whole when only_verifiers is false.
	js := loadGraph(t, example)
	all, n, err := Build(js, []Input{{Path: filepath.Join(example, "coverage/per-test"), Basis: BasisPerTest}}, Options{OnlyVerifiers: false})
	if err != nil || len(all.PerTest) != 4 || n.NonVerifiers != 0 {
		t.Errorf("only_verifiers false: %d per-test, %+v, %v", len(all.PerTest), n, err)
	}
}
