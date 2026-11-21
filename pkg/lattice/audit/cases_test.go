package audit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// feeGraph is feature f with INV-1 and three cases: a and b use the
// invariant's enforcer f.EnfINV-1, c names its own, f.Validate. Each test
// f.TestX verifies the case its tags name.
func feeGraph(tags map[string][]string) schema.KnowledgeGraph {
	invs := []schema.Invariant{{ID: "INV-1", Statement: "A fee never exceeds five percent of the amount.", Cases: []schema.InvariantCase{
		{ID: "a", Kind: schema.CaseHappy, Statement: "A 100.00 withdrawal pays at most 5.00."},
		{ID: "b", Kind: schema.CaseEdge, Statement: "A 0.01 withdrawal pays no fee."},
		{ID: "c", Kind: schema.CaseError, Statement: "A negative amount is rejected first.", Enforcer: "f.Validate"},
	}}}
	kg := graph(invs, []string{"INV-1"}, nil)
	kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "f.Validate", Feature: "f", EnforcesInvariants: []string{"INV-1"}})
	for test, refs := range tags {
		g := schema.GraphSymbol{FQN: test, File: "f_test.go", Feature: "f", IsTest: true}
		for _, ref := range refs {
			base, _, _ := schema.SplitCaseRef(ref)
			g.Verifies = append(g.Verifies, base)
			if base != ref {
				g.VerifiesCases = append(g.VerifiesCases, ref)
			}
		}
		kg.Tests = append(kg.Tests, g)
	}
	return kg
}

func caseOf(t *testing.T, r Row, id string) CaseRow {
	t.Helper()
	for _, c := range r.Cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no case %s on %s: %+v", id, r.Promise, r.Cases)
	return CaseRow{}
}

func killedWith(promise, enforcer string, failed ...string) BreakRun {
	r := run(promise, enforcer, "killed")
	r.FailedTests = failed
	return r
}

// A case is covered when a tagged verifier passed and breaking the
// enforcer made it fail: the invariant's enforcer for a case with none of
// its own, the case's enforcer otherwise. A case-tagged test still counts as
// a verifier of the invariant, and the cases never change the row's class.
// @verifies audit:INV-8#a
func TestCaseCovered(t *testing.T) {
	kg := feeGraph(map[string][]string{"f.TestHundred": {"f:INV-1#a"}, "f.TestNegative": {"INV-1#c"}})
	set := pass("f.TestHundred", "f.TestNegative")
	rep := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123",
		killedWith("f:INV-1", "f.EnfINV-1", "f.TestHundred"),
		killedWith("f:INV-1", "f.Validate", "TestNegative"))})
	r := rowOf(t, rep, "f:INV-1")
	if r.Class != ClassBreakTested || len(r.Evidence.Verifiers) != 2 {
		t.Fatalf("row = %s with verifiers %+v; case tags count as verifiers of the invariant", r.Class, r.Evidence.Verifiers)
	}
	a, c := caseOf(t, r, "a"), caseOf(t, r, "c")
	if a.Coverage != CaseCovered || a.Label != "1a" || a.Kind != "happy" || len(a.Verifiers) != 1 || a.Verifiers[0] != "f.TestHundred" {
		t.Errorf("a = %+v", a)
	}
	if c.Coverage != CaseCovered || !strings.Contains(c.Reason, "f.Validate") || c.Enforcer != "f.Validate" {
		t.Errorf("c (bare ref, own enforcer) = %+v", c)
	}
	if b := caseOf(t, r, "b"); b.Coverage != CaseUncovered {
		t.Errorf("b has no tagged test: %+v", b)
	}

	// The case's own enforcer is the one that counts: a kill of the
	// invariant's enforcer alone does not cover case c.
	rep = Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123",
		killedWith("f:INV-1", "f.EnfINV-1", "f.TestHundred", "f.TestNegative"))})
	if c := caseOf(t, rowOf(t, rep, "f:INV-1"), "c"); c.Coverage != CasePartial || !strings.Contains(c.Reason, "no break run of f.Validate") {
		t.Errorf("c without a break of its own enforcer = %+v", c)
	}
}

// A case is partial when its tagged verifier passed but nothing shows a
// break catches it: no break run, a break that left it green, a kill that
// failed only other tests, or a kill with no failed test names recorded.
// @verifies audit:INV-8#b
func TestCasePartial(t *testing.T) {
	kg := feeGraph(map[string][]string{"f.TestHundred": {"f:INV-1#a"}, "f.TestOther": {"f:INV-1"}})
	set := pass("f.TestHundred", "f.TestOther")
	for name, tc := range map[string]struct {
		breaks *BreaksFile
		reason string
	}{
		"no break run": {nil, "no break run of f.EnfINV-1"},
		"left green":   {breaks("abc123", run("f:INV-1", "f.EnfINV-1", "survived")), "left f.TestHundred green"},
		"partial run":  {breaks("abc123", partialUnknown("f:INV-1", "f.EnfINV-1", "f.TestOther")), "left f.TestHundred green"},
		"other tests":  {breaks("abc123", killedWith("f:INV-1", "f.EnfINV-1", "f.TestOther")), "failed other tests"},
		"no names":     {breaks("abc123", run("f:INV-1", "f.EnfINV-1", "killed")), "recorded no failed test names"},
		"stale breaks": {breaks("old999", killedWith("f:INV-1", "f.EnfINV-1", "f.TestHundred")), "no break run"},
	} {
		a := caseOf(t, rowOf(t, Compute(Sources{KG: kg, Results: set, Breaks: tc.breaks}), "f:INV-1"), "a")
		if a.Coverage != CasePartial || !strings.Contains(a.Reason, tc.reason) {
			t.Errorf("%s: a = %+v, want partial naming %q", name, a, tc.reason)
		}
	}
}

