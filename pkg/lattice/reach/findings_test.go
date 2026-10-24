package reach_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// ran is suite coverage for the findings: fqn -> executed; absent = no data.
type ran map[string]bool

func (r ran) SuiteRan(fqn string) (bool, bool) { v, ok := r[fqn]; return v, ok }

var fixture = map[string]string{
	"src/main.ts": `import { used, alsoUsed } from './lib';
import { maybe } from './amb';
export function main(x: any) {
  used();
  alsoUsed();
  x.shared();
}
`,
	"src/lib.ts": `export function used() { helper(); }
export function alsoUsed() {}
function helper() {}
export function lonely() {}
export function onlyTests() {}
export function ranInTests() {}
`,
	"src/amb.ts": `export class P { shared() {} }
export class Q { shared() {} }
export function maybe() {}
`,
	"tests/lib.test.ts": `import { onlyTests } from '../src/lib';
test('only tests', () => { onlyTests(); });
`,
}

func build(t *testing.T, roots bool, extra ...ir.Module) (schema.KnowledgeGraph, *reach.Result) {
	t.Helper()
	mods := append(parse(t, typescript.New(), fixture), extra...)
	kg := graph.Build(graph.Input{Modules: mods}, graph.Options{})
	var eps []schema.EntryPoint
	if roots {
		eps = []schema.EntryPoint{{ID: "ep.cli.main", Handler: schema.Handler{Symbol: "src.main.main"}}}
		kg.EntryPoints = eps
	}
	r := reach.Compute(reach.Input{Modules: mods, EntryPoints: eps})
	reach.Annotate(&kg, r)
	return kg, r
}

func codes(v []schema.Violation) []string {
	var out []string
	for _, x := range v {
		out = append(out, x.Code+" "+x.Subject)
	}
	sort.Strings(out)
	return out
}

// The four classes: entry from a root, test_only, unreached, and unknown
// for a symbol only an ambiguous reference names (P.shared, Q.shared) and
// for a language with no reference data.
// @verifies reachability:INV-4
func TestClassify(t *testing.T) {
	noRefs := ir.Module{File: "lib/x.php", Language: "php", Symbols: []ir.Symbol{
		{Name: "x", FQN: "App\\x", Kind: ir.KindFunction, File: "lib/x.php", Line: 1}}}
	kg, r := build(t, true, noRefs)
	want := map[string]string{
		"src.main.main":      schema.ReachEntry,
		"src.lib.used":       schema.ReachEntry,
		"src.lib.helper":     schema.ReachEntry,
		"src.lib.onlyTests":  schema.ReachTestOnly,
		"src.lib.lonely":     schema.ReachUnreached,
		"src.amb.P.shared":   schema.ReachUnknown,
		"src.amb.Q.shared":   schema.ReachUnknown,
		"src.amb.maybe":      schema.ReachUnreached, // imported, never called
		"App\\x":             schema.ReachUnknown,
		"src.lib.ranInTests": schema.ReachUnreached,
	}
	for fqn, c := range want {
		if r.Class[fqn] != c {
			t.Errorf("%s: class %q, want %q", fqn, r.Class[fqn], c)
		}
	}
	for _, s := range kg.Symbols {
		if s.Reach != r.Class[s.FQN] {
			t.Errorf("%s: Reach %q on the graph, %q classified", s.FQN, s.Reach, r.Class[s.FQN])
		}
	}

	// With no roots every symbol is unknown.
	_, none := build(t, false)
	for fqn, c := range none.Class {
		if c != schema.ReachUnknown {
			t.Errorf("no roots: %s is %q, want unknown", fqn, c)
		}
	}
}

// An ambiguous call reaches the candidates whose type reached code builds
// or names (Rapid Type Analysis): P is constructed, so P.run is entry; Q
// is never used, so Q.run stays unknown, not unreached. The call itself
// stays unresolved.
// @verifies reachability:INV-4
func TestRapidTypeAnalysis(t *testing.T) {
	mods := parse(t, typescript.New(), map[string]string{
		"src/main.ts": "import { P } from './impl';\nexport function main(x: any) {\n  const p = new P();\n  x.run(p);\n}\n",
		"src/impl.ts": "export class P { run() { helper(); } }\nexport class Q { run() {} }\nfunction helper() {}\n",
	})
	g := reach.Build(mods, reach.Options{})
	r := reach.Classify(g, []reach.Root{{FQN: "src.main.main", Reason: "test"}})
	for fqn, want := range map[string]string{
		"src.impl.P.run":  schema.ReachEntry,
		"src.impl.helper": schema.ReachEntry, // what the dispatched method calls
		"src.impl.Q.run":  schema.ReachUnknown,
	} {
		if r.Class[fqn] != want {
			t.Errorf("%s: class %q, want %q", fqn, r.Class[fqn], want)
		}
	}
	if hasEdge(g, "src.main.main", "src.impl.P.run") || !contains(unresolved(g), "src.main.main run ambiguous") {
		t.Errorf("the call must stay unresolved:%s", joined(unresolved(g)))
	}
}

