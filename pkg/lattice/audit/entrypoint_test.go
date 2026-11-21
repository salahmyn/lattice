package audit

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/lines"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// epSources is one promise, f:INV-1, kept by f.EnfINV-1 and verified by two
// tests: f.TestDirect calls the enforcer, f.TestEntry calls f.Serve, the
// handler of the entry point f.route, which calls the enforcer. One break of
// the enforcer is killed; failed names the tests that failed after it.
func epSources(failed ...string) Sources {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestDirect", "f.TestEntry"}})
	kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "f.Serve", Kind: "function", File: "serve.go", Line: 10, EndLine: 14, Language: "go", Feature: "f"})
	kg.EntryPoints = []schema.EntryPoint{{ID: "f.route", Status: schema.StatusProduction, Kind: schema.EntryPointKindHTTP,
		Handler: schema.Handler{Symbol: "f.Serve"}}}
	br := run("f:INV-1", "f.EnfINV-1", "killed")
	br.FailedTests = failed
	return Sources{
		KG: kg, Results: pass("f.TestDirect", "f.TestEntry"), Breaks: breaks("abc123", br),
		Calls: &CallGraph{Edges: []CallEdge{
			{From: "f.Serve", To: "f.EnfINV-1", Kind: "call"},
			{From: "f.TestDirect", To: "f.EnfINV-1", Kind: "call"},
			{From: "f.TestEntry", To: "f.Serve", Kind: "call"},
		}},
	}
}

func epProofOf(t *testing.T, src Sources) *EntryProof {
	t.Helper()
	p := rowOf(t, Compute(src), "f:INV-1").EntryPoint
	if p == nil {
		t.Fatal("a killed break has an entry-point proof")
	}
	return p
}

func wantProof(t *testing.T, name string, p *EntryProof, outcome, cause string) {
	t.Helper()
	if p.Outcome != outcome || p.Cause != cause {
		t.Errorf("%s: %s/%s, want %s/%s (%s)", name, p.Outcome, p.Cause, outcome, cause, p.Reason)
	}
}

// perFileCoverage has only a run of the test file, never a test's own.
type perFileCoverage struct{ fakeCoverage }

func (perFileCoverage) TestRan(string, string) (bool, string) { return true, rtm.EvidencePerFile }

// A static path from the killer to the handler shows it can run the way in,
// never that it did: it reads unknown. A killer that reaches no way in, in a
// graph with no unresolved call, reads none.
// @verifies audit:INV-11
func TestEntryProofStaticIsNeverProof(t *testing.T) {
	p := epProofOf(t, epSources("f.TestDirect"))
	wantProof(t, "direct killer only", p, EntryNone, "")
	if len(p.WaysIn) != 1 || p.WaysIn[0].ID != "f.route" || p.WaysIn[0].Handler != "f.Serve" ||
		len(p.Killers) != 1 || p.Killers[0] != "f.TestDirect" || len(p.Enforcers) != 1 {
		t.Errorf("proof = %+v", p)
	}

	p = epProofOf(t, epSources("f.TestEntry"))
	wantProof(t, "entry killer, no coverage", p, EntryUnknown, CauseNoDynamic)
	if !strings.Contains(p.Reason, "f.route (f.Serve)") || !strings.Contains(p.Reason, "statically") {
		t.Errorf("reason = %q", p.Reason)
	}

	wantProof(t, "both killed, no coverage", epProofOf(t, epSources("f.TestDirect", "f.TestEntry")), EntryUnknown, CauseNoDynamic)

	// The proof never changes the class or the break signal.
	row := rowOf(t, Compute(epSources("f.TestDirect")), "f:INV-1")
	if row.Class != ClassBreakTested || row.Signals.BreakTested.Value != "killed" {
		t.Errorf("row = %s %+v", row.Class, row.Signals.BreakTested)
	}
}

