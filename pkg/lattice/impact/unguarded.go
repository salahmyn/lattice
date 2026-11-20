package impact

// @module-feature impact

import (
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// CallGraph is the static call graph Lattice builds from the adapters'
// references (reach.Graph). Callers returns the symbols with a resolved
// reference to fqn. It is a local interface so impact does not depend on how
// the graph is built.
type CallGraph interface {
	Callers(fqn string) []string
}

// TestCoverage is ingested test coverage (coverage.Index). TestRan reports
// whether a test's own run executed a symbol, and the basis of that answer:
// "per_test" when the test has a report of its own.
type TestCoverage interface {
	TestRan(test, symbol string) (executed bool, basis string)
}

// basisPerTest is the TestCoverage basis of a test's own run.
const basisPerTest = "per_test"

// How a test was found to exercise an unguarded symbol, strongest first.
const (
	// ViaCoverage: the test's own coverage run executed the symbol (a fact).
	ViaCoverage = "coverage"
	// ViaCallGraph: the test's static call graph reaches the symbol (an
	// upper bound: the call may never run).
	ViaCallGraph = "call_graph"
	// ViaHeuristic: the test file sits next to the symbol's file with the
	// same base name (a guess).
	ViaHeuristic = "heuristic"
)

// ReasonUncovered is why an unguarded symbol is listed under uncovered.
const ReasonUncovered = "no test is known to exercise this; run the full suite"

// UncoveredSymbol is a touched production symbol no promise guards and no
// evidence finds a test for. It is listed, never dropped.
type UncoveredSymbol struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Lines  string `json:"lines,omitempty"`
	Reason string `json:"reason"`
}

// unguardedSymbols are the touched production symbols no promise guards: no
// enforcer span covers them and no checked reference (a BRD criterion's
// enforced_by) names them. These are the lines lattice check reports as
// untraced or unguarded.
func unguardedSymbols(kg schema.KnowledgeGraph, rep Report) []ChangedSymbol {
	referenced := map[string]bool{}
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			for _, ref := range sc.EnforcedBy {
				referenced[ref] = true
			}
		}
	}
	var out []ChangedSymbol
	for _, cs := range rep.ChangedSymbols {
		if cs.Test || len(cs.Enforces) > 0 || referenced[cs.Symbol] || (cs.OldSymbol != "" && referenced[cs.OldSymbol]) {
			continue
		}
		out = append(out, cs)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Symbol < out[j].Symbol
	})
	return out
}

// testCases are the graph's tests that a test runner runs and reports by
// name. The graph's tests also hold every other symbol of a test file (a
// fixture builder, a fake class and its methods, a date helper): those are
// helpers, and the test listing never names one as a test. A test is
//   - a symbol tagged @verifies;
//   - a test-framework case, which the adapters name <module>.test.<title>
//     (test() and it() in TypeScript and JavaScript, a pgTAP assertion);
//   - in Go, a TestXxx, BenchmarkXxx, FuzzXxx or ExampleXxx function or
//     suite method, or a t.Run subtest of one;
//   - in Python and PHP, a function or method whose name starts with test.
//
// @enforces impact:INV-9
func testCases(tests []schema.GraphSymbol) map[string]bool {
	byFQN := make(map[string]schema.GraphSymbol, len(tests))
	for _, t := range tests {
		byFQN[t.FQN] = t
	}
	memo := make(map[string]bool, len(tests))
	var isCase func(t schema.GraphSymbol, depth int) bool
	isCase = func(t schema.GraphSymbol, depth int) bool {
		if v, ok := memo[t.FQN]; ok {
			return v
		}
		v := false
		switch {
		case len(t.Verifies) > 0:
			v = true
		case strings.HasPrefix(t.FQN, moduleOf(t.File)+".test."):
			v = true
		case t.Language == "go":
			parent, ok := byFQN[t.EnclosingFQN]
			switch {
			case t.Kind == "function" && t.EnclosingFQN == "", t.Kind == "method":
				v = isGoTestName(t.Name)
			case t.Kind == "function" && ok && depth < maxSubtestDepth:
				v = isCase(parent, depth+1) // a t.Run subtest
			}
		case t.Language == "python" || t.Language == "php":
			v = (t.Kind == "function" || t.Kind == "method") && strings.HasPrefix(t.Name, "test")
		}
		memo[t.FQN] = v
		return v
	}
	out := make(map[string]bool, len(tests))
	for _, t := range tests {
		if isCase(t, 0) {
			out[t.FQN] = true
		}
	}
	return out
}

// maxSubtestDepth bounds the walk from a subtest up to its test.
const maxSubtestDepth = 64

// moduleOf is the module path the adapters prefix a file's symbols with:
// the path without its extension, dotted (e2e/pay.spec.ts -> e2e.pay.spec).
func moduleOf(file string) string {
	return strings.ReplaceAll(strings.TrimSuffix(file, path.Ext(file)), "/", ".")
}

// isGoTestName reports whether a function name is one go test runs and
// reports by name: TestXxx, BenchmarkXxx, FuzzXxx or ExampleXxx, where Xxx
// does not start with a lower-case letter.
func isGoTestName(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := name[len(prefix):]
		if rest == "" {
			return true
		}
		r, _ := utf8.DecodeRuneInString(rest)
		return !unicode.IsLower(r)
	}
	return false
}

