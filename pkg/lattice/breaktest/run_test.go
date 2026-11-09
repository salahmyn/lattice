package breaktest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
		// Deno: type-check, resolution and check-file errors are build failures.
		{"", "Check file:///w/a_test.ts\nerror: TS2322 [ERROR]: Type 'string' is not assignable\n", 1, false, statusBuild},
		{"", "error: Module not found \"file:///w/b.ts\".\n", 1, false, statusBuild},
		// A runtime failure with a stack trace is a failing test, not a broken build.
		{"", "running 1 test from ./a_test.ts\nit works ... FAILED (3ms)\n\n ERRORS\n\nit works => ./a_test.ts:4:6\nerror: TypeError: Cannot read properties of undefined (reading 'x')\n    at f (file:///w/a.ts:3:10)\n    at file:///w/a_test.ts:5:3\n", 1, false, statusFail},
		{"", "TypeError: x is not a function\n    at f (web/a.ts:3:10)\n", 1, false, statusFail},
		{"", "SyntaxError: in an assertion message\n at test (a.test.ts:1:1)\n", 1, false, statusFail},
		{"go", "", -1, false, statusError},
		{"go", "partial output", 1, true, statusTimeout},
	}
	for i, c := range cases {
		if got := classify(c.kind, c.exit, c.out, c.timedOut); got.status != c.want {
			t.Errorf("case %d (%q): status %d, want %d (%s)", i, c.out, got.status, c.want, got.reason)
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
	base := Run{EnforcerHash: "e", VerifierHash: "v", Method: MethodEarlyReturn}
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
		"verifier": func(r *Run) { r.VerifierHash = "v2" },
		"method":   func(r *Run) { r.Method = MethodScripted },
		"script":   func(r *Run) { r.ScriptHash = "s" },
		"config":   func(r *Run) { r.ConfigHash = "c" },
	} {
		c := base
		mut(&c)
		if c.cacheKey() == base.cacheKey() {
			t.Errorf("a changed %s must miss the cache", name)
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