// A killer's own per-test run that executed the handler, or a symbol on the
// path from it to the enforcer, is proof. A run that did not execute them is
// not proof of the opposite: the static graph still decides, and it reads
// unknown for a killer that can reach the way in.
// @verifies audit:INV-11
func TestEntryProofPerTestCoverage(t *testing.T) {
	src := epSources("f.TestDirect", "f.TestEntry")
	src.Coverage = fakeCoverage{"f.TestDirect": {"f.EnfINV-1"}, "f.TestEntry": {"f.Serve", "f.EnfINV-1"}}
	p := epProofOf(t, src)
	wantProof(t, "entry killer ran the handler", p, EntryProven, "")
	if len(p.Evidence) != 1 || !strings.Contains(p.Evidence[0], "f.TestEntry: its own run executed the handler f.Serve of f.route") ||
		!strings.Contains(p.Evidence[0], "per-test coverage") {
		t.Errorf("evidence = %v", p.Evidence)
	}

	// The direct test did run the enforcer, which is not a way in.
	src = epSources("f.TestDirect")
	src.Coverage = fakeCoverage{"f.TestDirect": {"f.EnfINV-1"}, "f.TestEntry": {"f.Serve", "f.EnfINV-1"}}
	wantProof(t, "only the direct killer", epProofOf(t, src), EntryNone, "")

	// The entry test failed, but its own run did not execute the handler.
	src = epSources("f.TestEntry")
	src.Coverage = fakeCoverage{"f.TestEntry": {"f.EnfINV-1"}}
	wantProof(t, "per-test run without the handler", epProofOf(t, src), EntryUnknown, CauseNoDynamic)

	// A symbol between the handler and the enforcer counts.
	src = epSources("f.TestEntry")
	src.Calls.Edges = []CallEdge{{From: "f.Serve", To: "f.Mid", Kind: "call"}, {From: "f.Mid", To: "f.EnfINV-1", Kind: "call"},
		{From: "f.TestEntry", To: "f.Serve", Kind: "call"}}
	src.Coverage = fakeCoverage{"f.TestEntry": {"f.Mid", "f.EnfINV-1"}}
	p = epProofOf(t, src)
	wantProof(t, "a symbol on the path", p, EntryProven, "")
	if !strings.Contains(p.Evidence[0], "f.Mid on the path of f.route (f.Serve) to f.EnfINV-1") {
		t.Errorf("evidence = %v", p.Evidence)
	}

	// Only a run of the test file is per_file, not the test's own.
	src = epSources("f.TestEntry")
	src.Coverage = perFileCoverage{fakeCoverage{"f.TestEntry": {"f.Serve"}}}
	wantProof(t, "per_file basis", epProofOf(t, src), EntryUnknown, CauseNoDynamic)
}

func epLines(shared bool, hit, unknown string, tests ...string) *lines.File {
	f := lines.New("tree", "stamp")
	f.Invocations["k1"] = lines.Invocation{Name: "go", Files: map[string]lines.FileLines{
		"serve.go": {Instrumented: "11-13", Hit: hit}}}
	f.Promises["f:INV-1"] = []lines.Group{{Key: "k1", Name: "go", Tests: tests, Shared: shared, Unknown: unknown}}
	return f
}

// verifier-lines.json is group level: an unshared invocation of the killer's
// group that hit a line of the handler is proof, and says it is the group's.
// A shared invocation is not proof, since another test may have run the line.
// @verifies audit:INV-11
func TestEntryProofVerifierLines(t *testing.T) {
	withLines := func(f *lines.File, failed ...string) *EntryProof {
		src := epSources(failed...)
		src.VerifierLines = f
		return epProofOf(t, src)
	}
	p := withLines(epLines(false, "11-12", "", "f.TestDirect", "f.TestEntry"), "f.TestEntry")
	wantProof(t, "unshared", p, EntryProven, "")
	if !strings.Contains(p.Evidence[0], "unshared invocation") || !strings.Contains(p.Evidence[0], "group level") ||
		!strings.Contains(p.Evidence[0], "f.Serve") {
		t.Errorf("evidence = %v", p.Evidence)
	}

	for name, f := range map[string]*lines.File{
		"shared":               epLines(true, "11-12", "", "f.TestDirect", "f.TestEntry"),
		"handler not hit":      epLines(false, "", "", "f.TestDirect", "f.TestEntry"),
		"group has no data":    epLines(false, "11-12", "the baseline did not pass", "f.TestDirect", "f.TestEntry"),
		"killer not in group":  epLines(false, "11-12", "", "f.TestDirect"),
		"other line hit only":  epLines(false, "20", "", "f.TestDirect", "f.TestEntry"),
		"invocation is absent": {Schema: lines.FileSchema, Promises: map[string][]lines.Group{"f:INV-1": {{Key: "gone", Tests: []string{"f.TestEntry"}}}}},
	} {
		wantProof(t, name, withLines(f, "f.TestEntry"), EntryUnknown, CauseNoDynamic)
	}

	// A killer's own per-test run outranks its group's lines: the run is
	// known not to have executed the handler.
	src := epSources("f.TestEntry")
	src.VerifierLines = epLines(false, "11-12", "", "f.TestDirect", "f.TestEntry")
	src.Coverage = fakeCoverage{"f.TestEntry": {"f.EnfINV-1"}}
	wantProof(t, "per-test run known", epProofOf(t, src), EntryUnknown, CauseNoDynamic)
}

