package slice

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

const (
	uiSym  = "web.orders.load"
	apiSym = "api.orders.listOrders"
	policy = "sql.policy.public.orders.reads"
)

// exerciseSlice slices the UI -> http -> API -> table -> policy fixture and
// binds the per-test runs given (test -> symbols it executed) to it.
func exerciseSlice(t *testing.T, runs map[string][]string, verifiers ...string) (Record, *coverage.Index) {
	t.Helper()
	g, kg := httpSliceKG(t, map[string]string{"api/orders.ts": "function listOrders() { return supabase.from('orders').select(); }\napp.get('/api/orders', listOrders);\n"})
	var ix *coverage.Index
	if runs != nil {
		ix = coverage.NewIndex(coverage.Set{PerTest: runs}, kg)
		g = g.WithCoverage(ix)
	}
	rec := Compute(g, "orders:INV-1", enf(policy))
	Exercise(&rec, g, verifiers)
	return rec, ix
}

func uiPath(t *testing.T, rec Record) Path {
	t.Helper()
	for _, p := range rec.Paths {
		if p.Root.FQN == uiSym {
			return p
		}
	}
	t.Fatalf("no path from the UI root: %+v", rec.Paths)
	return Path{}
}

// A browser test with a faked backend runs only UI code and shows nothing
// about the API or the data; a test that ran the handler and the policy shows
// the second half; a test that ran all three shows every hop.
// @verifies reachability:INV-12
func TestHopExercisedOnlyWhenOneVerifierRanBothSides(t *testing.T) {
	runs := map[string][]string{"orders.a": {uiSym}, "orders.b": {apiSym, policy}}
	rec, _ := exerciseSlice(t, runs, "orders.a", "orders.b")
	p := uiPath(t, rec)
	if len(p.Hops) != 3 || p.Hops[0].Kind != "http" {
		t.Fatalf("hops = %+v", p.Hops)
	}
	if h := p.Hops[0]; h.Exercised || len(h.ExercisedBy) != 0 {
		t.Errorf("ui -> api hop exercised by %v although no test ran both sides", h.ExercisedBy)
	}
	for _, h := range p.Hops[1:] { // api -> table, table -> policy
		if !h.Exercised || !reflect.DeepEqual(h.ExercisedBy, []string{"orders.b"}) {
			t.Errorf("hop %s -> %s: exercised %v by %v, want by orders.b", h.From, h.To, h.Exercised, h.ExercisedBy)
		}
	}
	if p.ExercisedHops == nil || *p.ExercisedHops != 2 {
		t.Errorf("exercised hops = %v, want 2 of 3", p.ExercisedHops)
	}
	if rec.Exercised != ExercisedMeasured || rec.Reading != ReadingComplete {
		t.Errorf("exercised %q reading %q: exercising must not change the reading", rec.Exercised, rec.Reading)
	}
	want := []Verifier{{ID: "orders.a", Layers: []string{"ui"}}, {ID: "orders.b", Layers: []string{"api", "data"}}}
	if !reflect.DeepEqual(rec.Verifiers, want) {
		t.Errorf("verifiers = %+v, want %+v", rec.Verifiers, want)
	}

	// Add a test that ran UI, API and the enforcer: every hop is exercised by it.
	runs["orders.c"] = []string{uiSym, apiSym, policy}
	rec, _ = exerciseSlice(t, runs, "orders.a", "orders.b", "orders.c")
	p = uiPath(t, rec)
	for _, h := range p.Hops {
		if !h.Exercised || len(h.ExercisedBy) == 0 || h.ExercisedBy[len(h.ExercisedBy)-1] != "orders.c" {
			t.Errorf("hop %s -> %s: exercised %v by %v, want orders.c among them", h.From, h.To, h.Exercised, h.ExercisedBy)
		}
	}
	if h := p.Hops[0]; !reflect.DeepEqual(h.ExercisedBy, []string{"orders.c"}) {
		t.Errorf("ui -> api hop exercised by %v, want only orders.c", h.ExercisedBy)
	}
	if *p.ExercisedHops != 3 {
		t.Errorf("exercised hops = %d, want 3 of 3", *p.ExercisedHops)
	}
}

