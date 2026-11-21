package breaktest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/lines"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func linesGroups() map[string]group {
	rn := config.Runner{Name: "vt", Match: []string{"web/**"}, Dir: "web",
		Command: []string{"vitest", "run", "--outputFile={junit}", "--coverage.reportsDirectory={coverage}", "-t", "{filter}", "{files}"}}
	plainRn := config.Runner{Name: "plain", Match: []string{"web/**"}, Command: []string{"vitest", "run", "-t", "{filter}", "{files}"}}
	ws := sel("web.a.x", "web/a.test.ts", "typescript")
	return map[string]group{
		"cov":   {name: "vt", kind: kindRunner, runner: rn, language: "typescript", tests: []results.Selector{ws}},
		"plain": {name: "plain", kind: kindRunner, runner: plainRn, language: "typescript", tests: []results.Selector{ws}},
		"shell": {name: "python", kind: kindShell, tmpl: "coverage run -m pytest {files} && coverage lcov -o {coverage}", language: "python", tests: []results.Selector{sel("t.test_a", "tests/test_a.py", "python")}},
		"go":    {name: "go", kind: kindGo, language: "go", tests: []results.Selector{sel("pkg.TestA", "pkg/a_test.go", "go")}},
		"pgtap": {name: "pg", kind: kindShell, tmpl: "pg_prove {files}", language: "sql", tests: []results.Selector{sel("db.t", "db/t.sql", "postgres")}},
	}
}

