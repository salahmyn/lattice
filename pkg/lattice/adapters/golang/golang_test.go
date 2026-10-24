package golang

import (
	"context"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func parse(t *testing.T, file, src string) ir.Module {
	t.Helper()
	mod, err := New().Parse(context.Background(), file, []byte(src))
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	return mod
}

func byFQN(mod ir.Module) map[string]ir.Symbol {
	out := map[string]ir.Symbol{}
	for _, s := range mod.Symbols {
		out[s.FQN] = s
	}
	return out
}

func args(s ir.Symbol, kind string) []string {
	var out []string
	for _, a := range s.Annotations {
		if a.Kind != kind {
			continue
		}
		for _, v := range a.Args {
			out = append(out, v.(string))
		}
	}
	return out
}

func orphans(mod ir.Module) []ir.Diagnostic {
	var out []ir.Diagnostic
	for _, d := range mod.Diagnostics {
		if d.Code == schema.CodeAnnotationOrphaned {
			out = append(out, d)
		}
	}
	return out
}

const goSample = `// Package refund settles refunds.
//
// @module-feature billing.refund
// @module-enforces billing.refund:INV-9
package refund

import "errors"

// Max is untagged, so it is no symbol.
const Max = 10

// @feature billing.refund
const Limit = 3

var (
	// @enforces billing.refund:INV-4
	ErrTooLarge = errors.New("too large")
	other       int
)

// Processor settles refunds.
// @feature billing.refund
type Processor struct{ n int }

type (
	// Store keeps refunds.
	Store interface{ Get() }
	Amount int
	ID = string
	box[T any] struct{ v T }
)

// Settle pays a refund.
// @enforces billing.refund:INV-1, billing.refund:INV-2
// @capability self_service_refund @role finance
func Settle[T any](v T) error { return nil }

// @enforces billing.refund:INV-3
func (p *Processor) Run() error {
	return nil
}

func (b box[T]) get() T { return b.v }

func (Amount) String() string { return "" }

func helper() {}
`

func TestParseGoSymbols(t *testing.T) {
	mod := parse(t, "pkg/billing/refund/refund.go", goSample)
	if mod.Language != "go" {
		t.Errorf("language = %q", mod.Language)
	}
	if got := len(mod.ModuleAnnotations); got != 2 {
		t.Fatalf("module annotations = %d, want 2: %+v", got, mod.ModuleAnnotations)
	}
	if !mod.HasCode {
		t.Error("a var with an initializer is runtime code: HasCode should be set")
	}
	syms := byFQN(mod)
	want := []struct {
		fqn       string
		kind      ir.SymbolKind
		exported  bool
		enclosing string
		line, end int
	}{
		{"pkg.billing.refund.Limit", ir.KindConst, true, "", 13, 13},
		{"pkg.billing.refund.ErrTooLarge", ir.KindConst, true, "", 17, 17},
		{"pkg.billing.refund.Processor", ir.KindClass, true, "", 23, 23},
		{"pkg.billing.refund.Store", ir.KindInterface, true, "", 27, 27},
		{"pkg.billing.refund.Amount", ir.KindClass, true, "", 28, 28},
		{"pkg.billing.refund.ID", ir.KindClass, true, "", 29, 29},
		{"pkg.billing.refund.box", ir.KindClass, false, "", 30, 30},
		{"pkg.billing.refund.Settle", ir.KindFunction, true, "", 36, 36},
		{"pkg.billing.refund.Processor.Run", ir.KindMethod, true, "pkg.billing.refund.Processor", 39, 41},
		{"pkg.billing.refund.box.get", ir.KindMethod, false, "pkg.billing.refund.box", 43, 43},
		{"pkg.billing.refund.Amount.String", ir.KindMethod, true, "pkg.billing.refund.Amount", 45, 45},
		{"pkg.billing.refund.helper", ir.KindFunction, false, "", 47, 47},
	}
	for _, w := range want {
		s, ok := syms[w.fqn]
		if !ok {
			t.Errorf("missing symbol %s", w.fqn)
			continue
		}
		if s.Kind != w.kind || s.Exported != w.exported || s.EnclosingFQN != w.enclosing ||
			s.Line != w.line || s.EndLine != w.end || s.IsTest {
			t.Errorf("%s = kind %s exported %v enclosing %q lines %d-%d test %v; want %s %v %q %d-%d",
				w.fqn, s.Kind, s.Exported, s.EnclosingFQN, s.Line, s.EndLine, s.IsTest,
				w.kind, w.exported, w.enclosing, w.line, w.end)
		}
	}
	for _, untagged := range []string{"pkg.billing.refund.Max", "pkg.billing.refund.other"} {
		if _, ok := syms[untagged]; ok {
			t.Errorf("%s is an untagged const/var and should be no symbol", untagged)
		}
	}
	if got := len(mod.Symbols); got != len(want) {
		t.Errorf("symbols = %d, want %d", got, len(want))
	}

	settle := syms["pkg.billing.refund.Settle"]
	if got := strings.Join(args(settle, "enforces_invariant"), " "); got != "billing.refund:INV-1 billing.refund:INV-2" {
		t.Errorf("Settle @enforces = %q", got)
	}
	if got := args(settle, "capability"); len(got) != 1 || got[0] != "self_service_refund" {
		t.Errorf("Settle @capability = %v", got)
	}
	if got := args(settle, "role"); len(got) != 1 || got[0] != "finance" {
		t.Errorf("Settle @role = %v", got)
	}
	if got := args(syms["pkg.billing.refund.ErrTooLarge"], "enforces_invariant"); len(got) != 1 {
		t.Errorf("ErrTooLarge @enforces = %v", got)
	}
	if got := args(syms["pkg.billing.refund.Processor.Run"], "enforces_invariant"); len(got) != 1 || got[0] != "billing.refund:INV-3" {
		t.Errorf("Run @enforces = %v", got)
	}
	if d := orphans(mod); len(d) != 0 {
		t.Errorf("unexpected orphans: %+v", d)
	}
}

func TestHasCode(t *testing.T) {
	for _, tc := range []struct {
		name, file, src string
		want            bool
	}{
		{"types and consts only", "a/types.go", "package a\n\nconst X = 1\n\ntype T struct{}\n", false},
		{"a var with a value", "a/vars.go", "package a\n\nvar registry = map[string]int{}\n", true},
		{"an init function", "a/init.go", "package a\n\nfunc init() {}\n", true},
		{"a declared var only", "a/decl.go", "package a\n\nvar x int\n", false},
		{"never in a test file", "a/a_test.go", "package a\n\nvar x = 1\n\nfunc init() {}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parse(t, tc.file, tc.src).HasCode; got != tc.want {
				t.Errorf("HasCode = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRootPackageUsesPackageName(t *testing.T) {
	mod := parse(t, "main.go", "package main\n\nfunc main() {}\n")
	if len(mod.Symbols) != 1 || mod.Symbols[0].FQN != "main.main" {
		t.Fatalf("symbols = %+v, want main.main", mod.Symbols)
	}
	mod = parse(t, "lattice_test.go", "package lattice_test\n\nfunc TestX(t *testing.T) {}\n")
	if len(mod.Symbols) != 1 || mod.Symbols[0].FQN != "lattice.TestX" {
		t.Fatalf("symbols = %+v, want lattice.TestX", mod.Symbols)
	}
}

func TestBlockCommentTags(t *testing.T) {
	mod := parse(t, "a/a.go", "package a\n\n/*\n * @feature a.b\n * @suppresses a.b:INV-1 reason: covered by the database\n */\nfunc F() {}\n")
	s := byFQN(mod)["a.F"]
	if got := args(s, "feature"); len(got) != 1 || got[0] != "a.b" {
		t.Fatalf("@feature = %v", got)
	}
	var sup ir.Annotation
	for _, a := range s.Annotations {
		if a.Kind == "suppresses_invariant" {
			sup = a
		}
	}
	if len(sup.Args) != 1 || sup.Kwargs["reason"] != "covered by the database" {
		t.Errorf("@suppresses = %+v", sup)
	}
}

const goProseSample = `package a

// build fuses the error contracts manifests declare with the
// @error annotations found in source, and the
// @surface annotations on symbols.
// @error REFUND_TOO_LARGE
// @surface http POST /refunds
func build() {
	// @enforces symbol carries the tag but guards nothing
}
`

// TestProseIsNotATag checks that a wrapped doc comment line that happens to
// start with "@" reads as prose, not as a tag, while real tags on the same
// symbol still parse.
func TestProseIsNotATag(t *testing.T) {
	mod := parse(t, "a/a.go", goProseSample)
	s := byFQN(mod)["a.build"]
	if got := args(s, "error"); len(got) != 1 || got[0] != "REFUND_TOO_LARGE" {
		t.Errorf("@error = %v, want [REFUND_TOO_LARGE]", got)
	}
	if got := args(s, "surface"); len(got) != 1 || got[0] != "http POST /refunds" {
		t.Errorf("@surface = %v, want [http POST /refunds]", got)
	}
	if d := orphans(mod); len(d) != 0 {
		t.Errorf("prose inside a body is no orphaned tag: %+v", d)
	}
}

const goOrphanSample = `package a

// @feature a.b
import "fmt"

// @feature a.c

func Detached() {}

func Body() {
	// @enforces a.b:INV-1
	fmt.Println()
}

var x = 1 // @feature a.d

// Tagged init.
// @enforces a.b:INV-2
func init() {}

// @feature a.e
`

func TestOrphanedAnnotations(t *testing.T) {
	mod := parse(t, "a/a.go", goOrphanSample)
	d := orphans(mod)
	wantLines := []int{3, 6, 11, 15, 18, 21}
	if len(d) != len(wantLines) {
		t.Fatalf("orphans = %+v, want lines %v", d, wantLines)
	}
	got := map[int]string{}
	for _, o := range d {
		got[o.Line] = o.Message
	}
	for _, ln := range wantLines {
		if got[ln] == "" {
			t.Errorf("no orphan at line %d; got %+v", ln, d)
		}
	}
	if !strings.Contains(got[6], "blank line") {
		t.Errorf("line 6 message should name the blank line: %q", got[6])
	}
	if !strings.Contains(got[11], "function body") {
		t.Errorf("line 11 message should name the function body: %q", got[11])
	}
	if !strings.Contains(got[18], "init is not addressable") {
		t.Errorf("line 18 message should explain init: %q", got[18])
	}
	if !strings.Contains(got[15], "trails code") {
		t.Errorf("line 15 message should say it trails code: %q", got[15])
	}
	if s := byFQN(mod)["a.Detached"]; len(s.Annotations) != 0 {
		t.Errorf("a tag separated by a blank line must not attach: %+v", s.Annotations)
	}
}

const goTestSample = `package results_test

import "testing"

// @verifies results:INV-1
func TestLookup(t *testing.T) {
	// @verifies results:INV-2
	t.Run("a failure wins", func(t *testing.T) {
		t.Run("across reports", func(t *testing.T) {})
	})
	t.Run("a failure wins", func(t *testing.T) {})
	t.Run("", func(t *testing.T) {})

	cases := []struct{ name string }{{"x"}, {"y"}}
	// @verifies results:INV-3
	for _, tc := range cases {
		// @verifies results:INV-4
		t.Run(tc.name, func(t *testing.T) {
			t.Run("inner", func(t *testing.T) {})
		})
	}
}

func helper(t *testing.T) {
	// @verifies results:INV-5
	t.Run("from a helper", func(t *testing.T) {})
}

func BenchmarkLookup(b *testing.B) {}
`

func TestTestSymbolsAndSubtests(t *testing.T) {
	mod := parse(t, "pkg/lattice/results/results_test.go", goTestSample)
	syms := byFQN(mod)
	for _, s := range mod.Symbols {
		if !s.IsTest {
			t.Errorf("%s from a _test.go file should be IsTest", s.FQN)
		}
	}
	const root = "pkg.lattice.results.TestLookup"
	for _, fqn := range []string{
		root,
		root + ".a_failure_wins",
		root + ".a_failure_wins.across_reports",
		root + ".a_failure_wins#01",
		root + ".#00",
		"pkg.lattice.results.helper",
		"pkg.lattice.results.BenchmarkLookup",
	} {
		if _, ok := syms[fqn]; !ok {
			t.Errorf("missing %s; have %v", fqn, keys(syms))
		}
	}
	if got := len(mod.Symbols); got != 7 {
		t.Errorf("symbols = %d, want 7 (dynamic subtests add none): %v", got, keys(syms))
	}
	sub := syms[root+".a_failure_wins"]
	if sub.Name != "a failure wins" || sub.EnclosingFQN != root {
		t.Errorf("subtest = %+v", sub)
	}
	if got := args(sub, "verifies"); len(got) != 1 || got[0] != "results:INV-2" {
		t.Errorf("subtest @verifies = %v", got)
	}
	// Tags above the table loop and above t.Run(tc.name, ...) attach to the
	// test itself: its run passes only when every row passes.
	if got := strings.Join(args(syms[root], "verifies"), " "); got != "results:INV-1 results:INV-3 results:INV-4" {
		t.Errorf("TestLookup @verifies = %q", got)
	}
	if got := args(syms["pkg.lattice.results.helper"], "verifies"); len(got) != 1 {
		t.Errorf("helper @verifies = %v", got)
	}
	if d := orphans(mod); len(d) != 0 {
		t.Errorf("unexpected orphans: %+v", d)
	}
}

func keys(m map[string]ir.Symbol) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestIsGoTestName(t *testing.T) {
	for name, want := range map[string]bool{
		"TestX": true, "Test": true, "Test_x": true, "Testify": false,
		"BenchmarkParse": true, "FuzzParse": true, "ExampleSet_Lookup": true, "helper": false,
	} {
		if got := isGoTestName(name); got != want {
			t.Errorf("isGoTestName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRewriteSubtest(t *testing.T) {
	for in, want := range map[string]string{
		"a failure wins": "a_failure_wins",
		"tab\there":      "tab_here",
		"bell\a":         `bell\a`,
		"a/b":            "a/b",
	} {
		if got := rewriteSubtest(in); got != want {
			t.Errorf("rewriteSubtest(%q) = %q, want %q", in, got, want)
		}
	}
}

// junitReport is what gotestsum --junitfile (and go-junit-report) write for
// `go test ./...`: classname is the package import path, name the test's run
// name with subtests joined by "/".
const junitReport = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="example.com/shop/pkg/lattice/results" tests="6">
    <testcase classname="example.com/shop/pkg/lattice/results" name="TestLookup"></testcase>
    <testcase classname="example.com/shop/pkg/lattice/results" name="TestLookup/a_failure_wins"></testcase>
    <testcase classname="example.com/shop/pkg/lattice/results" name="TestLookup/a_failure_wins/across_reports"></testcase>
    <testcase classname="example.com/shop/pkg/lattice/results" name="TestLookup/a_failure_wins#01">
      <failure message="Failed"></failure>
    </testcase>
    <testcase classname="example.com/shop/pkg/lattice/results" name="TestLookup/#00">
      <skipped message="skipped"></skipped>
    </testcase>
    <testcase classname="example.com/shop/pkg/lattice/results" name="BenchmarkLookup"></testcase>
  </testsuite>
  <testsuite name="example.com/shop/pkg/lattice/other" tests="1">
    <testcase classname="example.com/shop/pkg/lattice/other" name="TestLookup">
      <failure message="Failed"></failure>
    </testcase>
  </testsuite>
</testsuites>`

// TestGoTestResultsJoin checks that the FQNs the adapter gives Go tests and
// subtests resolve to their go test JUnit cases, and only to them: a
// same-named test in another package neither joins nor makes the lookup
// ambiguous.
func TestGoTestResultsJoin(t *testing.T) {
	mod := parse(t, "pkg/lattice/results/results_test.go", goTestSample)
	set, err := results.ParseJUnit([]byte(junitReport))
	if err != nil {
		t.Fatal(err)
	}
	const root = "pkg.lattice.results.TestLookup"
	want := map[string]results.Outcome{
		root:                                    results.Pass,
		root + ".a_failure_wins":                results.Pass,
		root + ".a_failure_wins.across_reports": results.Pass,
		root + ".a_failure_wins#01":             results.Fail,
		root + ".#00":                           results.Skip,
		"pkg.lattice.results.BenchmarkLookup":   results.Pass,
	}
	syms := byFQN(mod)
	for fqn, outcome := range want {
		if _, ok := syms[fqn]; !ok {
			t.Errorf("adapter emitted no %s", fqn)
			continue
		}
		if amb := set.Ambiguous(fqn); amb != nil {
			t.Errorf("%s is ambiguous: %v", fqn, amb)
		}
		got, ok := set.Lookup(fqn)
		if !ok || got != outcome {
			t.Errorf("Lookup(%s) = %q, %v; want %q", fqn, got, ok, outcome)
		}
	}
	if _, ok := set.Lookup("pkg.lattice.results.helper"); ok {
		t.Error("a helper that go test never runs must not join a case")
	}
}

func TestResolveKeepsFQNsUnique(t *testing.T) {
	a := parse(t, "a/open_linux.go", "package a\n\ntype T struct{}\n\nfunc (T) M() {}\n\nfunc Open() {}\n")
	b := parse(t, "a/open_windows.go", "package a\n\ntype T struct{}\n\nfunc (T) M() {}\n\nfunc Open() {}\n\nfunc Other() {}\n")
	mods := New().Resolve([]ir.Module{a, b})
	got := byFQN(mods[1])
	for _, fqn := range []string{"a.T@open_windows", "a.T@open_windows.M", "a.Open@open_windows", "a.Other"} {
		if _, ok := got[fqn]; !ok {
			t.Errorf("missing %s; have %v", fqn, keys(got))
		}
	}
	if m := got["a.T@open_windows.M"]; m.EnclosingFQN != "a.T@open_windows" {
		t.Errorf("renamed method enclosing = %q", m.EnclosingFQN)
	}
	if first := byFQN(mods[0]); first["a.Open"].FQN != "a.Open" || first["a.T.M"].FQN != "a.T.M" {
		t.Errorf("first declaration should keep its FQN: %v", keys(first))
	}
}

func TestRenderAnnotationSuggestion(t *testing.T) {
	out, err := New().RenderAnnotationSuggestion(ir.Symbol{}, []adapters.AnnotationSuggestion{
		{Annotation: "feature", Args: []interface{}{"billing.refund"}},
		{Annotation: "enforces_invariant", Args: []interface{}{[]string{"billing.refund:INV-1", "billing.refund:INV-2"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "// @feature billing.refund\n// @enforces billing.refund:INV-1, billing.refund:INV-2\n"
	if out != want {
		t.Errorf("render = %q, want %q", out, want)
	}
	// What it renders parses back to the same annotations.
	mod := parse(t, "a/a.go", "package a\n\n"+out+"func F() {}\n")
	if s := byFQN(mod)["a.F"]; len(s.Annotations) != 2 {
		t.Errorf("round trip = %+v", s.Annotations)
	}
}

func TestNoIndexerOrMutationRunner(t *testing.T) {
	a := New()
	if _, err := a.SCIPIndexerCommand(".", "out.scip"); err != adapters.ErrIndexerNotConfigured {
		t.Errorf("SCIPIndexerCommand err = %v", err)
	}
	if _, err := a.MutationRunnerCommand(".", []string{"a.go"}); err != adapters.ErrMutationRunnerNotConfigured {
		t.Errorf("MutationRunnerCommand err = %v", err)
	}
	if !a.CanParse("x/y.go") || a.CanParse("x/y.gox") {
		t.Error("CanParse")
	}
}
