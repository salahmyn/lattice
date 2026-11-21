package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// A fake database is a state file. break.setup writes "good". Each runner
// refuses to start unless the file says "good" (as pg_prove would fail on a
// database that does not match the migrations), fails when the object it was
// given carries the break, and leaves the file "bad" after a broken object
// was applied, because it never applies the original again.
type fakeDB struct {
	state, log string
}

func newFakeDB(t *testing.T) fakeDB {
	t.Helper()
	d := t.TempDir()
	return fakeDB{state: filepath.Join(d, "state"), log: filepath.Join(d, "setups")}
}

func (db fakeDB) setup() string {
	return "  setup:\n    - " + shellYAML("printf good > "+shellQuoteTest(db.state)+"; echo run >> "+shellQuoteTest(db.log)) + "\n"
}

// runner is a swap runner for one pgTAP file; alwaysFail makes its tests fail
// whatever the database holds.
func (db fakeDB) runner(name, file, marker string, alwaysFail bool) string {
	fail := "grep -qF " + shellQuoteTest(marker) + ` "$patched"`
	if alwaysFail {
		fail = "true"
	}
	// One line: a newline in a YAML flow scalar folds into a space.
	script := `state=$1; patched=$2; original=$3; ` +
		`if [ "$(cat "$state")" != good ]; then echo 'not ok 1 - database does not match the migrations'; exit 1; fi; ` +
		`if cmp -s "$patched" "$original"; then printf good > "$state"; else printf bad > "$state"; fi; ` +
		`if ` + fail + `; then echo 'not ok 1 - check'; exit 1; fi; echo 'ok 1 - check'`
	return "    - name: " + name + "\n      match: [\"" + file + "\"]\n      command: [sh, -c, " + shellYAML(script) +
		", sh, " + shellYAML(db.state) + ", \"{patched_sql}\", \"{original_sql}\", \"{files}\"]\n"
}

func (db fakeDB) config(restore string, runners ...string) string {
	return "break:\n" + db.setup() + restore + "  runners:\n" + strings.Join(runners, "")
}

func (db fakeDB) allRunners() []string {
	return []string{
		db.runner("policy", "db/tests/policy_test.sql", "true OR (", false),
		db.runner("trigger", "db/tests/trigger_test.sql", "WHEN (false)", false),
		db.runner("function", "db/tests/function_test.sql", "RETURN COALESCE(NEW, OLD);", false),
	}
}

func (db fakeDB) setups(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(db.log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "run\n")
}

// A runner that leaves the database wrong after a broken run poisons the next
// baseline. With no break.restore, that baseline is retried once after a
// fresh setup, so its promise is killed, not unknown.
// @verifies breaktest:INV-18
func TestBreakSQLSwapHealsBaselineAfterBadRestore(t *testing.T) {
	db := newFakeDB(t)
	dir := sqlFixtureWithConfig(t, db.config("", db.allRunners()...))
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	for _, p := range []string{"orders:INV-1", "orders:INV-2", "orders:INV-3"} {
		r := byPromise(res.doc.Report)[p]
		if r.Outcome != breaktest.Killed || r.Baseline != "pass" {
			t.Errorf("%s: %s, baseline %s (%s), want killed on a passing baseline", p, r.Outcome, r.Baseline, r.Reason)
		}
	}
	if n := db.setups(t); n != 3 {
		t.Errorf("setup ran %d times, want 3: once, and once for each of the two healed baselines", n)
	}
	if res.doc.Summary.SetupRuns != 3 {
		t.Errorf("summary setup_runs = %d, want 3", res.doc.Summary.SetupRuns)
	}
	// The retry is a baseline run, never a reused one.
	if b := res.doc.Summary.Baselines; b.Run != 5 || b.Reused != 0 {
		t.Errorf("baselines: %+v, want 5 run (three, and two retries) and 0 reused", b)
	}
}

// The summary line says how many times setup ran again.
// @verifies breaktest:INV-18
func TestBreakSQLSwapSummaryShowsSetupRuns(t *testing.T) {
	db := newFakeDB(t)
	dir := sqlFixtureWithConfig(t, db.config("", db.allRunners()...))
	res := runBreakExit(t, dir, false)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	if !strings.Contains(res.out, "setup: 1 + 2 after restore") {
		t.Errorf("the summary does not show the setup runs:\n%s", res.out)
	}
}

