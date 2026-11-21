package breaktest

import (
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// Inputs are declared by every runner group of a run or not at all: a Go
// group, a template or a runner without inputs sends the run back to the tree.
// @verifies breaktest:INV-16
func TestDeclaredInputs(t *testing.T) {
	with := func(in ...string) group {
		return group{kind: kindRunner, runner: config.Runner{Name: "r", Inputs: in}}
	}
	got, ok := declaredInputs([]group{with("db/**", "seed.sql"), with("db/**", "fixtures/**")})
	if !ok || !reflect.DeepEqual(got, []string{"db/**", "fixtures/**", "seed.sql"}) {
		t.Errorf("union = %v %v", got, ok)
	}
	for name, groups := range map[string][]group{
		"no groups":        nil,
		"a go group":       {with("db/**"), {kind: kindGo}},
		"a template":       {with("db/**"), {kind: kindShell, tmpl: "pytest {files}"}},
		"a runner without": {with("db/**"), with()},
	} {
		if _, ok := declaredInputs(groups); ok {
			t.Errorf("%s: inputs are not declared", name)
		}
	}
}

// The tree listing keeps the files under the code root, relative to it.
// @verifies breaktest:INV-16
func TestParseTree(t *testing.T) {
	out := "100644 blob aaa\tREADME.md\x00100644 blob bbb\tapp/db/a b.sql\x00100755 blob ccc\tapp/run.sh\x00160000 commit ddd\tvendor/mod\x00"
	if got := parseTree(out, ""); len(got) != 4 || got[1] != (treeFile{path: "app/db/a b.sql", blob: "bbb"}) {
		t.Errorf("whole tree = %v", got)
	}
	want := []treeFile{{path: "db/a b.sql", blob: "bbb"}, {path: "run.sh", blob: "ccc"}}
	if got := parseTree(out, "app"); !reflect.DeepEqual(got, want) {
		t.Errorf("under app = %v, want %v", got, want)
	}
}

// A failed baseline is kept for the tree and cleared by a pass; a nil file
// (a dirty tree) keeps none, and a failure is never read back as a baseline.
// @verifies breaktest:INV-16
func TestBaselineFileKeepsFailures(t *testing.T) {
	dir := t.TempDir()
	g := group{kind: kindRunner, runner: config.Runner{Name: "r", Command: []string{"r", "{files}"}}}
	f := loadBaselines(dir, "tree1", "stamp1")
	f.putFailed("k", "baseline fails")
	if err := f.write(dir); err != nil {
		t.Fatal(err)
	}
	again := loadBaselines(dir, "tree1", "stamp1")
	if !again.failed("k") {
		t.Error("the failure is kept for the tree")
	}
	if _, ok := again.get("k", g); ok {
		t.Error("a failure is not a baseline to reuse")
	}
	if loadBaselines(dir, "tree2", "stamp1").failed("k") {
		t.Error("another tree does not inherit the failure")
	}
	again.put("k", g, invocation{res: execResult{status: statusOK}})
	if again.failed("k") {
		t.Error("a pass clears the failure")
	}
	var none *baselineFile
	none.putFailed("k", "x")
	if none.failed("k") {
		t.Error("a nil file keeps nothing")
	}
}
