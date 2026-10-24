package coverage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// example is the rnd/0009 sample workspace. Its coverage/ files were written
// by Node's built-in test runner (examples/coverage-prototype/regen.sh), not
// by hand.
const example = "../../../examples/coverage-prototype"

// loadExample runs the real extract pipeline on the example: the same
// adapters, graph builder and entry-point loader `lattice extract` uses.
func loadExample(t *testing.T) (schema.KnowledgeGraph, string) {
	t.Helper()
	ws, err := workspace.Open(example)
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
	kg := graph.Build(graph.Input{Manifests: res.Manifests, Modules: res.Modules}, graph.Options{})
	eps, _, err := entrypoints.LoadEntryPointsReport(ws.EntryPointsDir())
	if err != nil {
		t.Fatalf("entry points: %v", err)
	}
	kg.EntryPoints = eps
	return kg, ws.CodeRoots[0].Path
}

func readLCOV(t *testing.T, p string) Report {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	r, err := ParseLCOV(data)
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return r
}

// TestPrototype is the rnd/0009 demonstration. Run it with -v to see the
// findings and levels:
//
//	go test ./pkg/lattice/coverage -run TestPrototype -v
//
// It shows (a) a verifier that misses its enforcer while its invariant reads
// demonstrated, (b) a dead SQL function, (c) an invariant below its coverage
// floor.
func TestPrototype(t *testing.T) {
	kg, root := loadExample(t)
	edges, err := StaticEdges(root, kg)
	if err != nil {
		t.Fatalf("static edges: %v", err)
	}
	in := Input{
		KG:      kg,
		Edges:   edges,
		Suite:   readLCOV(t, filepath.Join(example, "coverage/all.lcov")),
		PerTest: map[string]Report{},
	}
	// One lcov file per test, named by the test title's slug: the last
	// segment of the TypeScript adapter's <module>.test.<slug> FQN.
	for _, tst := range kg.Tests {
		slug := tst.FQN[strings.LastIndex(tst.FQN, ".")+1:]
		p := filepath.Join(example, "coverage/per-test", slug+".lcov")
		if _, err := os.Stat(p); err == nil {
			in.PerTest[tst.FQN] = readLCOV(t, p)
		}
	}
	if len(in.PerTest) != len(kg.Tests) {
		t.Fatalf("per-test coverage for %d of %d tests", len(in.PerTest), len(kg.Tests))
	}

	findings := Analyze(in, Policy{FeatureFloor: 50, InvariantFloor: 80})
	got := map[string]bool{}
	for _, f := range findings {
		got[f.Code+" "+f.Subject] = true
		t.Logf("%-8s %-25s %s", f.Severity, f.Code, f.Message)
	}

	want := []string{
		// (a) the stubbed guard: the test passes, the enforcer never runs.
		"VERIFIER_MISSES_ENFORCER wallet:INV-1",
		// (b) superseded SQL function: no .rpc, no SQL caller, no test.
		"SYMBOL_UNREACHED sql.public.debit_v1",
		// (c) one path of five through the fee enforcer.
		"COVERAGE_BELOW_FLOOR fees:INV-1",
		// Follow from the same data.
		"COVERAGE_BELOW_FLOOR wallet:INV-1",
		"SYMBOL_TEST_ONLY src.statement.formatStatement",
		"SYMBOL_UNTESTED src.wallet.assertSufficientFunds",
		"SYMBOL_UNTESTED src.wallet.debitInDatabase",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing finding %q", w)
		}
	}
	if len(findings) != len(want) {
		t.Errorf("got %d findings, want %d", len(findings), len(want))
	}
	// The live SQL function is reached through `.rpc('debit')`.
	if got["SYMBOL_UNREACHED sql.public.debit"] {
		t.Errorf("sql.public.debit is called through .rpc and must not be unreached")
	}

	// The truth ladder with the proposed top rung. Results come from the
	// junit report Node wrote in the same run, through results.ParseJUnit.
	junit, err := os.ReadFile(filepath.Join(example, "coverage/junit.xml"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := results.ParseJUnit(junit)
	if err != nil {
		t.Fatal(err)
	}
	resultOf := func(fqn string) (bool, bool) {
		o, ok := set.Lookup(fqn)
		return o == results.Pass, ok
	}
	for ref, want := range map[string]string{
		"wallet:INV-1": "demonstrated", // today's top rung, and the most it can claim
		"wallet:INV-2": "exercised",
		"fees:INV-1":   "exercised", // exercised, yet below its floor: the floor is separate
	} {
		level, why := Level(in, ref, resultOf)
		t.Logf("%-13s %-12s %s", ref, level, why)
		if level != want {
			t.Errorf("%s: level %s, want %s (%s)", ref, level, want, why)
		}
	}
}

// TestParseLCOVFunctionRecords: FNDA decides whether a function ran, even
// when its declaration line is marked hit (V8 marks it when the module loads).
func TestParseLCOVFunctionRecords(t *testing.T) {
	r := readLCOV(t, filepath.Join(example, "coverage/per-test/rejects-an-overdraft.lcov"))
	guard := schema.GraphSymbol{FQN: "src.wallet.assertSufficientFunds", File: "src/wallet.js", Line: 8, EndLine: 12}
	if c := Of(r, guard); !c.Known || c.Executed || r["src/wallet.js"].Lines[8] == 0 {
		t.Errorf("assertSufficientFunds: %+v, declaration line hits %d; want known, not executed, declaration line hit",
			c, r["src/wallet.js"].Lines[8])
	}
	withdraw := schema.GraphSymbol{FQN: "src.wallet.withdraw", File: "src/wallet.js", Line: 25, EndLine: 28}
	if c := Of(r, withdraw); !c.Executed {
		t.Errorf("withdraw: %+v, want executed", c)
	}
}

// TestParseGoProfile reads the go test -coverprofile shape. The lines are
// copied from a real profile of this repository's rtm package.
func TestParseGoProfile(t *testing.T) {
	data := []byte("mode: set\n" +
		"github.com/salahmyn/lattice/pkg/lattice/rtm/rtm.go:67.21,68.11 1 1\n" +
		"github.com/salahmyn/lattice/pkg/lattice/rtm/rtm.go:69.24,70.11 1 0\n")
	r, err := ParseGoProfile(data, "github.com/salahmyn/lattice")
	if err != nil {
		t.Fatal(err)
	}
	f := r["pkg/lattice/rtm/rtm.go"]
	if f == nil || f.Lines[67] != 1 || f.Lines[68] != 1 || f.Lines[69] != 0 || f.Lines[70] != 0 {
		t.Fatalf("profile = %+v", f)
	}
}