// Without per-test coverage nothing reads exercised, the record says why once,
// and a verifier's span is unknown. A per-file run is not per-test evidence.
// @verifies reachability:INV-12
func TestNoPerTestCoverageExercisesNothing(t *testing.T) {
	rec, _ := exerciseSlice(t, nil, "orders.a")
	if rec.Exercised != ExercisedUnknown || !strings.Contains(rec.ExercisedWhy, "no per-test coverage") {
		t.Errorf("exercised %q (%s)", rec.Exercised, rec.ExercisedWhy)
	}
	for _, p := range rec.Paths {
		if p.ExercisedHops != nil {
			t.Errorf("path counts exercised hops without coverage: %d", *p.ExercisedHops)
		}
		for _, h := range p.Hops {
			if h.Exercised || h.ExercisedBy != nil {
				t.Errorf("hop %s -> %s read exercised without coverage", h.From, h.To)
			}
		}
	}
	if !reflect.DeepEqual(rec.Verifiers, []Verifier{{ID: "orders.a", Layers: []string{"unknown"}}}) {
		t.Errorf("verifiers = %+v", rec.Verifiers)
	}
	if rec.Reading != ReadingComplete {
		t.Errorf("reading %q: exercising must not change it", rec.Reading)
	}
	var buf bytes.Buffer
	WriteText(&buf, rec)
	if out := buf.String(); !strings.Contains(out, "exercised: unknown, no per-test coverage") || strings.Contains(out, "=http=>") {
		t.Errorf("text:\n%s", out)
	}

	// An ingested index with no per-test report at all is the same as none.
	rec, _ = exerciseSlice(t, map[string][]string{}, "orders.a")
	if rec.Exercised != ExercisedUnknown || rec.Paths[0].ExercisedHops != nil {
		t.Errorf("an index with no per-test report: exercised %q", rec.Exercised)
	}

	// Coverage for other tests only: this verifier has none, so it exercises nothing.
	rec, _ = exerciseSlice(t, map[string][]string{"orders.other": {uiSym, apiSym, policy}}, "orders.a")
	for _, h := range uiPath(t, rec).Hops {
		if h.Exercised {
			t.Errorf("hop %s -> %s exercised by a verifier with no per-test coverage", h.From, h.To)
		}
	}
	if !reflect.DeepEqual(rec.Verifiers[0].Layers, []string{"unknown"}) {
		t.Errorf("span of a test with no coverage = %v, want [unknown]", rec.Verifiers[0].Layers)
	}
}

// Two tests that each ran one side of a hop do not exercise it: it takes one
// verifier's own run over both sides.
// @verifies reachability:INV-12
func TestTwoTestsEachRanOneSide(t *testing.T) {
	rec, _ := exerciseSlice(t, map[string][]string{"orders.a": {uiSym}, "orders.b": {apiSym}}, "orders.a", "orders.b")
	if h := uiPath(t, rec).Hops[0]; h.Exercised {
		t.Errorf("hop exercised by %v although no single test ran both sides", h.ExercisedBy)
	}
}

// The span of a test is the layers of what its own run executed, from the
// per-test report only; a test with none has no span.
// @verifies reachability:INV-12
func TestLayerSpan(t *testing.T) {
	_, ix := exerciseSlice(t, map[string][]string{"orders.a": {uiSym}, "orders.b": {policy, apiSym, "stale.symbol"}, "orders.none": {}}, "orders.a")
	for test, want := range map[string][]string{"orders.a": {"ui"}, "orders.b": {"api", "data"}, "orders.none": {}} {
		got, ok := ix.LayerSpan(test)
		if !ok || len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
			t.Errorf("LayerSpan(%s) = %v, %v; want %v", test, got, ok, want)
		}
	}
	if got, ok := ix.LayerSpan("orders.unreported"); ok || got != nil {
		t.Errorf("a test with no per-test coverage has span %v, %v; want unknown", got, ok)
	}
}

