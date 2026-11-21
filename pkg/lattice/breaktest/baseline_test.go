package breaktest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// The key is the command as rendered: a whole-file runner renders the same
// command for different tests of one file, a {filter} runner does not, and
// the runner, the template or a Go group's verifiers tell groups apart.
// @verifies breaktest:INV-13
func TestInvocationKey(t *testing.T) {
	files := config.Runner{Name: "pgtap", Match: []string{"db/**"}, Command: []string{"sh", "-c", "reset && pg_prove \"$@\"", "sh", "{files}"}}
	filter := config.Runner{Name: "vitest", Match: []string{"web/**"}, Command: []string{"vitest", "--outputFile={junit}", "-t", "{filter}", "{files}"}}
	runnerGroup := func(rn config.Runner, tests ...results.Selector) group {
		return group{name: rn.Name, kind: kindRunner, runner: rn, language: tests[0].Language, tests: tests}
	}
	a, b := sel("db.orders.a", "db/orders.sql", "sql"), sel("db.orders.b", "db/orders.sql", "sql")
	other := sel("db.fees.c", "db/fees.sql", "sql")
	if invocationKey(runnerGroup(files, a)) != invocationKey(runnerGroup(files, b)) {
		t.Error("two tests of one file render the same whole-file command")
	}
	if invocationKey(runnerGroup(files, a)) == invocationKey(runnerGroup(files, other)) {
		t.Error("another file is another command")
	}
	wa, wb := sel("web.a.x", "web/a.test.ts", "typescript"), sel("web.a.y", "web/a.test.ts", "typescript")
	if invocationKey(runnerGroup(filter, wa)) == invocationKey(runnerGroup(filter, wb)) {
		t.Error("a {filter} runner renders one command per test")
	}
	if invocationKey(runnerGroup(filter, wa, wb)) != invocationKey(runnerGroup(filter, wb, wa)) {
		t.Error("the key does not depend on the order of the verifiers")
	}
	changed := files
	changed.Command = []string{"sh", "-c", "pg_prove \"$@\"", "sh", "{files}"}
	if invocationKey(runnerGroup(files, a)) == invocationKey(runnerGroup(changed, a)) {
		t.Error("a changed command is another invocation")
	}
	shell := func(tmpl string, tests ...results.Selector) group {
		return group{name: "sql", kind: kindShell, tmpl: tmpl, language: "sql", tests: tests}
	}
	if invocationKey(shell("pg_prove {files}", a)) != invocationKey(shell("pg_prove {files}", b)) {
		t.Error("a whole-file template renders the same command for one file")
	}
	if invocationKey(shell("pg_prove {files}", a)) == invocationKey(shell("pg_prove -v {files}", a)) {
		t.Error("a changed template is another invocation")
	}
	goGroup := func(tests ...results.Selector) group { return group{name: "go", kind: kindGo, tests: tests} }
	ga, gb := sel("pkg.TestA", "pkg/a_test.go", "go"), sel("pkg.TestB", "pkg/a_test.go", "go")
	if invocationKey(goGroup(ga)) == invocationKey(goGroup(gb)) || invocationKey(goGroup(ga, gb)) != invocationKey(goGroup(gb, ga)) {
		t.Error("a Go group is keyed by its verifiers")
	}
}

// Only a pass is kept across runs; a failure or a build failure is shared
// within the run only; a timeout, an error or a passing run with no
// readable report is never reused.
// @verifies breaktest:INV-13
func TestInvocationReuse(t *testing.T) {
	report := []byte(`<testsuites><testsuite name="s"><testcase classname="db/orders.sql" name="a"/></testsuite></testsuites>`)
	for _, c := range []struct {
		name             string
		inv              invocation
		reusable, persis bool
	}{
		{"pass", invocation{res: execResult{status: statusOK}}, true, true},
		{"pass with a report", invocation{res: execResult{status: statusOK}, junit: true, report: report}, true, true},
		{"pass with no report", invocation{res: execResult{status: statusOK}, junit: true, reportErr: errors.New("missing")}, false, false},
		{"pass with a bad report", invocation{res: execResult{status: statusOK}, junit: true, report: []byte("not xml")}, false, false},
		{"fail", invocation{res: execResult{status: statusFail}}, true, false},
		{"build", invocation{res: execResult{status: statusBuild}}, true, false},
		{"timeout", invocation{res: execResult{status: statusTimeout}}, false, false},
		{"error", invocation{res: execResult{status: statusError}}, false, false},
	} {
		if got := c.inv.reusable(); got != c.reusable {
			t.Errorf("%s: reusable = %v", c.name, got)
		}
		if got := c.inv.persistable(); got != c.persis {
			t.Errorf("%s: persistable = %v", c.name, got)
		}
	}
}