// Runtime coverage only moves a symbol up: unreached and ran becomes
// test_only; entry that did not run stays entry; no data changes nothing.
// @verifies reachability:INV-5
func TestCoverageOnlyRaises(t *testing.T) {
	cov := ran{"src.lib.ranInTests": true, "src.lib.used": false}
	for _, c := range []struct {
		static, fqn, want string
		raised            bool
	}{
		{schema.ReachUnreached, "src.lib.ranInTests", schema.ReachTestOnly, true},
		{schema.ReachEntry, "src.lib.used", schema.ReachEntry, false},
		{schema.ReachUnreached, "src.lib.lonely", schema.ReachUnreached, false},
		{schema.ReachTestOnly, "src.lib.ranInTests", schema.ReachTestOnly, false},
	} {
		got, raised := reach.Effective(c.static, c.fqn, cov)
		if got != c.want || raised != c.raised {
			t.Errorf("Effective(%s, %s) = %s, %v; want %s, %v", c.static, c.fqn, got, raised, c.want, c.raised)
		}
	}
	if got, _ := reach.Effective(schema.ReachUnreached, "src.lib.ranInTests", nil); got != schema.ReachUnreached {
		t.Errorf("no coverage: %s", got)
	}
}

// Each finding fires on its case and nowhere else, states its evidence,
// and folds coverage in: SYMBOL_UNREACHED, SYMBOL_TEST_ONLY (static, and
// raised by coverage), SYMBOL_UNTESTED (only with suite data that says it
// did not run) and UNUSED_EXPORT (exported, imported by no other module,
// not a root). An unknown symbol raises nothing.
// @verifies reachability:INV-6
func TestFindings(t *testing.T) {
	kg, r := build(t, true)
	cov := ran{"src.lib.ranInTests": true, "src.lib.used": false, "src.lib.helper": true}
	got := codes(reach.Findings(kg, r, cov))
	want := []string{
		"SYMBOL_TEST_ONLY src.lib.onlyTests",
		"SYMBOL_TEST_ONLY src.lib.ranInTests", // unreached statically, ran under the suite
		"SYMBOL_UNREACHED src.amb.maybe",
		"SYMBOL_UNREACHED src.lib.lonely",
		"SYMBOL_UNTESTED src.lib.used", // reached, the suite did not run it
		"UNUSED_EXPORT src.amb.P",
		"UNUSED_EXPORT src.amb.Q",
		"UNUSED_EXPORT src.lib.lonely",
		"UNUSED_EXPORT src.lib.ranInTests",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:%s\nwant:%s", joined(got), joined(want))
	}
	for _, v := range reach.Findings(kg, r, cov) {
		if !strings.Contains(v.Message, "(evidence: ") {
			t.Errorf("%s %s states no evidence: %s", v.Code, v.Subject, v.Message)
		}
		if v.Code == schema.CodeSymbolUnreached && !strings.Contains(v.Message, "suite coverage: it did not run") &&
			!strings.Contains(v.Message, "no coverage for its file") {
			t.Errorf("SYMBOL_UNREACHED does not say what coverage showed: %s", v.Message)
		}
	}
	// Without coverage, SYMBOL_UNTESTED never fires and nothing is raised.
	got = codes(reach.Findings(kg, r, nil))
	if contains(got, "SYMBOL_UNTESTED src.lib.used") || !contains(got, "SYMBOL_UNREACHED src.lib.ranInTests") {
		t.Errorf("without coverage:%s", joined(got))
	}
}

// With no roots, one REACHABILITY_NO_ROOTS stands in for every per-symbol
// finding.
// @verifies reachability:INV-6
func TestFindingsNoRoots(t *testing.T) {
	kg, r := build(t, false)
	got := codes(reach.Findings(kg, r, ran{}))
	if strings.Join(got, ",") != "REACHABILITY_NO_ROOTS reachability" {
		t.Errorf("no roots:%s", joined(got))
	}
	if reach.Findings(kg, nil, nil) != nil {
		t.Error("no call graph, no finding")
	}
}

// With roots, a feature is reached when a root reaches one of its symbols.
// @verifies reachability:INV-7
func TestFeaturesReached(t *testing.T) {
	kg, r := build(t, true)
	for i := range kg.Symbols {
		switch kg.Symbols[i].FQN {
		case "src.lib.used":
			kg.Symbols[i].Feature = "billing"
		case "src.lib.lonely", "src.amb.P.shared":
			kg.Symbols[i].Feature = "dead" // unreached, and unknown
		}
	}
	got := r.FeaturesReached(kg)
	if !got["billing"] || got["dead"] {
		t.Errorf("features reached = %v", got)
	}
	_, none := build(t, false)
	if none.FeaturesReached(kg) != nil {
		t.Error("with no roots the flow-based rule applies: FeaturesReached is nil")
	}
}
