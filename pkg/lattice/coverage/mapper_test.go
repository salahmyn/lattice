package coverage

import (
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The declaration line is not evidence; a function record decides executed.
// In rejects-an-overdraft.lcov V8 marks assertSufficientFunds' declaration
// line 8 hit when the module loads, and FNDA says it never ran.
// @verifies coverage:INV-2
func TestOfDeclarationLineAndFunctionRecord(t *testing.T) {
	r := parse(t, filepath.Join(example, "coverage/per-test/rejects-an-overdraft.lcov")).Report
	guard := schema.GraphSymbol{FQN: "src.wallet.assertSufficientFunds", File: "src/wallet.js", Line: 8, EndLine: 12}
	c := Of(r, guard, nil)
	if !c.Known || c.Executed || c.Hit != 0 || c.Lines != 4 || r.Files["src/wallet.js"].Lines[8] == 0 {
		t.Errorf("assertSufficientFunds = %+v; want known, 4 body lines, none hit, not executed (declaration line hit %d)",
			c, r.Files["src/wallet.js"].Lines[8])
	}
	if c := Of(r, schema.GraphSymbol{FQN: "src.wallet.withdraw", File: "src/wallet.js", Line: 25, EndLine: 28}, nil); !c.Executed {
		t.Errorf("withdraw = %+v, want executed", c)
	}

	// A function record decides even when body lines read hit.
	// Hand-written: a body line marked, the function record at zero.
	h := NewReport(FormatLCOV)
	f := h.File("a.js")
	f.Lines[2], f.Funcs[1] = 1, 0
	if c := Of(h, schema.GraphSymbol{File: "a.js", Line: 1, EndLine: 3}, nil); c.Executed || c.Hit != 1 {
		t.Errorf("FNDA 0 with a hit body line = %+v, want not executed", c)
	}
	// A class has no record of its own: a method inside it decides.
	f.Funcs[5] = 2
	if c := Of(h, schema.GraphSymbol{File: "a.js", Line: 4, EndLine: 9, Kind: "class"}, nil); !c.Executed {
		t.Errorf("class with a called method = %+v, want executed", c)
	}

	// A one-line Go function: Go blocks never mark a declaration on load, so
	// its own line is evidence. AssertPositive is `func ... { return check(...) }`.
	g := parse(t, "testdata/gomod/coverage/per-test/testrejectsanonpositivedeposit.cover").Report
	g = g.remap(func(p string) string { return "wallet/wallet.go" })
	one := schema.GraphSymbol{FQN: "wallet.AssertPositive", File: "wallet/wallet.go", Line: 19, EndLine: 19}
	if c := Of(g, one, nil); !c.Executed || c.Lines != 1 {
		t.Errorf("one-line Go function = %+v, want executed from its own line", c)
	}
	// The same shape in LCOV has no body lines and no evidence from them.
	js := NewReport(FormatLCOV)
	js.File("b.js").Lines[3] = 1
	if c := Of(js, schema.GraphSymbol{File: "b.js", Line: 3, EndLine: 3}, nil); c.Executed || c.Lines != 0 {
		t.Errorf("one-line JS symbol = %+v, want no body evidence", c)
	}
}

// A file absent from a report has no data, unless the run measured it or it
// is inside the report's include set.
// @verifies coverage:INV-3
func TestOfAbsentFile(t *testing.T) {
	r := parse(t, filepath.Join(example, "coverage/per-test/rejects-an-overdraft.lcov")).Report
	main := schema.GraphSymbol{FQN: "src.cli.main", File: "src/cli.js", Line: 8, EndLine: 17}
	if c := Of(r, main, nil); c.Known {
		t.Errorf("src/cli.js is absent and no include set is declared: %+v, want no data", c)
	}
	inc := includeSet("", []string{"src/**"})
	c := Of(r, main, inc)
	if !c.Known || c.Executed || c.Lines != 9 || c.Hit != 0 {
		t.Errorf("inside include src/**: %+v, want known, 9 unhit body lines", c)
	}
	if c := Of(r, schema.GraphSymbol{File: "lib/x.js", Line: 1, EndLine: 4}, inc); c.Known {
		t.Errorf("outside the include set: %+v, want no data", c)
	}
	// coverage.py contexts share one run: a test that never touched a
	// measured file ran none of it.
	p := parse(t, "testdata/python/coverage.json")
	ctx := p.Contexts["test_wallet.test_deposits"]
	ctx.Files = map[string]*FileCov{} // as if the test touched no file
	if c := Of(ctx, schema.GraphSymbol{File: "src/wallet.py", Line: 24, EndLine: 26}, nil); !c.Known || c.Executed || c.Lines == 0 {
		t.Errorf("measured, untouched: %+v, want known and not executed", c)
	}
}

func TestResolve(t *testing.T) {
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{
		{File: "src/wallet.js"}, {File: "pkg/lattice/rtm/rtm.go"}, {File: "apps/web/src/x.ts"},
		{File: "a/util.ts"}, {File: "b/util.ts"},
	}}
	r := newResolver(kg)
	for _, tc := range []struct{ in, root, want string }{
		{"src/wallet.js", "", "src/wallet.js"},
		{"./src/wallet.js", "", "src/wallet.js"},
		{"/work/coverage-prototype/src/wallet.js", "", "src/wallet.js"},
		{"github.com/salahmyn/lattice/pkg/lattice/rtm/rtm.go", "", "pkg/lattice/rtm/rtm.go"},
		{"src/x.ts", "apps/web", "apps/web/src/x.ts"},
		{"src/x.ts", "", "apps/web/src/x.ts"}, // the only graph file it is a tail of
		{"util.ts", "", "util.ts"},            // two candidates: never guessed
		{`src\wallet.js`, "", "src/wallet.js"},
	} {
		if got := r.resolve(tc.in, tc.root); got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.in, tc.root, got, tc.want)
		}
	}
}