// A killer's static graph that has an unresolved call, no reference at all,
// or no path to the enforcer it killed is not complete: it reads unknown,
// never none.
// @verifies audit:INV-11
func TestEntryProofIncompleteGraph(t *testing.T) {
	src := epSources("f.TestDirect")
	src.Calls.Unresolved = []CallUnresolved{{From: "f.TestDirect", Kind: "call", Name: "load", Qualifier: "~", Why: "ambiguous"}}
	p := epProofOf(t, src)
	wantProof(t, "unresolved call in the killer", p, EntryUnknown, CauseIncomplete)
	if !strings.Contains(p.Reason, "unresolved call") || !strings.Contains(p.Reason, "load") {
		t.Errorf("reason = %q", p.Reason)
	}

	// An unresolved call further down, in code the killer reaches.
	src = epSources("f.TestDirect")
	src.Calls.Unresolved = []CallUnresolved{{From: "f.EnfINV-1", Kind: "rpc", Why: "dynamic"}}
	wantProof(t, "unresolved call below", epProofOf(t, src), EntryUnknown, CauseIncomplete)

	// An unresolved call elsewhere does not matter.
	src = epSources("f.TestDirect")
	src.Calls.Unresolved = []CallUnresolved{{From: "f.Elsewhere", Kind: "call", Why: "ambiguous"}}
	wantProof(t, "unresolved call elsewhere", epProofOf(t, src), EntryNone, "")

	src = epSources("f.TestDirect")
	src.Calls.Edges = src.Calls.Edges[:1] // the graph has no reference from the test
	wantProof(t, "no reference from the killer", epProofOf(t, src), EntryUnknown, CauseIncomplete)

	src = epSources("f.TestDirect")
	src.Calls.Edges[1] = CallEdge{From: "f.TestDirect", To: "f.Other", Kind: "call"} // it does not reach what it killed
	wantProof(t, "no path to the enforcer", epProofOf(t, src), EntryUnknown, CauseIncomplete)
}

// What reads unknown, with the reason it does: no ways in, a SQL enforcer, no
// failed test names, no call graph. Only a way in whose static path reaches the
// enforcer is one; a deprecated entry point, or a proposal derived from a
// runtime root, is not.
// @verifies audit:INV-11
func TestEntryProofUnknownReasons(t *testing.T) {
	src := epSources("f.TestDirect")
	src.KG.EntryPoints = nil
	wantProof(t, "no entry point", epProofOf(t, src), EntryUnknown, CauseNoWayIn)

	src = epSources("f.TestDirect")
	src.KG.EntryPoints[0].Status = schema.StatusDeprecated
	wantProof(t, "deprecated entry point", epProofOf(t, src), EntryUnknown, CauseNoWayIn)

	src = epSources("f.TestDirect")
	src.KG.EntryPoints[0].DetectedBy = "runtime_root:serve"
	wantProof(t, "proposed entry point", epProofOf(t, src), EntryUnknown, CauseNoWayIn)

	src = epSources("f.TestDirect")
	src.KG.EntryPoints[0].Handler.Symbol = "f.Unrelated"
	wantProof(t, "handler reaches nothing", epProofOf(t, src), EntryUnknown, CauseNoWayIn)

	src = epSources("f.TestDirect")
	for i := range src.KG.Symbols {
		if src.KG.Symbols[i].FQN == "f.EnfINV-1" {
			src.KG.Symbols[i].Language = "postgres"
		}
	}
	p := epProofOf(t, src)
	wantProof(t, "SQL enforcer", p, EntryUnknown, CauseSQL)
	if !strings.Contains(p.Reason, "SQL enforcer: ways in are database roles") {
		t.Errorf("reason = %q", p.Reason)
	}

	wantProof(t, "no failed names", epProofOf(t, epSources()), EntryUnknown, CauseNoFailed)
	wantProof(t, "failed names of no verifier", epProofOf(t, epSources("f.Unrelated")), EntryUnknown, CauseNoFailed)

	src = epSources("f.TestDirect")
	src.Calls = nil
	wantProof(t, "no call graph", epProofOf(t, src), EntryUnknown, CauseNoGraph)
}

// A runtime root no entry point declares is a way in too, and a way in whose
// path needs a channel is not one: its handler reaches the enforcer only
// asynchronously.
// @verifies audit:INV-11
func TestEntryProofRuntimeRootsAndChannels(t *testing.T) {
	src := epSources("f.TestDirect")
	src.KG.EntryPoints = nil
	src.Calls.Roots = []WayIn{{ID: "runtime root serve.go:10", Handler: "f.Serve", Kind: schema.EntryPointKindHTTP, Source: WayRuntimeRoot}}
	p := epProofOf(t, src)
	wantProof(t, "runtime root", p, EntryNone, "")
	if len(p.WaysIn) != 1 || p.WaysIn[0].Source != WayRuntimeRoot {
		t.Errorf("ways in = %+v", p.WaysIn)
	}

	src = epSources("f.TestDirect")
	src.Calls.Edges[0] = CallEdge{From: "f.Serve", To: "f.EnfINV-1", Kind: "publish"}
	wantProof(t, "asynchronous path", epProofOf(t, src), EntryUnknown, CauseNoWayIn)

	// The handler is the enforcer itself: a test that calls it ran the way in.
	src = epSources("f.TestDirect")
	src.KG.EntryPoints[0].Handler.Symbol = "f.EnfINV-1"
	src.Coverage = fakeCoverage{"f.TestDirect": {"f.EnfINV-1"}}
	wantProof(t, "handler is the enforcer", epProofOf(t, src), EntryProven, "")
}

