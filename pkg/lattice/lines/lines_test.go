package lines

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// fn is a production function of feature f with a line extent.
func fn(fqn, file string, line, end int, enforces ...string) schema.GraphSymbol {
	return schema.GraphSymbol{FQN: fqn, Name: fqn, Kind: "function", File: file, Line: line, EndLine: end,
		Language: "go", Feature: "f", EnforcesInvariants: enforces}
}

func testSym(fqn, file string, verifies ...string) schema.GraphSymbol {
	return schema.GraphSymbol{FQN: fqn, File: file, IsTest: true, Feature: "f", Verifies: verifies}
}

func calls(pairs ...string) []Edge {
	var out []Edge
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Edge{From: pairs[i], To: pairs[i+1], Kind: "call"})
	}
	return out
}

func kindsOf(kg schema.KnowledgeGraph, edges []Edge) map[string]string {
	blocks := map[string]bool{}
	for _, s := range kg.Symbols {
		blocks[s.FQN] = true
	}
	out := map[string]string{}
	for f, n := range intents(kg, blocks, edges) {
		out[f] = n.kind()
	}
	return out
}

// A block serves an intent when every caller enforces or serves it, shares it
// when some caller has none, and has none when nothing with an intent calls
// it. A cycle of helpers is decided the same way whatever the order of the
// edges, a test never counts as a caller, and an import edge is not a call.
// @verifies audit:INV-10
func TestIntentFixedPoint(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{
			fn("Enf", "a.go", 1, 10, "INV-1"),
			fn("H1", "a.go", 12, 15), // Enf -> H1 <-> H2
			fn("H2", "a.go", 17, 20),
			fn("Both", "a.go", 22, 25),     // Enf and Main call it
			fn("Deep", "a.go", 27, 30),     // only the shared Both calls it
			fn("Main", "a.go", 32, 40),     // no intent
			fn("Alone", "a.go", 42, 45),    // nobody calls it
			fn("TestOnly", "a.go", 47, 50), // only a test calls it
			fn("Loop1", "a.go", 52, 55),    // a cycle nothing with an intent enters
			fn("Loop2", "a.go", 57, 60),
			fn("Other", "a.go", 62, 70, "INV-2"),
		},
		Tests: []schema.GraphSymbol{testSym("TestEnf", "a_test.go", "INV-1")},
	}
	edges := append(calls("Enf", "H1", "H1", "H2", "H2", "H1", "Enf", "Both", "Main", "Both", "Both", "Deep",
		"TestEnf", "TestOnly", "Loop1", "Loop2", "Loop2", "Loop1", "Other", "H1", "Main", "Enf"),
		Edge{From: "Alone", To: "Enf", Kind: "import"})
	want := map[string]string{
		"Enf": IntentEnforces, "H1": IntentServes, "H2": IntentServes, "Both": IntentShared, "Deep": IntentShared,
		"Main": IntentNone, "Alone": IntentNone, "TestOnly": IntentNone, "Loop1": IntentNone, "Loop2": IntentNone,
		"Other": IntentEnforces,
	}
	got := kindsOf(kg, edges)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("intents = %v, want %v", got, want)
	}
	// H1 is called by Enf (INV-1) and Other (INV-2), which both enforce: it
	// serves both, and an enforcer's own callers never make it shared.
	blocks := map[string]bool{}
	for _, s := range kg.Symbols {
		blocks[s.FQN] = true
	}
	if refs := intents(kg, blocks, edges)["H1"].refs(); !reflect.DeepEqual(refs, []string{"f:INV-1", "f:INV-2"}) {
		t.Errorf("H1 intent = %v", refs)
	}
	// The order of the edges does not change anything.
	rev := make([]Edge, len(edges))
	for i, e := range edges {
		rev[len(edges)-1-i] = e
	}
	if !reflect.DeepEqual(kindsOf(kg, rev), want) {
		t.Errorf("reversed edges changed the intents: %v", kindsOf(kg, rev))
	}
}