// A hop's end that is no symbol is stood for by the nearest symbol on its
// side; with no symbol on a side the hop cannot be shown exercised.
// @verifies reachability:INV-12
func TestNonSymbolEndsUseTheNearestSymbol(t *testing.T) {
	f := &fixture{layers: map[string]string{"a.h": "api", "sql.policy.p": "data"}}
	f.root("a.h", "entry point ep").
		edge("a.h", "table", "sql.table.t").
		edge("sql.table.t", "table", "sql.policy.p").
		root("sql.table.t2", "job").
		edge("sql.table.t2", "table", "sql.policy.p")
	g := f.graph()
	ix := coverage.NewIndex(coverage.Set{PerTest: map[string][]string{"t.one": {"a.h", "sql.policy.p"}}}, schema.KnowledgeGraph{})
	rec := Compute(g.WithCoverage(ix), "x:INV-1", enf("sql.policy.p"))
	Exercise(&rec, g.WithCoverage(ix), []string{"t.one"})
	for _, p := range rec.Paths {
		switch p.Root.FQN {
		case "a.h":
			if *p.ExercisedHops != 2 {
				t.Errorf("handler -> table -> policy: %d of 2 exercised, want 2 (table ends use the nearest symbol)", *p.ExercisedHops)
			}
		case "sql.table.t2":
			if *p.ExercisedHops != 0 || p.Hops[0].Exercised {
				t.Errorf("a path that starts at a node with no symbol before it must not read exercised: %+v", p.Hops)
			}
		}
	}
}

// An unknown hop is never exercised, even when both ends ran.
// @verifies reachability:INV-12
func TestUnknownHopIsNeverExercised(t *testing.T) {
	f := &fixture{layers: map[string]string{"a.h": "api", "sql.policy.p": "data"}}
	f.root("a.h", "entry point ep").ambiguous("a.h", "call", "sql.policy.p", "other")
	g := f.graph()
	ix := coverage.NewIndex(coverage.Set{PerTest: map[string][]string{"t.one": {"a.h", "sql.policy.p"}}}, schema.KnowledgeGraph{})
	g = g.WithCoverage(ix)
	rec := Compute(g, "x:INV-1", enf("sql.policy.p"))
	Exercise(&rec, g, []string{"t.one"})
	if h := rec.Paths[0].Hops[0]; h.Exercised || h.Truth != TruthUnknown {
		t.Errorf("hop = %+v", h)
	}
	if rec.Reading != ReadingUnknownFromHop {
		t.Errorf("reading %q", rec.Reading)
	}
}

// The text draws an exercised hop =kind=> and lists the verifiers with their
// spans; the JSON adds only fields.
func TestExerciseText(t *testing.T) {
	rec, _ := exerciseSlice(t, map[string][]string{"orders.a": {uiSym}, "orders.b": {apiSym, policy}}, "orders.a", "orders.b")
	var buf bytes.Buffer
	WriteText(&buf, rec)
	out := buf.String()
	for _, want := range []string{"-http-> api.orders.listOrders [api] =table=> ", "exercised 2 of 3", "=kind=> marks an exercised hop",
		"verifiers (2)", "orders.a  [ui]", "orders.b  [api, data]"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"exercised":"measured"`, `"exercised_hops":2`, `"exercised_by":["orders.b"]`, `"verifiers":[{"id":"orders.a","layers":["ui"]}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("json lacks %s: %s", want, b)
		}
	}
}

// A run of the whole test file is weaker evidence than the test's own run and
// never exercises a hop.
// @verifies reachability:INV-12
func TestPerFileRunDoesNotExerciseAHop(t *testing.T) {
	f := &fixture{layers: map[string]string{"a.h": "api", "a.g": "api"}}
	f.root("a.h", "entry point ep").edge("a.h", "call", "a.g")
	kg := schema.KnowledgeGraph{Tests: []schema.GraphSymbol{{FQN: "t.one", File: "t/one.test.ts"}}}
	set := coverage.Set{
		PerTest: map[string][]string{"t.other": {}}, // some per-test coverage exists
		PerFile: map[string][]string{"t/one.test.ts": {"a.h", "a.g"}},
	}
	g := f.graph().WithCoverage(coverage.NewIndex(set, kg))
	rec := Compute(g, "x:INV-1", enf("a.g"))
	Exercise(&rec, g, []string{"t.one"})
	if h := rec.Paths[0].Hops[0]; h.Exercised {
		t.Errorf("hop exercised by a per-file run: %+v", h)
	}
}