// A promise whose break did not kill an enforcer has no proof, and the counts
// are over the promises that have one.
// @verifies audit:INV-11
func TestEntryProofCountsAndRows(t *testing.T) {
	src := epSources("f.TestDirect")
	src.Breaks = breaks("abc123", run("f:INV-1", "f.EnfINV-1", "survived"))
	rep := Compute(src)
	if rowOf(t, rep, "f:INV-1").EntryPoint != nil || rep.Summary.EntryPoint != nil {
		t.Errorf("a survived break has no proof: %+v / %+v", rowOf(t, rep, "f:INV-1").EntryPoint, rep.Summary.EntryPoint)
	}
	src.Breaks = nil
	if Compute(src).Summary.EntryPoint != nil {
		t.Error("no breaks.json, no proof")
	}

	rep = Compute(epSources("f.TestDirect"))
	if s := rep.Summary.EntryPoint; s == nil || s.None != 1 || s.Proven != 0 || s.Unknown != 0 {
		t.Fatalf("summary = %+v", s)
	}
	got := Filter{Class: ClassFake}.Apply(rep)
	if got.Summary.EntryPoint != nil {
		t.Errorf("a filter recounts over the rows kept: %+v", got.Summary.EntryPoint)
	}

	s := EntryPointSummary{Proven: 3, None: 1, Unknown: 7, UnknownBy: map[string]int{CauseNoDynamic: 5, CauseIncomplete: 2}}
	want := "entry-point proof: 3 proven, 1 none, 7 unknown (5 no dynamic evidence, 2 incomplete call graph)"
	if s.Line() != want {
		t.Errorf("line = %q, want %q", s.Line(), want)
	}
	if l := (EntryPointSummary{Proven: 1}).Line(); l != "entry-point proof: 1 proven, 0 none, 0 unknown" {
		t.Errorf("line = %q", l)
	}

	var text strings.Builder
	WriteText(&text, rep)
	var md strings.Builder
	WriteMarkdown(&md, rep)
	for name, out := range map[string]string{"text": text.String(), "markdown": md.String()} {
		if !strings.Contains(out, "entry-point proof: 0 proven, 1 none, 0 unknown") || !strings.Contains(out, "none") {
			t.Errorf("%s output lacks the proof:\n%s", name, out)
		}
	}
	if !strings.Contains(text.String(), "entry point none") {
		t.Errorf("the row names its outcome:\n%s", text.String())
	}
}

// BREAK_NO_ENTRYPOINT is raised for the none outcome only, at info: unknown
// raises nothing, and neither does proof.
// @verifies audit:INV-11
func TestEntryPointFindings(t *testing.T) {
	loc := map[string]*schema.Location{"f": {File: "lattice/features/f.yaml"}}
	got := EntryPointFindings(ptr(Compute(epSources("f.TestDirect"))), loc)
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	f := got[0]
	if f.Code != schema.CodeBreakNoEntryPoint || f.Severity != schema.SeverityInfo || f.Subject != "f:INV-1" ||
		f.FeatureID != "f" || f.Location == nil || f.Location.File != "lattice/features/f.yaml" {
		t.Errorf("finding = %+v", f)
	}
	for _, want := range []string{"f:INV-1 is break-tested", "f.EnfINV-1", "f.TestDirect", "f.route (f.Serve)"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message lacks %q: %s", want, f.Message)
		}
	}
	if f.NextAction == nil || f.NextAction.Kind != "add_verification" || !strings.Contains(f.NextAction.Detail, "@verifies f:INV-1") {
		t.Errorf("next action = %+v", f.NextAction)
	}

	proven := epSources("f.TestEntry")
	proven.Coverage = fakeCoverage{"f.TestEntry": {"f.Serve"}}
	for name, src := range map[string]Sources{
		"unknown": epSources("f.TestEntry"),
		"proven":  proven,
	} {
		if got := EntryPointFindings(ptr(Compute(src)), loc); len(got) != 0 {
			t.Errorf("%s: findings = %+v", name, got)
		}
	}
	if EntryPointFindings(nil, loc) != nil {
		t.Error("no audit, no finding")
	}
}

func ptr[T any](v T) *T { return &v }