// graph and input for a block with lines 10..20 whose invariant f:INV-1 has a
// verifier.
func oneBlock() (schema.KnowledgeGraph, Input) {
	kg := schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{fn("Enf", "a.go", 10, 20, "INV-1")},
		Tests:   []schema.GraphSymbol{testSym("TestEnf", "a_test.go", "INV-1")},
		BRDs: []schema.BRD{{ID: "brd.x", SuccessCriteria: []schema.BRDCriterion{
			{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"f:INV-1"}}}}},
	}
	return kg, Input{KG: kg}
}

func verifierFile(g Group, inv Invocation) *File {
	f := New("tree", "stamp")
	f.Invocations["k"] = inv
	g.Key = "k"
	f.Promises["f:INV-1"] = []Group{g}
	return f
}

func block(t *testing.T, r Report, fqn string) Block {
	t.Helper()
	for _, b := range r.Blocks {
		if b.FQN == fqn {
			return b
		}
	}
	t.Fatalf("no block %s in %+v", fqn, r.Blocks)
	return Block{}
}

// An unshared invocation of the intent's verifiers makes the lines it hit
// run; one that also ran other tests only run_shared; instrumented lines it
// missed are not_run; and the intent says whether a BRD criterion requires it.
// @verifies audit:INV-10
func TestLevelsRunSharedAndNotRun(t *testing.T) {
	_, in := oneBlock()
	inv := Invocation{Name: "r", Files: map[string]FileLines{"a.go": {Instrumented: "11-14", Hit: "11-12"}}}
	in.Verifier = verifierFile(Group{Name: "r", Shared: false}, inv)
	b := block(t, Compute(in), "Enf")
	if b.Levels != (Levels{Run: 2, NotRun: 2}) || b.NotRun != "13-14" {
		t.Errorf("unshared: %+v not_run %q", b.Levels, b.NotRun)
	}
	if len(b.Intent.Invariants) != 1 || b.Intent.Invariants[0] != (Ref{Invariant: "f:INV-1", Required: "required"}) {
		t.Errorf("intent = %+v, want f:INV-1 required", b.Intent)
	}

	in.Verifier = verifierFile(Group{Name: "r", Shared: true, Others: []string{"other"}, OthersTotal: 1}, inv)
	b = block(t, Compute(in), "Enf")
	if b.Levels != (Levels{RunShared: 2, NotRun: 2}) || b.RunShared != "11-12" {
		t.Errorf("shared: %+v run_shared %q", b.Levels, b.RunShared)
	}
}

// A line is never run on the word of data that is not its verifiers': the
// suite and another promise's invocation make it other, a group with no data
// leaves it unknown with the group's reason, and a file nothing lists is
// unknown, not not_run.
// @verifies audit:INV-10
func TestLevelsOtherAndUnknown(t *testing.T) {
	kg, in := oneBlock()

	// Only the suite ran the line: other, not run.
	suite := coverage.NewReport(coverage.FormatLCOV)
	f := suite.File("a.go")
	f.Lines[11], f.Lines[12] = 3, 0
	in.Suite = suite
	b := block(t, Compute(in), "Enf")
	if b.Levels != (Levels{Other: 1, NotRun: 1}) || b.Other != "11" || b.NotRun != "12" {
		t.Errorf("suite only: %+v other %q not_run %q", b.Levels, b.Other, b.NotRun)
	}

	// A group that wrote no report says why, and the suite stays other.
	in.Verifier = verifierFile(Group{Name: "r"}, Invocation{Name: "r", Unknown: "the runner has no {coverage}"})
	b = block(t, Compute(in), "Enf")
	if b.Levels.Run != 0 || b.Levels.Checked != 0 || b.Levels.Other != 1 {
		t.Errorf("a group with no data made lines run: %+v", b.Levels)
	}

	// Nothing lists the file: every line unknown, with the reason.
	in.Suite = coverage.Report{}
	b = block(t, Compute(in), "Enf")
	if b.Levels.Unknown == 0 || b.Levels.Known() != 0 || b.Unknown != "the runner has no {coverage}" {
		t.Errorf("no data: %+v %q", b.Levels, b.Unknown)
	}
	in.Verifier = nil
	if b = block(t, Compute(in), "Enf"); b.Unknown != WhyNotRun {
		t.Errorf("no verifier-lines.json: %q, want %q", b.Unknown, WhyNotRun)
	}
	in.VerifierWhy, in.Verifier = "another tree", New("t", "s")
	if b = block(t, Compute(in), "Enf"); !strings.Contains(b.Unknown, "another tree") || b.Levels.Known() != 0 {
		t.Errorf("stale verifier-lines.json: %+v %q", b.Levels, b.Unknown)
	}
	_ = kg

	// A block no promise owns: any hit is other.
	kg2 := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{fn("Free", "b.go", 1, 6)}}
	s2 := coverage.NewReport(coverage.FormatLCOV)
	s2.File("b.go").Lines[2] = 1
	s2.File("b.go").Lines[3] = 0
	r := Compute(Input{KG: kg2, Suite: s2})
	if b := block(t, r, "Free"); b.Intent.Kind != IntentNone || b.Levels != (Levels{Other: 1, NotRun: 1}) {
		t.Errorf("block with no intent = %+v %+v", b.Intent, b.Levels)
	}
	if r.Workspace.WithIntent.Known() != 0 || r.Workspace.Verified != nil || r.Workspace.NoIntent.Other != 1 {
		t.Errorf("workspace = %+v", r.Workspace)
	}
}

