package breaktest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A break that does not compile is unknown, never killed: the runner's build
// failure must not read as a verifier noticing the break.
// @verifies breaktest:INV-3
func TestClassify(t *testing.T) {
	cases := []struct {
		kind, out string
		exit      int
		timedOut  bool
		want      status
	}{
		{"go", "ok  \tfixture/guard\t0.003s\n", 0, false, statusOK},
		{"go", "--- FAIL: TestX (0.00s)\nFAIL\nFAIL\tfixture/guard\t0.003s\n", 1, false, statusFail},
		{"go", "FAIL\tfixture/guard [build failed]\n", 1, false, statusBuild},
		{"go", "FAIL\tfixture/guard [setup failed]\n", 1, false, statusBuild},
		{"go", "    x_test.go:3: got \"FAIL\\tp [build failed]\"\n--- FAIL: TestX\nFAIL\n", 1, false, statusFail},
		{"go", "fatal error: all goroutines are asleep - deadlock!\n--- FAIL: TestX\n", 2, false, statusFail},
		{"", " FAIL  a.test.ts > f\nAssertionError: expected 1 to be 2\n", 1, false, statusFail},
		{"", "Error: Transform failed with 1 error:\nsrc/a.ts:3:10: ERROR: Expected \";\"\n", 1, false, statusBuild},
		{"", "E   IndentationError: unexpected indent\n", 2, false, statusBuild},
		{"", "ERROR collecting tests/test_a.py\n", 2, false, statusBuild},
		{"", "==== ERRORS ====\n_ ERROR collecting tests/test_fees.py _\nImportError while importing test module '/w/tests/test_fees.py'.\n", 2, false, statusBuild},
		// PHP parse errors, as PHP prints them on stderr and on stdout.
		{"", "PHP Parse error:  syntax error, unexpected token \"}\" in /w/src/Fees.php on line 12\n", 255, false, statusBuild},
		{"", "Parse error: syntax error, unexpected '}' in /w/src/Fees.php on line 12\n", 255, false, statusBuild},
		// A TAP harness's "Parse errors" says a test script stopped before its
		// plan: a failing test, not a broken build.
		{"", "db/tests/fees.sql .. 1/10 \n# Looks like you planned 10 tests but ran 4.\ndb/tests/fees.sql .. Dubious, test returned 255 (wstat 65280, 0xff00)\nFailed 6/10 subtests \n\nTest Summary Report\n-------------------\ndb/tests/fees.sql (Wstat: 65280 Tests: 4 Failed: 0)\n  Non-zero exit status: 255\n  Parse errors: Bad plan.  You planned 10 tests but ran 4.\nFiles=1, Tests=4,  0 wallclock secs\nResult: FAIL\n", 1, false, statusFail},
		{"", "db/tests/wallet.sql (Wstat: 768 Tests: 0 Failed: 0)\n  Parse errors: No plan found in TAP output\nResult: FAIL\n", 1, false, statusFail},
		{"", "db/tests/orders.sql .. Failed 2/5 subtests\nResult: FAIL\n", 1, false, statusFail},
		// Deno: type-check, resolution and check-file errors are build failures.
		{"", "Check file:///w/a_test.ts\nerror: TS2322 [ERROR]: Type 'string' is not assignable\n", 1, false, statusBuild},
		{"", "error: Module not found \"file:///w/b.ts\".\n", 1, false, statusBuild},
		// A runtime failure with a stack trace is a failing test, not a broken build.
		{"", "running 1 test from ./a_test.ts\nit works ... FAILED (3ms)\n\n ERRORS\n\nit works => ./a_test.ts:4:6\nerror: TypeError: Cannot read properties of undefined (reading 'x')\n    at f (file:///w/a.ts:3:10)\n    at file:///w/a_test.ts:5:3\n", 1, false, statusFail},
		{"", "TypeError: x is not a function\n    at f (web/a.ts:3:10)\n", 1, false, statusFail},
		{"", "SyntaxError: in an assertion message\n at test (a.test.ts:1:1)\n", 1, false, statusFail},
		{"go", "", -1, false, statusError},
		{"go", "partial output", 1, true, statusTimeout},
		// Exit 125: the runner could not set the code up (a database reset
		// failed because a SQL break did not apply). Unknown, never killed,
		// even with a test failure or SQL error in the output.
		{"", "Applying migration 001_init.sql...\nERROR: syntax error at or near \"OR\" (SQLSTATE 42601)\n", 125, false, statusBuild},
		{"", "not ok 1 - policy denies\n", 125, false, statusBuild},
		{"go", "--- FAIL: TestX\n", 125, false, statusBuild},
	}
	for i, c := range cases {
		if got := classify(c.kind, c.exit, c.out, c.timedOut); got.status != c.want {
			t.Errorf("case %d (%q): status %d, want %d (%s)", i, c.out, got.status, c.want, got.reason)
		}
	}
	if got := classify("", 125, "Applying migration 001_init.sql...\nERROR: syntax error at or near \"OR\" (SQLSTATE 42601)\n", false); got.reason != `did not apply (runner exit 125): ERROR: syntax error at or near "OR" (SQLSTATE 42601)` {
		t.Errorf("reason = %q", got.reason)
	}
}