// partialUnknown is a run lattice break reads unknown because the verifiers
// that ran passed after the break while notRun did not run.
func partialUnknown(promise, enforcer string, notRun ...string) BreakRun {
	r := run(promise, enforcer, "unknown")
	r.Reason = "partial: the verifiers that ran passed after the break"
	r.NotRunVerifiers = notRun
	return r
}

// A case is failing when a tagged verifier is red, even when another tagged
// verifier passed and a break killed it.
// @verifies audit:INV-8#c
func TestCaseFailing(t *testing.T) {
	kg := feeGraph(map[string][]string{"f.TestHundred": {"f:INV-1#a"}, "f.TestRounding": {"f:INV-1#a"}})
	set := pass("f.TestHundred")
	set.Results["f.testrounding"] = results.Fail
	a := caseOf(t, rowOf(t, Compute(Sources{KG: kg, Results: set,
		Breaks: breaks("abc123", killedWith("f:INV-1", "f.EnfINV-1", "f.TestHundred"))}), "f:INV-1"), "a")
	if a.Coverage != CaseFailing || !strings.Contains(a.Reason, "f.TestRounding") || len(a.Verifiers) != 2 {
		t.Errorf("a = %+v", a)
	}
}

// A case is uncovered when no test is tagged with it, or when no tagged
// test has a result (none ran, or it was skipped). A retired case is listed,
// marked, and computed the same way.
// @verifies audit:INV-8#d
func TestCaseUncovered(t *testing.T) {
	kg := feeGraph(map[string][]string{"f.TestHundred": {"f:INV-1#a"}, "f.TestTiny": {"f:INV-1#b"}})
	kg.Features[0].Invariants[0].Cases[1].Retired = true
	set := results.Set{Results: map[string]results.Outcome{"f.testtiny": results.Skip}}
	r := rowOf(t, Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123", killedWith("f:INV-1", "f.EnfINV-1", "f.TestHundred"))}), "f:INV-1")
	if a := caseOf(t, r, "a"); a.Coverage != CaseUncovered || !strings.Contains(a.Reason, "none ran") {
		t.Errorf("a with no result = %+v", a)
	}
	if b := caseOf(t, r, "b"); b.Coverage != CaseUncovered || !b.Retired {
		t.Errorf("b skipped and retired = %+v", b)
	}
	if c := caseOf(t, r, "c"); c.Coverage != CaseUncovered || !strings.Contains(c.Reason, "no test is tagged @verifies f:INV-1#c") {
		t.Errorf("c untagged = %+v", c)
	}
}

// An invariant without cases has no cases key on its row: lattice.audit/1
// only gains a field.
func TestNoCasesNoField(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	b, _ := json.Marshal(Compute(Sources{KG: kg, Results: pass("f.TestOne")}).Rows[0])
	if strings.Contains(string(b), `"cases"`) {
		t.Errorf("row = %s", b)
	}
}

// A break run of one case (`lattice break --promise f:INV-1#c`, recorded
// under the case ref) counts for that case's coverage and never for the
// promise's break signal, which still reads only runs of all its verifiers.
// @verifies breaktest:INV-11
func TestCaseBreakRunCountsForTheCaseOnly(t *testing.T) {
	kg := feeGraph(map[string][]string{"f.TestNegative": {"f:INV-1#c"}, "f.TestHundred": {"f:INV-1#a"}})
	set := pass("f.TestNegative", "f.TestHundred")
	rep := Compute(Sources{KG: kg, Results: set, Breaks: breaks("abc123",
		killedWith("f:INV-1#c", "f.Validate", "f.TestNegative"),
		run("f:INV-1#a", "f.EnfINV-1", "survived"))})
	r := rowOf(t, rep, "f:INV-1")
	if c := caseOf(t, r, "c"); c.Coverage != CaseCovered || !strings.Contains(c.Reason, "f.Validate") {
		t.Errorf("c = %+v", c)
	}
	if a := caseOf(t, r, "a"); a.Coverage != CasePartial || !strings.Contains(a.Reason, "left f.TestHundred green") {
		t.Errorf("a = %+v", a)
	}
	if r.Signals.BreakTested.Value != "unknown" || len(r.Evidence.Breaks) != 0 || r.Class == ClassFake {
		t.Errorf("case runs must not reach the promise: break_tested %s, breaks %+v, class %s", r.Signals.BreakTested.Value, r.Evidence.Breaks, r.Class)
	}
}

// An edge run breaks a hop, not an enforcer: even a survived one never reads
// as the promise's break signal, and EdgeRuns lists it for the commit the
// graph is at only.
// @verifies audit:INV-3
func TestEdgeRunsAreNotTheBreakSignal(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	set := pass("f.TestOne")
	edge := run("f:INV-1", "f.client", "survived")
	edge.Method, edge.Hop = MethodEdge, &BreakHop{From: "f.client", To: "f.handler", Kind: "http"}
	bf := breaks("abc123", run("f:INV-1", "f.EnfINV-1", "killed"), edge)
	row := Compute(Sources{KG: kg, Results: set, Breaks: bf}).Rows[0]
	if row.Signals.BreakTested.Value != "killed" || row.Class != ClassBreakTested || len(row.Evidence.Breaks) != 1 {
		t.Errorf("a survived edge run changed the break signal: %s %+v", row.Class, row.Signals.BreakTested)
	}
	if got := EdgeRuns(bf, "abc123")["f:INV-1"]; len(got) != 1 || got[0].Hop.To != "f.handler" {
		t.Errorf("edge runs = %+v", got)
	}
	if got := EdgeRuns(bf, "old999"); len(got) != 0 {
		t.Errorf("edge runs of another commit were listed: %+v", got)
	}
}