// A line a killed break edited is checked, unless the verifiers' own data
// shows none of them ran it; a break that survived, or of another file, checks
// nothing; and where no report lists the block at all the edited lines are the
// only ones with a level, the rest unknown.
// @verifies audit:INV-10
func TestLevelsChecked(t *testing.T) {
	_, in := oneBlock()
	in.Verifier = verifierFile(Group{Name: "r"}, Invocation{Name: "r",
		Files: map[string]FileLines{"a.go": {Instrumented: "11-14", Hit: "11-13"}}})
	killed := BreakRun{Promise: "f:INV-1", Outcome: "killed", EnforcerFile: "a.go", Method: "early_return", Lines: []int{10, 20}}
	in.Breaks = []BreakRun{killed}
	b := block(t, Compute(in), "Enf")
	// 14 was never run by a verifier, so the break cannot have been noticed
	// by it: it stays not_run.
	if b.Levels != (Levels{Checked: 3, NotRun: 1}) || b.NotRun != "14" {
		t.Errorf("killed: %+v not_run %q", b.Levels, b.NotRun)
	}
	for name, mod := range map[string]func(*BreakRun){
		"survived":   func(r *BreakRun) { r.Outcome = "survived" },
		"other file": func(r *BreakRun) { r.EnforcerFile = "b.go" },
		"edge":       func(r *BreakRun) { r.Method = "edge" },
		"no lines":   func(r *BreakRun) { r.Lines = nil },
	} {
		r := killed
		mod(&r)
		in.Breaks = []BreakRun{r}
		if b := block(t, Compute(in), "Enf"); b.Levels.Checked != 0 || b.Levels.Run != 3 {
			t.Errorf("%s: %+v, want nothing checked", name, b.Levels)
		}
	}

	// No report lists the block: only the edited lines have a level.
	in.Verifier, in.Breaks = nil, []BreakRun{{Promise: "f:INV-1", Outcome: "killed", EnforcerFile: "a.go", Lines: []int{12, 13}}}
	b = block(t, Compute(in), "Enf")
	if b.Levels.Checked != 2 || b.Levels.Unknown != 10-2 || b.Unknown == "" {
		t.Errorf("break lines with no report: %+v %q", b.Levels, b.Unknown)
	}
}

// A block nested in another owns its lines: they are not counted twice.
// @verifies audit:INV-10
func TestNestedBlocksOwnTheirLines(t *testing.T) {
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{fn("Outer", "a.go", 1, 10), fn("Inner", "a.go", 4, 7)}}
	suite := coverage.NewReport(coverage.FormatLCOV)
	for l := 2; l <= 10; l++ {
		suite.File("a.go").Lines[l] = 1
	}
	r := Compute(Input{KG: kg, Suite: suite})
	outer, inner := block(t, r, "Outer"), block(t, r, "Inner")
	if outer.Levels.Other != 6 || inner.Levels.Other != 3 {
		t.Errorf("outer %+v inner %+v, want 6 and 3 lines", outer.Levels, inner.Levels)
	}
}