// A key made without --lines is byte for byte the key this package made before
// --lines existed (a frozen copy of its struct), so no plain cached baseline
// is invalidated; with it, a group that runs with coverage gets another key,
// and one that does not keeps its own.
// @verifies breaktest:INV-13
func TestLinesKeysLeavePlainOnesAlone(t *testing.T) {
	type oldKey struct {
		Kind     string
		Name     string
		Runner   *config.Runner `json:",omitempty"`
		Template string         `json:",omitempty"`
		Dir      string         `json:",omitempty"`
		Argv     []string       `json:",omitempty"`
		Tests    []string       `json:",omitempty"`
	}
	frozen := func(g group) string {
		d := oldKey{Kind: g.kind.String(), Name: g.name}
		switch g.kind {
		case kindGo:
			for _, tt := range g.tests {
				d.Tests = append(d.Tests, tt.Test)
			}
		case kindRunner:
			rn := g.runner
			d.Runner = &rn
			d.Dir, d.Argv = render(g, worktreeToken, junitToken, keySQL)
		default:
			d.Template = g.tmpl
			d.Dir, d.Argv = render(g, worktreeToken, junitToken, keySQL)
		}
		b, _ := json.Marshal(d)
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	for name, g := range linesGroups() {
		if got, want := invocationKey(g), frozen(g); got != want {
			t.Errorf("%s: plain key = %s, want the pre-lines key %s", name, got, want)
		}
		if invocationKeyFor(g, false) != invocationKey(g) {
			t.Errorf("%s: a key made with lines off differs from the plain one", name)
		}
		r := &runner{opts: Options{Lines: true}}
		if differs := r.keyOf(g) != invocationKey(g); g.coversLines() != differs {
			t.Errorf("%s: coversLines %v but the --lines key differs %v", name, g.coversLines(), differs)
		}
		if (&runner{}).keyOf(g) != invocationKey(g) {
			t.Errorf("%s: without --lines the key must be the plain one", name)
		}
	}
	gs := linesGroups()
	for name, want := range map[string]bool{"cov": true, "shell": true, "go": true, "plain": false, "pgtap": false} {
		if got := gs[name].coversLines(); got != want {
			t.Errorf("%s: coversLines = %v, want %v", name, got, want)
		}
	}
}

// The command a runner or a template is given renders exactly as before when
// it has no {coverage}, and {coverage} is filled with a path (and left as
// written in the invocation key, which must not name a temporary file).
// @verifies breaktest:INV-13
func TestCoveragePlaceholderRenders(t *testing.T) {
	gs := linesGroups()
	_, plain := render(gs["plain"], "/wt", "/tmp/j.xml", sqlFiles{})
	_, withCov := renderCov(gs["plain"], "/wt", "/tmp/j.xml", "/tmp/cov", sqlFiles{})
	if !reflect.DeepEqual(plain, withCov) {
		t.Errorf("a command with no {coverage} renders differently: %v vs %v", plain, withCov)
	}
	if !reflect.DeepEqual(expandArgv([]string{"x", "{files}", "{junit}"}, []string{"a"}, "f", "j"),
		expandArgvCov([]string{"x", "{files}", "{junit}"}, []string{"a"}, "f", "j", "/c", sqlFiles{})) {
		t.Errorf("expandArgv changed")
	}

	dir, argv := renderCov(gs["cov"], "/wt", "/tmp/j.xml", "/tmp/cov-1", sqlFiles{})
	if dir != filepath.Join("/wt", "web") || !has(argv, "--coverage.reportsDirectory=/tmp/cov-1") || !has(argv, "--outputFile=/tmp/j.xml") {
		t.Errorf("runner: dir %s argv %v", dir, argv)
	}
	_, argv = renderCov(gs["shell"], "/wt", "", "/tmp/a b/cov", sqlFiles{})
	if !strings.Contains(argv[2], "coverage lcov -o '/tmp/a b/cov'") {
		t.Errorf("template: %v", argv)
	}
	_, key := render(gs["cov"], worktreeToken, junitToken, keySQL)
	if !has(key, "--coverage.reportsDirectory={coverage}") {
		t.Errorf("the key must name the placeholder, got %v", key)
	}
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// A tool that can only write into a directory is given {coverage} as one and
// its single coverage report is read (HTML in a subdirectory is not); several
// reports, none, or a report that is not coverage read unknown with the
// reason, and the lines of a file outside the workspace are dropped.
// @verifies audit:INV-10
func TestReadCoverageDirectoryOrFile(t *testing.T) {
	const lcov = "TN:\nSF:src/a.ts\nDA:2,1\nDA:3,0\nend_of_record\nSF:node_modules/x/i.js\nDA:1,1\nend_of_record\n"
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{{FQN: "a", File: "src/a.ts", Line: 1, EndLine: 4}}}
	r := newLinesRunner(kg)

	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "lcov-report"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "lcov-report", "index.html"), []byte("<html>"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "lcov.info"), []byte(lcov), 0o644))
	inv := r.readCoverage(dir, "", nil)
	if inv.Unknown != "" || len(inv.Files) != 1 || inv.Files["src/a.ts"].Instrumented != "2-3" || inv.Files["src/a.ts"].Hit != "2" {
		t.Errorf("directory: %+v", inv)
	}

	file := filepath.Join(t.TempDir(), "cov.lcov")
	must(t, os.WriteFile(file, []byte(lcov), 0o644))
	if inv := r.readCoverage(file, "", nil); inv.Files["src/a.ts"].Hit != "2" {
		t.Errorf("file: %+v", inv)
	}

	// A root: a report relative to the runner's directory.
	rel := filepath.Join(t.TempDir(), "rel.lcov")
	must(t, os.WriteFile(rel, []byte("SF:a.ts\nDA:2,1\nend_of_record\n"), 0o644))
	rkg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{{FQN: "a", File: "web/a.ts", Line: 1, EndLine: 4}}}
	if inv := newLinesRunner(rkg).readCoverage(rel, "web", nil); inv.Files["web/a.ts"].Hit != "2" {
		t.Errorf("relative to the runner dir: %+v", inv)
	}

	two := t.TempDir()
	must(t, os.WriteFile(filepath.Join(two, "a.info"), []byte(lcov), 0o644))
	must(t, os.WriteFile(filepath.Join(two, "b.info"), []byte(lcov), 0o644))
	if inv := r.readCoverage(two, "", nil); !strings.Contains(inv.Unknown, "several coverage reports") || inv.Files != nil {
		t.Errorf("two reports: %+v", inv)
	}
	if inv := r.readCoverage(t.TempDir(), "", nil); inv.Unknown != whyNoReport {
		t.Errorf("empty directory: %+v", inv)
	}
	if inv := r.readCoverage(filepath.Join(dir, "absent"), "", nil); inv.Unknown != whyNoReport {
		t.Errorf("missing path: %+v", inv)
	}
	junk := filepath.Join(t.TempDir(), "junk")
	must(t, os.WriteFile(junk, []byte("<testsuites/>"), 0o644))
	if inv := r.readCoverage(junk, "", nil); !strings.Contains(inv.Unknown, "could not be parsed") {
		t.Errorf("not coverage: %+v", inv)
	}
	other := filepath.Join(t.TempDir(), "o.lcov")
	must(t, os.WriteFile(other, []byte("SF:elsewhere.ts\nDA:1,1\nend_of_record\n"), 0o644))
	if inv := r.readCoverage(other, "", nil); inv.Unknown != whyNoFiles {
		t.Errorf("a file outside the workspace: %+v", inv)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Go's import paths join the module's directory.
func TestGoResolverMapsImportPaths(t *testing.T) {
	res := goResolver("example.com/svc", "svc")
	if got := res("example.com/svc/pkg/a.go"); got != "svc/pkg/a.go" {
		t.Errorf("module in a subdirectory: %q", got)
	}
	if got := goResolver("example.com/svc", ".")("example.com/svc/a.go"); got != "a.go" {
		t.Errorf("module at the root: %q", got)
	}
	if res("other.org/x/a.go") != "" || goResolver("", ".") != nil {
		t.Errorf("a path outside the module, or no module path, must not map")
	}
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// c\nmodule \"example.com/q\"\n\ngo 1.21\n"), 0o644))
	if goModulePath(dir) != "example.com/q" {
		t.Errorf("module path = %q", goModulePath(dir))
	}
}

// A break that targets one expression records exactly the lines it edits; an
// early return, which skips a whole body, records no line and is whole, so a
// kill credits no single line. Run carries both as optional additions.
// @verifies audit:INV-10
func TestEditLines(t *testing.T) {
	src := []byte("package p\n\nfunc F() int {\n\treturn 1\n}\n")
	brk, err := Generate("go", "p.go", src, Symbol{Name: "F", Kind: "function", Line: 3})
	must(t, err)
	plan := breakPlan{edits: []fileEdit{{file: "p.go", src: src, brk: brk, line: 3}}}
	enf := schema.GraphSymbol{File: "p.go", Line: 3, EndLine: 5, Language: "go"}
	if got, whole := editLines(plan, enf); got != nil || !whole {
		t.Errorf("early return = %v whole %v, want no lines and whole", got, whole)
	}
	policy := []byte("create policy p on t\n  using (a = 1\n  and b = 2);\n")
	pe := breakPlan{edits: []fileEdit{{file: "m.sql", src: policy,
		brk: Break{Offset: 28, Text: "x", More: []Break{{Offset: 44, Text: "y"}}}}}}
	if got, whole := editLines(pe, schema.GraphSymbol{File: "m.sql", Line: 1, Language: "postgres"}); !reflect.DeepEqual(got, []int{2, 3}) || whole {
		t.Errorf("SQL expression edit = %v whole %v, want exactly lines [2 3]", got, whole)
	}
	fn := []byte("create or replace function f() returns int language sql as $$ select 1 $$;\n")
	fe := breakPlan{edits: []fileEdit{{file: "m.sql", src: fn, brk: Break{Offset: 5, Text: "x"}}}}
	if got, whole := editLines(fe, schema.GraphSymbol{File: "m.sql", Line: 1, Language: "postgres"}); got != nil || !whole {
		t.Errorf("SQL function early return = %v whole %v", got, whole)
	}
	if got, whole := editLines(breakPlan{}, enf); got != nil || whole {
		t.Errorf("no edit has no lines")
	}

	with, _ := json.Marshal(Run{Promise: "p", Lines: []int{3, 5}, Whole: true})
	without, _ := json.Marshal(Run{Promise: "p"})
	if !strings.Contains(string(with), `"lines":[3,5]`) || !strings.Contains(string(with), `"whole":true`) ||
		strings.Contains(string(without), `"lines"`) || strings.Contains(string(without), `"whole"`) {
		t.Errorf("lines and whole must be optional additions: %s / %s", with, without)
	}
	var old Report
	must(t, json.Unmarshal([]byte(`{"schema":"lattice.breaks/1","runs":[{"promise":"p","enforcer":"e","outcome":"killed"}]}`), &old))
	if len(old.Runs) != 1 || old.Runs[0].Lines != nil || old.Runs[0].Whole {
		t.Errorf("old report: %+v", old)
	}
}

// Whether an invocation ran another test is read from its JUnit report: a
// test no verifier names makes it shared; with no report nothing says, and it
// is shared; a Go group is shared only when a verifier is a subtest.
// @verifies audit:INV-10
func TestSharedOf(t *testing.T) {
	v := sel("suite.checks.rejects-empty", "web/a.test.ts", "typescript")
	g := group{kind: kindRunner, name: "r", tests: []results.Selector{v}}
	junit := func(names ...string) invocation {
		xml := "<testsuites><testsuite>"
		for _, n := range names {
			xml += `<testcase classname="test" name="` + n + `"/>`
		}
		xml += "</testsuite></testsuites>"
		return invocation{res: execResult{status: statusOK}, junit: true, report: []byte(xml)}
	}
	if _, shared, others, n := sharedOf(g, junit("rejects empty")); shared || n != 0 || others != nil {
		t.Errorf("only the verifier ran: shared %v others %v", shared, others)
	}
	ran, shared, others, n := sharedOf(g, junit("rejects empty", "logger smoke test"))
	if !shared || n != 1 || !reflect.DeepEqual(others, []string{"test::logger smoke test"}) || len(ran) != 1 {
		t.Errorf("another test ran: ran %v shared %v others %v/%d", ran, shared, others, n)
	}
	if _, shared, _, _ := sharedOf(g, invocation{res: execResult{status: statusOK}}); !shared {
		t.Errorf("without {junit} an invocation must count as shared")
	}
	goTop := group{kind: kindGo, name: "go", tests: []results.Selector{{Test: "pkg.TestA", Filters: map[string][]string{"go": {"-run", "^TestA$"}}}}}
	goSub := group{kind: kindGo, name: "go", tests: []results.Selector{{Test: "pkg.TestA.sub", Filters: map[string][]string{"go": {"-run", "^TestA$/^sub$"}}}}}
	if _, shared, _, _ := sharedOf(goTop, invocation{}); shared {
		t.Errorf("a top-level Go test runs alone")
	}
	if _, shared, _, _ := sharedOf(goSub, invocation{}); !shared {
		t.Errorf("a Go subtest runs with its siblings")
	}
}

// newLinesRunner is a runner that can map a report onto a graph.
func newLinesRunner(kg schema.KnowledgeGraph) *runner {
	return &runner{paths: coverage.NewPathMapper(kg), src: lines.SourceFiles(kg)}
}