// One shared invocation is accounted for each group's own verifiers: a
// verifier the report does not list did not run, and accounting one group
// leaves the invocation as it was for the next.
// @verifies breaktest:INV-7
// @verifies breaktest:INV-13
func TestSharedInvocationAccountsEachGroup(t *testing.T) {
	report := []byte(`<testsuites><testsuite name="s"><testcase classname="db/orders.sql" name="a"/></testsuite></testsuites>`)
	inv := invocation{res: execResult{status: statusOK}, junit: true, report: report}
	ga := group{kind: kindRunner, tests: []results.Selector{sel("db/orders.a", "db/orders.sql", "sql")}}
	gb := group{kind: kindRunner, tests: []results.Selector{sel("db/orders.b", "db/orders.sql", "sql")}}
	if got := inv.result(ga); got.executed != 1 || !reflect.DeepEqual(got.ran, []string{"db/orders.a"}) {
		t.Errorf("a: %+v", got)
	}
	if got := inv.result(gb); got.executed != 0 || len(got.ran) != 0 {
		t.Errorf("b did not run: %+v", got)
	}
	if got := combine([]execResult{inv.result(gb)}); got.status != statusError {
		t.Errorf("a baseline that ran none of b's verifiers is an error: %+v", got)
	}
	if inv.res.ran != nil || inv.res.executed != 0 {
		t.Errorf("accounting changed the invocation: %+v", inv.res)
	}
}

// Kept baselines are read back only for the same tree and stamp; a nil file
// (a dirty tree) neither reads nor keeps any.
// @verifies breaktest:INV-13
func TestBaselineFile(t *testing.T) {
	dir := t.TempDir()
	g := group{kind: kindRunner, runner: config.Runner{Name: "r", Command: []string{"r", "--junit={junit}", "{files}"}},
		tests: []results.Selector{sel("db/orders.a", "db/orders.sql", "sql")}}
	report := []byte(`<testsuites><testsuite name="s"><testcase classname="db/orders.sql" name="a"/></testsuite></testsuites>`)
	f := loadBaselines(dir, "tree1", "stamp1")
	f.put("k", g, invocation{res: execResult{status: statusOK}, junit: true, report: report})
	if err := f.write(dir); err != nil {
		t.Fatal(err)
	}
	got, ok := loadBaselines(dir, "tree1", "stamp1").get("k", g)
	if !ok || !reflect.DeepEqual(got.result(g).ran, []string{"db/orders.a"}) {
		t.Errorf("same tree: %v %+v", ok, got)
	}
	if _, ok := loadBaselines(dir, "tree2", "stamp1").get("k", g); ok {
		t.Error("another tree must not reuse the baseline")
	}
	if _, ok := loadBaselines(dir, "tree1", "stamp2").get("k", g); ok {
		t.Error("another stamp must not reuse the baseline")
	}
	var none *baselineFile
	none.put("k", g, invocation{res: execResult{status: statusOK}})
	if _, ok := none.get("k", g); ok {
		t.Error("a nil file keeps nothing")
	}
	setup := config.Break{Setup: []string{"npm ci"}}
	if baselineStamp("t", "", config.Break{}) == baselineStamp("t", "", setup) || baselineStamp("t", "", setup) == baselineStamp("u", "", setup) {
		t.Error("the stamp covers the tree and the setup commands")
	}
}
