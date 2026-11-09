package breaktest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

func sel(test, file, lang string) results.Selector {
	return results.Selector{Test: test, Name: test, File: file, Language: lang}
}

// Verifiers are grouped by the first runner whose glob covers their file, the
// language's command template comes next, Go is built in, and a verifier no
// runner covers is returned apart.
// @verifies breaktest:INV-6
func TestGroupsByRunner(t *testing.T) {
	cfg := config.Config{Break: config.Break{
		Runners: []config.Runner{
			{Name: "deno", Match: []string{"supabase/**/*_test.ts"}, Command: []string{"deno", "test", "{files}"}},
			{Name: "vitest", Match: []string{"apps/**/*.test.ts"}, Command: []string{"vitest", "{files}"}},
		},
		Commands: map[string]string{"python": "pytest {files}"},
	}}
	r := &runner{opts: Options{Config: cfg}}
	groups, none := r.groups([]results.Selector{
		sel("a", "apps/web/a.test.ts", "typescript"),
		sel("b", "supabase/fn/b_test.ts", "typescript"),
		sel("c", "apps/web/c.test.ts", "typescript"),
		sel("d", "tests/d.py", "python"),
		sel("e", "pkg/e_test.go", "go"),
		sel("f", "e2e/f.spec.ts", "typescript"),
	})
	var got []string
	for _, g := range groups {
		var names []string
		for _, s := range g.tests {
			names = append(names, s.Test)
		}
		got = append(got, g.kind.String()+":"+g.name+"="+strings.Join(names, ","))
	}
	want := []string{"go:go=e", "runner:deno=b", "runner:vitest=a,c", "shell:python=d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
	if len(none) != 1 || none[0].Test != "f" {
		t.Errorf("no runner for %+v, want only f", none)
	}
}

// A promise is killed when any group fails; no tests executed at all is an
// error, never a pass; a runtime failure outranks a build failure.
// @verifies breaktest:INV-6
// @verifies breaktest:INV-7
func TestCombine(t *testing.T) {
	ok := func(ran ...string) execResult { return execResult{status: statusOK, ran: ran, executed: len(ran)} }
	fail := execResult{status: statusFail, reason: "x", failed: []string{"b > it fails"}, ran: []string{"b"}, executed: 1}

	if got := combine([]execResult{ok("a"), fail}); got.status != statusFail || got.reason != "failed: b > it fails" || !reflect.DeepEqual(got.ran, []string{"a", "b"}) {
		t.Errorf("one failing group: %+v", got)
	}
	if got := combine([]execResult{ok("a"), ok("b")}); got.status != statusOK || len(got.ran) != 2 {
		t.Errorf("both pass: %+v", got)
	}
	// One group ran nothing, the other ran: partial, not an error.
	if got := combine([]execResult{ok(), ok("b")}); got.status != statusOK || !reflect.DeepEqual(got.ran, []string{"b"}) {
		t.Errorf("one group empty: %+v", got)
	}
	// Nothing ran anywhere.
	if got := combine([]execResult{ok(), ok()}); got.status != statusError || !strings.HasPrefix(got.reason, "the filter matched no tests") {
		t.Errorf("no tests: %+v", got)
	}
	build := execResult{status: statusBuild, reason: "does not compile: x"}
	if got := combine([]execResult{build, fail}); got.status != statusFail {
		t.Errorf("a failing test must outrank a build failure: %+v", got)
	}
	if got := combine([]execResult{ok("a"), build}); got.status != statusBuild {
		t.Errorf("a build failure in any group: %+v", got)
	}
	if got := combine([]execResult{fail, {status: statusTimeout, reason: "timed out"}}); got.status != statusTimeout {
		t.Errorf("a timeout outranks: %+v", got)
	}
}

// With {junit} the tests that executed come from the report: a verifier the
// runner's filter selected nothing for is not run, and a skipped one is not
// either. Without it the count cannot be checked.
// @verifies breaktest:INV-7
func TestAccount(t *testing.T) {
	const report = `<testsuites>
  <testsuite name="a">
    <testcase classname="web/a.test.ts" name="suite &gt; keeps the total"/>
    <testcase classname="web/a.test.ts" name="suite &gt; rejects zero"><failure message="boom"/></testcase>
    <testcase classname="web/a.test.ts" name="suite &gt; is skipped"><skipped/></testcase>
  </testsuite>
</testsuites>`
	dir := t.TempDir()
	junit := filepath.Join(dir, "r.xml")
	if err := os.WriteFile(junit, []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	g := group{tests: []results.Selector{
		sel("web/a.test.keeps-the-total", "web/a.test.ts", "typescript"),
		sel("web/a.test.rejects-zero", "web/a.test.ts", "typescript"),
		sel("web/a.test.is-skipped", "web/a.test.ts", "typescript"),
		sel("web/a.test.missing-one", "web/a.test.ts", "typescript"),
	}}
	got := account(g, execResult{status: statusFail, reason: "line"}, junit)
	wantRan := []string{"web/a.test.keeps-the-total", "web/a.test.rejects-zero"}
	if !reflect.DeepEqual(got.ran, wantRan) || got.executed != 2 || got.unchecked {
		t.Errorf("ran = %v executed %d unchecked %v, want %v", got.ran, got.executed, got.unchecked, wantRan)
	}
	if !reflect.DeepEqual(got.failed, []string{"web/a.test.ts::suite > rejects zero"}) || !strings.HasPrefix(got.reason, "failed: ") {
		t.Errorf("failed = %v reason %q", got.failed, got.reason)
	}
	// A passing run that wrote no report cannot be trusted to have run tests.
	if got := account(g, execResult{status: statusOK}, filepath.Join(dir, "none.xml")); got.status != statusError {
		t.Errorf("no report: %+v", got)
	}
	// No {junit}: every verifier is taken as run, and the result says so.
	got = account(g, execResult{status: statusOK}, "")
	if !got.unchecked || len(got.ran) != 4 {
		t.Errorf("unchecked: %+v", got)
	}
}

// The reason for a failure is a test name or an error line, never a lone
// bracket.
// @verifies breaktest:INV-3
func TestFailingLine(t *testing.T) {
	for _, c := range []struct{ out, want string }{
		{"  expect(x).toBe(1)\n}\n", "expect(x).toBe(1)"},
		{"some output\nAssertionError: expected 1 to be 2\n    at f\n}", "AssertionError: expected 1 to be 2"},
		{"--- FAIL: TestX (0.00s)\n}\n", "--- FAIL: TestX (0.00s)"},
		{"{\n}\n", "the runner failed with no output"},
		{"", "the runner failed with no output"},
	} {
		if got := failingLine(c.out); got != c.want {
			t.Errorf("failingLine(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

// Runner commands are argument lists: {files} expands to one argument per
// file, {filter} and {junit} are replaced in place, and a runner that sets
// dir gets paths relative to it.
// @verifies breaktest:INV-6
func TestExpandArgv(t *testing.T) {
	got := expandArgv([]string{"vitest", "run", "--reporter=junit", "--outputFile={junit}", "-t", "{filter}", "{files}"},
		[]string{"a.test.ts", "b.test.ts"}, "(x y)$", "/tmp/j.xml")
	want := []string{"vitest", "run", "--reporter=junit", "--outputFile=/tmp/j.xml", "-t", "(x y)$", "a.test.ts", "b.test.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v", got)
	}
	rel := relativeTo("/wt", "apps/client", []string{"apps/client/src/a.test.ts", "other/b.test.ts"})
	if rel[0] != "src/a.test.ts" || rel[1] != filepath.Join("/wt", "other", "b.test.ts") {
		t.Errorf("relativeTo = %v", rel)
	}
}

// A changed runner, setup command, link setting or generator version changes
// the configuration hash that keys the cache.
// @verifies breaktest:INV-5
func TestConfigHash(t *testing.T) {
	base := config.Break{Runners: []config.Runner{{Name: "u", Match: []string{"**/*.test.js"}, Command: []string{"node", "--test", "{files}"}}}}
	sels := []results.Selector{sel("a", "x/a.test.js", "javascript")}
	hash := func(b config.Break, noRunner ...results.Selector) string {
		r := &runner{opts: Options{Config: config.Config{Break: b}}}
		groups, _ := r.groups(sels)
		return r.configHash(groups, noRunner)
	}
	h := hash(base)
	if h != hash(base) {
		t.Error("the hash is not stable")
	}
	off := false
	for name, mut := range map[string]func(*config.Break){
		"command": func(b *config.Break) {
			b.Runners = []config.Runner{{Name: "u", Match: b.Runners[0].Match, Command: []string{"node", "--test", "--x", "{files}"}}}
		},
		"match": func(b *config.Break) {
			b.Runners = []config.Runner{{Name: "u", Match: []string{"x/**"}, Command: b.Runners[0].Command}}
		},
		"dir": func(b *config.Break) {
			b.Runners = []config.Runner{{Name: "u", Match: b.Runners[0].Match, Command: b.Runners[0].Command, Dir: "x"}}
		},
		"setup": func(b *config.Break) { b.Setup = []string{"npm ci"} },
		"link":  func(b *config.Break) { b.LinkNodeModules = &off },
	} {
		b := base
		mut(&b)
		if hash(b) == h {
			t.Errorf("a changed %s must change the hash", name)
		}
	}
	if hash(base, sel("z", "y/z.test.ts", "typescript")) == h {
		t.Error("a verifier with no runner must change the hash")
	}
}

// Every node_modules directory of the main checkout is linked at the same
// relative path of the worktree; what is already there is left alone, and
// nothing inside a node_modules is searched.
// @verifies breaktest:INV-8
func TestLinkNodeModules(t *testing.T) {
	top, root := t.TempDir(), t.TempDir()
	for _, p := range []string{"node_modules/pkg/index.js", "apps/web/node_modules/dep/index.js",
		"node_modules/pkg/node_modules/inner/x.js", "apps/api/src/a.js", "kept/node_modules/m.js"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(top, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(top, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "apps", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "kept", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := linkNodeModules(top, root); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"node_modules", "apps/web/node_modules"} {
		got, err := os.Readlink(filepath.Join(root, link))
		if err != nil || got != filepath.Join(top, link) {
			t.Errorf("%s: link = %q, %v", link, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules", "pkg", "index.js")); err != nil {
		t.Errorf("the link does not reach the files: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(root, "kept", "node_modules")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("an existing directory was replaced: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "apps", "api")); err == nil {
		t.Error("a directory with no node_modules was created")
	}
}