// A "did not apply" reason quotes the first ERROR:/error: line from the token
// on, else the last non-empty line, and caps it at 120 runes.
// @verifies breaktest:INV-3
func TestApplyReasonTrim(t *testing.T) {
	long := strings.Repeat("é", 300)
	blob := `{"error":{"code":"x","message":"` + strings.Repeat("m", 400) + `"}}`
	cases := []struct{ name, out, want string }{
		{"error line wins over a later line", "reset\nERROR: relation \"a\" does not exist\n" + blob + "\n", `ERROR: relation "a" does not exist`},
		{"lowercase error: marker, from the token", "step 1\nfatal error: boom\nstep 3\n", "error: boom"},
		{"token in the middle of a JSON line", `{"level":"fatal","msg":"ERROR: relation \"a\" does not exist"}` + "\n", `ERROR: relation \"a\" does not exist"}`},
		{"earlier token wins on a line", "x error: one ERROR: two\n", "error: one ERROR: two"},
		{"a quoted JSON key is no marker", blob + "\n", blob[:119] + "…"},
		{"first marker, not the last", "ERROR: first\nERROR: second\n", "ERROR: first"},
		{"last non-empty line otherwise", "one\ntwo\n\n", "two"},
		{"ends at a literal backslash-n", `{"msg":"ERROR: bad thing\nHINT: try again"}` + "\n", `ERROR: bad thing`},
		{"a real newline still ends it", "ERROR: bad thing\nHINT: try again\n", `ERROR: bad thing`},
		{"no output", "  \n", ""},
		{"capped at 120 runes", "ERROR: " + long, "ERROR: " + strings.Repeat("é", 112) + "…"},
		{"last line capped too", "start\n" + blob, blob[:119] + "…"},
	}
	for _, c := range cases {
		got := classify("", 125, c.out, false).reason
		want := "did not apply (runner exit 125)"
		if c.want != "" {
			want += ": " + c.want
		}
		if got != want {
			t.Errorf("%s: reason %q, want %q", c.name, got, want)
		}
		if n := utf8.RuneCountInString(strings.TrimPrefix(got, "did not apply (runner exit 125): ")); n > 120 {
			t.Errorf("%s: quoted %d runes, over the cap", c.name, n)
		}
	}
}

// `go test -run` that matches nothing exits 0; that must not pass as a
// verifier that survived the break.
// @verifies breaktest:INV-4
func TestGoVerifiersPassed(t *testing.T) {
	out := "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n--- SKIP: TestB (0.00s)\nPASS\n"
	if _, ok := goVerifiersPassed(out, []string{"TestA"}); !ok {
		t.Error("TestA passed")
	}
	if name, ok := goVerifiersPassed(out, []string{"TestA", "TestB"}); ok || name != "TestB" {
		t.Errorf("a skipped test is not a pass: %q %v", name, ok)
	}
	if _, ok := goVerifiersPassed("testing: warning: no tests to run\nPASS\n", []string{"TestA"}); ok {
		t.Error("no tests ran")
	}
}