// unguardedTests finds, for each touched symbol no promise guards, the tests
// that exercise it, on the strongest evidence available for that symbol:
// per-test coverage, else the static call graph, else a test file next to
// its file, else, as hints, a test file that changed with its file
// (co_change), else one that mentions its name (mentions). Only test cases
// (see testCases) are found: a helper the call graph passes through is
// walked, never listed. It returns the tests found with why each is
// selected, the symbols only a hint found tests for, and the symbols no
// source finds a test for. hs may be nil: no hints.
// @enforces impact:INV-7
// @enforces impact:INV-8
// @enforces impact:INV-9
func unguardedTests(in Input, pm pathMap, rep Report, hs *hintSource, cases map[string]bool) (map[string][]string, []HintedSymbol, []UncoveredSymbol) {
	kg := in.Graph
	why := map[string][]string{}
	hinted := []HintedSymbol{}
	uncovered := []UncoveredSymbol{}
	fileOf := map[string]string{}
	for _, t := range kg.Tests {
		fileOf[t.FQN] = t.File
	}
	for _, cs := range unguardedSymbols(kg, rep) {
		lines := cs.Lines
		if lines == "" {
			lines = cs.OldLines
		}
		via, found := ViaCoverage, byCoverage(in.Coverage, kg, cases, cs)
		if len(found) == 0 {
			via, found = ViaCallGraph, byCallGraph(in.Calls, cases, cs)
		}
		if len(found) == 0 {
			via, found = ViaHeuristic, byNeighbour(kg, pm, cases, cs)
		}
		var evidence map[string]string
		if len(found) == 0 && hs != nil {
			via = ViaCoChange
			found, evidence = hs.byCoChange(cs)
			if len(found) == 0 {
				via = ViaMentions
				found, evidence = hs.byMention(cs)
			}
		}
		if len(found) == 0 {
			uncovered = append(uncovered, UncoveredSymbol{Symbol: cs.Symbol, File: cs.File, Lines: lines, Reason: ReasonUncovered})
			continue
		}
		if evidence != nil {
			var files []string
			for _, t := range found {
				files = appendNew(files, pm.toGit(fileOf[t]))
			}
			sort.Strings(files)
			hinted = append(hinted, HintedSymbol{Symbol: cs.Symbol, File: cs.File, Lines: lines, Via: via, Files: files, Tests: found})
		}
		for _, t := range found {
			reason := "unguarded " + cs.Symbol + " via " + via
			if ev := evidence[t]; ev != "" {
				reason += " (" + ev + ")"
			}
			why[t] = appendNew(why[t], reason)
		}
	}
	return why, hinted, uncovered
}

// byCoverage lists the tests whose own coverage run executed the symbol.
func byCoverage(cov TestCoverage, kg schema.KnowledgeGraph, cases map[string]bool, cs ChangedSymbol) []string {
	if cov == nil {
		return nil
	}
	var out []string
	for _, t := range kg.Tests {
		if !cases[t.FQN] {
			continue
		}
		for _, s := range []string{cs.Symbol, cs.OldSymbol} {
			if s == "" {
				continue
			}
			if ran, basis := cov.TestRan(t.FQN, s); ran && basis == basisPerTest {
				out = appendNew(out, t.FQN)
			}
		}
	}
	return out
}

// byCallGraph lists the tests whose static call graph reaches the symbol:
// it walks the resolved references backwards from the symbol, through the
// helpers of test files too, and lists only the test cases it meets.
func byCallGraph(g CallGraph, cases map[string]bool, cs ChangedSymbol) []string {
	if g == nil {
		return nil
	}
	seen := map[string]bool{cs.Symbol: true}
	queue := []string{cs.Symbol}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range g.Callers(cur) {
			if seen[c] {
				continue
			}
			seen[c] = true
			if cases[c] {
				out = append(out, c)
			}
			queue = append(queue, c)
		}
	}
	sort.Strings(out)
	return out
}

// byNeighbour lists the tests of the test files next to the symbol's file:
// in its directory (or a __tests__ directory beside it) with the same base
// name, such as fees.test.ts, fees.spec.tsx, fees_test.go or test_fees.py
// for fees.ts, fees.go or fees.py.
func byNeighbour(kg schema.KnowledgeGraph, pm pathMap, cases map[string]bool, cs ChangedSymbol) []string {
	gp, _, ok := pm.toGraph(cs.File)
	if !ok {
		gp = cs.File
	}
	dir, stem := path.Dir(gp), baseStem(gp)
	var out []string
	for _, t := range kg.Tests {
		td := path.Dir(t.File)
		if cases[t.FQN] && (td == dir || td == path.Join(dir, "__tests__")) && baseStem(t.File) == stem && t.File != gp {
			out = appendNew(out, t.FQN)
		}
	}
	sort.Strings(out)
	return out
}

// baseStem is a file's name without its extension and test marker.
func baseStem(p string) string {
	b := path.Base(p)
	if i := strings.IndexByte(b, '.'); i > 0 {
		rest := b[i:]
		b = b[:i]
		if strings.HasPrefix(rest, ".test.") || strings.HasPrefix(rest, ".spec.") {
			return b
		}
	}
	b = strings.TrimSuffix(b, "_test")
	return strings.TrimPrefix(b, "test_")
}