// A verifier's own per-test run of an enforcer is an unshared run.
// @verifies audit:INV-10
func TestPerTestLinesAreRun(t *testing.T) {
	_, in := oneBlock()
	set := coverage.Set{PerTest: map[string][]string{"TestEnf": {"Enf"}},
		EnforcerLines: map[string]map[string]coverage.LineSet{"TestEnf": {"Enf": {Body: []int{11, 12, 13}, Hit: []int{11, 12}}}}}
	in.Coverage = coverage.NewIndex(set, in.KG)
	b := block(t, Compute(in), "Enf")
	if b.Levels != (Levels{Run: 2, NotRun: 1}) {
		t.Errorf("per-test lines: %+v", b.Levels)
	}
	// Without per-test data for every verifier nothing is claimed.
	in.Coverage = coverage.NewIndex(coverage.Set{}, in.KG)
	if b := block(t, Compute(in), "Enf"); b.Levels.Run != 0 {
		t.Errorf("no per-test data: %+v", b.Levels)
	}
}

// A SQL block has no line data: its lines are unknown with the pgTAP reason,
// and a per-test call count says only that the function ran.
// @verifies audit:INV-10
func TestSQLBlocksAreFunctionLevel(t *testing.T) {
	sql := fn("sql.fn", "m.sql", 1, 8, "INV-1")
	sql.Language = "postgres"
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{sql}, Tests: []schema.GraphSymbol{testSym("TestFn", "t.sql", "INV-1")}}
	set := coverage.Set{PerTest: map[string][]string{"TestFn": {"sql.fn"}}}
	r := Compute(Input{KG: kg, Coverage: coverage.NewIndex(set, kg),
		Source: func(string) ([]byte, bool) {
			return []byte("create function fn()\n-- note\nbegin\n\n  select 1;\nend;\n$$;\n\n"), true
		}})
	b := block(t, r, "sql.fn")
	if b.Levels.Known() != 0 || b.Levels.Unknown != 4 || b.Unknown != WhyPgTAP || !strings.Contains(b.Note, "called it") {
		t.Errorf("SQL block = %+v unknown %q note %q", b.Levels, b.Unknown, b.Note)
	}
}

// The percentage is (checked + run) over the lines with data, printed with the
// unknown count; with no known line it says so rather than dividing by zero.
// @verifies audit:INV-10
func TestPercentNeverOverNoLines(t *testing.T) {
	var none Totals
	none.WithIntent.Unknown = 7
	none.finish()
	if none.Verified != nil || percent(none) != "no line data (7 unknown)" {
		t.Errorf("no known lines: %v %q", none.Verified, percent(none))
	}
	var some Totals
	some.WithIntent = Levels{Checked: 1, Run: 2, RunShared: 1, Other: 1, NotRun: 3, Unknown: 4}
	some.finish()
	if got := percent(some); got != "37.5% (3 of 8 known lines, 4 unknown)" {
		t.Errorf("percent = %q", got)
	}
}

func twoFeatureReport() Report {
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{
		fn("A", "a.go", 1, 6, "INV-1"), fn("B", "b.go", 1, 6, "INV-2"), fn("C", "c.go", 1, 6, "INV-3"),
	}}
	kg.Symbols[1].Feature, kg.Symbols[2].Feature = "g", "h"
	f := New("tree", "stamp")
	f.Invocations["ka"] = Invocation{Files: map[string]FileLines{"a.go": {Instrumented: "2-5", Hit: "2-3"}}}
	f.Invocations["kb"] = Invocation{Files: map[string]FileLines{"b.go": {Instrumented: "2-5", Hit: "2-5"}}}
	f.Promises["f:INV-1"] = []Group{{Key: "ka"}}
	f.Promises["g:INV-2"] = []Group{{Key: "kb"}}
	return Compute(Input{KG: kg, Verifier: f})
}