// Timeouts and unknowns are not cached; killed and survived are, and a run
// is reused only when its hashes and method match.
// @verifies breaktest:INV-5
func TestCacheKey(t *testing.T) {
	base := Run{Enforcer: "a.positive", EnforcerHash: "e", VerifierHash: "v", Method: MethodEarlyReturn, InputsHash: "tree:t"}
	for _, o := range []string{Killed, Survived} {
		r := base
		r.Outcome = o
		if !r.cacheable() {
			t.Errorf("%s should be cacheable", o)
		}
	}
	r := base
	r.Outcome = Unknown
	if r.cacheable() {
		t.Error("unknown must not be cached: a timeout or a missing runner can change")
	}
	for name, mut := range map[string]func(*Run){
		"enforcer": func(r *Run) { r.EnforcerHash = "e2" },
		// the same source in another place is another enforcer
		"enforcer name": func(r *Run) { r.Enforcer = "b.positive" },
		"verifier":      func(r *Run) { r.VerifierHash = "v2" },
		"method":        func(r *Run) { r.Method = MethodScripted },
		"script":        func(r *Run) { r.ScriptHash = "s" },
		"config":        func(r *Run) { r.ConfigHash = "c" },
		"inputs":        func(r *Run) { r.InputsHash = "tree:u" },
	} {
		c := base
		mut(&c)
		if c.cacheKey() == base.cacheKey() {
			t.Errorf("a changed %s must miss the cache", name)
		}
	}
}

// A run with no inputs hash (a dirty tree, or a report from before the field
// existed) is never cached, whatever its outcome.
// @verifies breaktest:INV-16
func TestCacheableNeedsInputsHash(t *testing.T) {
	for _, o := range []string{Killed, Survived} {
		if (Run{Outcome: o}).cacheable() {
			t.Errorf("%s with no inputs hash must not be cached", o)
		}
		if !(Run{Outcome: o, InputsHash: "tree:t"}).cacheable() {
			t.Errorf("%s on a named tree should be cached", o)
		}
	}
}

// One run per (promise, enforcer); a script makes it one run per promise;
// --promise and --feature filter; a promise with no enforcer has no run.
// @verifies breaktest:INV-4
func TestPlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "breaks", "pay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "breaks", "pay", "INV-2.patch"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sym := func(fqn, feature string, inv ...string) schema.GraphSymbol {
		return schema.GraphSymbol{FQN: fqn, Name: fqn, Feature: feature, EnforcesInvariants: inv, File: "a.go", Language: "go"}
	}
	kg := schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{
			sym("A", "pay", "INV-1"), sym("B", "pay", "pay:INV-1", "INV-2"), sym("C", "pay", "INV-2"), sym("D", "cart", "INV-1"),
		},
		Tests: []schema.GraphSymbol{
			{FQN: "pkg.TestA", Name: "TestA", Language: "go", File: "a_test.go", Feature: "pay", Verifies: []string{"pay:INV-1"}},
		},
	}
	opts := Options{LatticeDir: dir}
	var got []string
	for _, tg := range plan(kg, opts) {
		var enf []string
		for _, e := range tg.enforcers {
			enf = append(enf, e.FQN)
		}
		got = append(got, tg.promise+"="+strings.Join(enf, "+"))
	}
	want := []string{"cart:INV-1=D", "pay:INV-1=A", "pay:INV-1=B", "pay:INV-2=B+C"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
	if p := plan(kg, Options{LatticeDir: dir, Promise: "pay:INV-1"}); len(p) != 2 || len(p[0].tests) != 1 {
		t.Errorf("--promise: %+v", p)
	}
	if p := plan(kg, Options{LatticeDir: dir, Feature: "cart"}); len(p) != 1 || p[0].promise != "cart:INV-1" {
		t.Errorf("--feature: %+v", p)
	}
}