// A failing break.restore runs setup again right after that run, so the next
// baseline finds a good database and no heal is needed. The run's outcome is
// the one its verifiers gave.
// @verifies breaktest:INV-18
func TestBreakSQLSwapFailedRestoreRerunsSetup(t *testing.T) {
	db := newFakeDB(t)
	restore := "  restore:\n    - " + shellYAML(`[ "$(cat `+shellQuoteTest(db.state)+`)" = good ]`) + "\n"
	dir := sqlFixtureWithConfig(t, db.config(restore, db.allRunners()...))
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	for _, p := range []string{"orders:INV-1", "orders:INV-2", "orders:INV-3"} {
		if r := byPromise(res.doc.Report)[p]; r.Outcome != breaktest.Killed || r.Baseline != "pass" {
			t.Errorf("%s: %s, baseline %s (%s), want killed", p, r.Outcome, r.Baseline, r.Reason)
		}
	}
	// The first setup, and one after each broken run left the database bad.
	if n := db.setups(t); n != 4 {
		t.Errorf("setup ran %d times, want 4", n)
	}
	if b := res.doc.Summary.Baselines; b.Run != 3 {
		t.Errorf("baselines run = %d, want 3: the restore made every retry unnecessary", b.Run)
	}
}

// A restore that succeeds leaves setup alone.
// @verifies breaktest:INV-18
func TestBreakSQLSwapRestoreSucceedsNoSetup(t *testing.T) {
	db := newFakeDB(t)
	restore := "  restore:\n    - " + shellYAML("printf good > "+shellQuoteTest(db.state)) + "\n"
	dir := sqlFixtureWithConfig(t, db.config(restore, db.allRunners()...))
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	if n := db.setups(t); n != 1 {
		t.Errorf("setup ran %d times, want 1", n)
	}
	if got, _ := os.ReadFile(db.state); string(got) != "good" {
		t.Errorf("database state = %q, want good", got)
	}
}

// A baseline that fails with no swap since the last setup is a real failure:
// setup is not run again and the baseline is not retried.
// @verifies breaktest:INV-18
func TestBreakSQLSwapFailingBaselineWithoutSwapNotRetried(t *testing.T) {
	db := newFakeDB(t)
	dir := sqlFixtureWithConfig(t, db.config("",
		db.runner("policy", "db/tests/policy_test.sql", "true OR (", true)))
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	r := byPromise(res.doc.Report)["orders:INV-1"]
	if r.Outcome != breaktest.Unknown || !strings.HasPrefix(r.Reason, "baseline fails") {
		t.Errorf("INV-1: %s %q, want unknown, baseline fails", r.Outcome, r.Reason)
	}
	if n := db.setups(t); n != 1 {
		t.Errorf("setup ran %d times, want 1: no swap came before the baseline", n)
	}
	if b := res.doc.Summary.Baselines; b.Run != 1 {
		t.Errorf("baselines run = %d, want 1", b.Run)
	}
}

// A baseline that fails again after the heal is a real failure and reads
// unknown, as it did before; it is retried once, not repeatedly.
// @verifies breaktest:INV-18
func TestBreakSQLSwapBaselineFailsAfterHeal(t *testing.T) {
	db := newFakeDB(t)
	dir := sqlFixtureWithConfig(t, db.config("",
		db.runner("policy", "db/tests/policy_test.sql", "true OR (", false),
		db.runner("trigger", "db/tests/trigger_test.sql", "WHEN (false)", true)))
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	runs := byPromise(res.doc.Report)
	if r := runs["orders:INV-1"]; r.Outcome != breaktest.Killed {
		t.Errorf("INV-1: %s (%s), want killed", r.Outcome, r.Reason)
	}
	if r := runs["orders:INV-2"]; r.Outcome != breaktest.Unknown || !strings.HasPrefix(r.Reason, "baseline fails") {
		t.Errorf("INV-2: %s %q, want unknown, baseline fails", r.Outcome, r.Reason)
	}
	if n := db.setups(t); n != 2 {
		t.Errorf("setup ran %d times, want 2: the first, and one for the retry", n)
	}
}

// A setup that fails after a failed restore reads unknown for the runs that
// follow, with a reason that says so.
// @verifies breaktest:INV-18
func TestBreakSQLSwapSetupFailsAfterFailedRestore(t *testing.T) {
	db := newFakeDB(t)
	// The second setup run fails.
	cfg := "break:\n  setup:\n    - " + shellYAML("[ ! -e "+shellQuoteTest(db.log)+" ] || exit 1; echo run >> "+shellQuoteTest(db.log)+"; printf good > "+shellQuoteTest(db.state)) + "\n" +
		"  restore:\n    - 'false'\n  runners:\n" + strings.Join(db.allRunners(), "")
	dir := sqlFixtureWithConfig(t, cfg)
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	runs := byPromise(res.doc.Report)
	if r := runs["orders:INV-1"]; r.Outcome != breaktest.Killed {
		t.Errorf("INV-1: %s (%s), want killed: its verifiers had already run", r.Outcome, r.Reason)
	}
	for _, p := range []string{"orders:INV-2", "orders:INV-3"} {
		if r := runs[p]; r.Outcome != breaktest.Unknown || !strings.HasPrefix(r.Reason, "setup failed after a failed restore") {
			t.Errorf("%s: %s %q, want unknown, setup failed after a failed restore", p, r.Outcome, r.Reason)
		}
	}
}