// A feature is below the floor when its verified lines are, a feature with
// no line data reads "no line data" as info and never as a failure, nothing
// fires with no floor, and the lines with no data never count as verified.
// @verifies audit:INV-10
func TestVerifiedLinesFloor(t *testing.T) {
	rep := twoFeatureReport()
	loc := map[string]*schema.Location{"f": {File: "features/f.yaml"}}

	got := Findings(&rep, 80, loc)
	bySubject := map[string]schema.Violation{}
	for _, v := range got {
		bySubject[v.Subject] = v
	}
	if v, ok := bySubject["f"]; !ok || v.Severity != schema.SeverityWarning || v.Code != schema.CodeVerifiedLinesBelowFloor ||
		!strings.Contains(v.Message, "50.0%") || !strings.Contains(v.Message, "2 of 4 known lines, 0 unknown") || v.Location == nil {
		t.Errorf("feature f: %+v", v)
	}
	if _, ok := bySubject["g"]; ok {
		t.Errorf("feature g is fully verified but reported: %+v", bySubject["g"])
	}
	if v, ok := bySubject["h"]; !ok || v.Severity != schema.SeverityInfo || !strings.Contains(v.Message, "no line data") {
		t.Errorf("feature h has no line data and should say so as info: %+v", v)
	}

	if Findings(&rep, 0, loc) != nil || Findings(nil, 80, loc) != nil {
		t.Errorf("no floor, or no report, must raise nothing")
	}
	if len(Findings(&rep, 50, loc)) != 1 { // only h (no data); f is at 50%
		t.Errorf("a feature at the floor is not below it: %+v", Findings(&rep, 50, loc))
	}
}

// Narrowing keeps one feature, or the blocks of some promises, and recomputes
// the totals over what is kept.
// @verifies audit:INV-10
func TestNarrow(t *testing.T) {
	rep := twoFeatureReport()
	n := Narrow(rep, "g", nil, false)
	if len(n.Blocks) != 1 || n.Blocks[0].FQN != "B" || !n.Narrowed || n.Workspace.WithIntent.Run != 4 {
		t.Errorf("--feature g: %+v", n)
	}
	n = Narrow(rep, "", []string{"f:INV-1"}, true)
	if len(n.Blocks) != 1 || n.Blocks[0].FQN != "A" {
		t.Errorf("--promise f:INV-1: %+v", n.Blocks)
	}
	if got := Narrow(rep, "", nil, false); !reflect.DeepEqual(got, rep) {
		t.Errorf("no filter changed the report")
	}
}

// Blocks, features and ranges come out in one deterministic order.
// @verifies audit:INV-10
func TestComputeIsDeterministic(t *testing.T) {
	a, _ := json.Marshal(twoFeatureReport())
	for i := 0; i < 5; i++ {
		if b, _ := json.Marshal(twoFeatureReport()); !bytes.Equal(a, b) {
			t.Fatalf("run %d differs:\n%s\n%s", i, a, b)
		}
	}
	r := twoFeatureReport()
	var order []string
	for _, b := range r.Blocks {
		order = append(order, b.File)
	}
	if !reflect.DeepEqual(order, []string{"a.go", "b.go", "c.go"}) {
		t.Errorf("blocks not sorted by file: %v", order)
	}
}

func TestRanges(t *testing.T) {
	for in, want := range map[string]string{"": "", "5": "5", "1,2,3,7": "1-3, 7", "9,3,4,4,5": "3-5, 9"} {
		var ls []int
		for _, l := range Expand(in) {
			ls = append(ls, l)
		}
		if got := Compact(ls); got != want {
			t.Errorf("Compact(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Expand("1-3, 7–8, x, 9-2"); !reflect.DeepEqual(got, []int{1, 2, 3, 7, 8}) {
		t.Errorf("Expand = %v", got)
	}
}

// A block a BRD criterion names in enforced_by is guarded, as lattice check
// counts it, so both read the same symbol as having an intent.
// @verifies audit:INV-10
func TestEnforcedByCountsAsGuarded(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{fn("Wired", "a.go", 1, 5), fn("Free", "a.go", 7, 9)},
		BRDs:    []schema.BRD{{ID: "brd.x", SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", EnforcedBy: []string{"Wired"}}}}},
	}
	got := kindsOf(kg, nil)
	if got["Wired"] != IntentEnforces || got["Free"] != IntentNone {
		t.Errorf("intents = %v", got)
	}
}