// Placeholders arrive shell-quoted, so a title with a quote cannot break out.
// @verifies breaktest:INV-4
func TestTemplateArgs(t *testing.T) {
	g := group{language: "typescript", tests: []results.Selector{
		{Test: "a.test.f", Name: "keeps it's total (USD)", File: "web/a.test.ts"},
		{Test: "a.test.g", Name: "g", File: "web/a.test.ts"},
	}}
	files, filter := templateArgs(g)
	if files != "'web/a.test.ts'" {
		t.Errorf("files = %s", files)
	}
	if filter != `'(g|keeps it'\''s total \(USD\))$'` {
		t.Errorf("filter = %s", filter)
	}
	py := group{language: "python", tests: []results.Selector{
		{Test: "tests.test_a.test_x", File: "tests/test_a.py"}, {Test: "tests.test_a.K.test_y", File: "tests/test_a.py"},
	}}
	if _, f := templateArgs(py); f != "'test_x or test_y'" {
		t.Errorf("python filter = %s", f)
	}
}

// --promise <promise>#<case> breaks the case's own enforcer, or each of the
// invariant's when it names none, against only the tests tagged with the
// case, and records the runs under the case ref. An unknown case plans
// nothing, and the promise's own plan is unchanged.
// @verifies breaktest:INV-11
func TestPlanCase(t *testing.T) {
	dir := t.TempDir()
	sym := func(fqn string) schema.GraphSymbol {
		return schema.GraphSymbol{FQN: fqn, Name: fqn, Feature: "pay", EnforcesInvariants: []string{"INV-1"}, File: "a.go", Language: "go"}
	}
	test := func(name string, refs ...string) schema.GraphSymbol {
		g := schema.GraphSymbol{FQN: "pkg." + name, Name: name, Language: "go", File: "a_test.go", Feature: "pay", Verifies: []string{"pay:INV-1"}}
		for _, r := range refs {
			g.VerifiesCases = append(g.VerifiesCases, r)
		}
		return g
	}
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{ID: "pay", Invariants: []schema.Invariant{{ID: "INV-1", Statement: "s", Cases: []schema.InvariantCase{
			{ID: "a", Kind: schema.CaseHappy, Statement: "x"},
			{ID: "b", Kind: schema.CaseError, Statement: "y", Enforcer: "Check"},
		}}}}},
		Symbols: []schema.GraphSymbol{sym("Cap"), sym("Check")},
		Tests:   []schema.GraphSymbol{test("TestHigh", "pay:INV-1#a"), test("TestNegative", "INV-1#b"), test("TestOther")},
	}
	show := func(p []target) []string {
		var out []string
		for _, tg := range p {
			var tests []string
			for _, v := range tg.tests {
				tests = append(tests, v.Name)
			}
			out = append(out, tg.promise+"="+tg.enforcers[0].FQN+":"+strings.Join(tests, "+"))
		}
		return out
	}
	for ref, want := range map[string][]string{
		"pay:INV-1#a": {"pay:INV-1#a=Cap:TestHigh", "pay:INV-1#a=Check:TestHigh"},
		"pay:INV-1#b": {"pay:INV-1#b=Check:TestNegative"},
		"pay:INV-1#z": nil,
		"pay:INV-1":   {"pay:INV-1=Cap:TestHigh+TestNegative+TestOther", "pay:INV-1=Check:TestHigh+TestNegative+TestOther"},
	} {
		if got := show(plan(kg, Options{LatticeDir: dir, Promise: ref})); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: plan = %v, want %v", ref, got, want)
		}
	}
	if o := (Options{Promise: "pay:INV-1#a"}); !o.Selects("pay:INV-1#a") || o.Selects("pay:INV-1") {
		t.Error("a case run selects the case ref only, so the promise's runs are kept")
	}
}
