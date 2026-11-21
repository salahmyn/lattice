package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// swapGroupsRunners: a client runner that passes (or fails after the break,
// per mode), and a database runner whose command needs the SQL object swap.
func swapGroupsConfig(log, mode string, withClient bool) string {
	cfg := "break:\n  runners:\n"
	if withClient {
		cfg += "    - name: client\n      match: [\"tests/**\"]\n" +
			"      command: [\"sh\", \"run.sh\", \"" + log + "\", \"" + mode + "\", \"{junit}\", \"{files}\"]\n"
	}
	pg := `echo "pg $*" >> "$1"; echo 'ok 1 - db'`
	return cfg + "    - name: db\n      match: [\"db/tests/**\"]\n" +
		"      command: [sh, -c, " + shellYAML(pg) + ", sh, " + shellYAML(log) + ", \"{patched_sql}\", \"{original_sql}\", \"{files}\"]\n"
}

// swapGroupsFixture is a client function enforcing orders:INV-1 with a client
// test and a database test, each with its own runner; the database runner
// needs {patched_sql}, which a client break does not have.
func swapGroupsFixture(t *testing.T, mode string, withClient bool) (dir, log string) {
	t.Helper()
	for _, tool := range []string{"git", "sh", "grep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir, log = t.TempDir(), filepath.Join(t.TempDir(), "invocations.log")
	files := map[string]string{
		".gitignore":                   "lattice/.cache/\nlattice/lattice.json\n",
		"package.json":                 "{\"type\":\"module\"}\n",
		"src/guard.js":                 sharedGuard,
		"run.sh":                       partialRunner,
		"report.xml":                   "<testsuites>\n  <testsuite name=\"guard\">\n" + junitCase("positive rejects zero") + junitCase("nonEmpty rejects empty") + "  </testsuite>\n</testsuites>\n",
		"lattice/config.yaml":          swapGroupsConfig(log, mode, withClient),
		"lattice/adapters.yaml":        "adapters:\n  postgres:\n    enabled: true\n",
		"lattice/features/orders.yaml": sharedFeature,
		"db/tests/guard_test.sql":      pgTAPFile("INV-1", "zero is not positive"),
	}
	if withClient {
		files["tests/guard.test.js"] = sharedTests
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fixture")
	return dir, log
}

func swapGroupsRun(t *testing.T, dir string) breaktest.Run {
	t.Helper()
	res := runBreakExit(t, dir, true, "--promise", "orders:INV-1")
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	return byPromise(res.doc.Report)["orders:INV-1"]
}

// A runner group that needs {patched_sql} does not block the others on a
// break that has no SQL swap: its verifiers are not run, and the group that
// can run decides. A failure after the break kills it.
// @verifies breaktest:INV-6
func TestBreakSwapGroupDroppedOthersRunKilled(t *testing.T) {
	dir, log := swapGroupsFixture(t, "fail", true)
	r := swapGroupsRun(t, dir)
	if r.Outcome != breaktest.Killed || r.Baseline != breaktest.BaselinePass ||
		len(r.RanVerifiers) != 1 || len(r.NotRunVerifiers) != 1 || !strings.Contains(r.NotRunVerifiers[0], "guard_test") {
		t.Errorf("want killed with the database verifier not run: %+v", r)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "pg ") {
		t.Errorf("the database runner ran without its SQL files:\n%s", b)
	}
}

// When the group that ran passes after the break, the run is unknown with a
// partial reason that names the verifiers that did not run and why, never
// survived.
// @verifies breaktest:INV-6
func TestBreakSwapGroupDroppedOthersPassReadsPartial(t *testing.T) {
	dir, log := swapGroupsFixture(t, "pass", true)
	r := swapGroupsRun(t, dir)
	if r.Outcome != breaktest.Unknown || r.Baseline != breaktest.BaselinePass || len(r.RanVerifiers) != 1 || len(r.NotRunVerifiers) != 1 ||
		!strings.HasPrefix(r.Reason, "partial: ") || !strings.Contains(r.Reason, "guard_test") ||
		!strings.Contains(r.Reason, "needs {patched_sql}, which a typescript break does not have") {
		t.Errorf("want unknown (partial) naming the database verifier: %+v", r)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "pg ") {
		t.Errorf("the database runner ran without its SQL files:\n%s", b)
	}
}

// When every group needs the swap, the reason stays the old one and nothing
// runs.
// @verifies breaktest:INV-6
func TestBreakSwapGroupOnlyKeepsUnknownReason(t *testing.T) {
	dir, log := swapGroupsFixture(t, "pass", false)
	r := swapGroupsRun(t, dir)
	if r.Outcome != breaktest.Unknown || r.Reason != "{patched_sql} not available for this break" {
		t.Errorf("want the old unknown reason: %+v", r)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a runner ran")
	}
}

// A SQL break has the swap, so the group that needs it still runs: here it
// applies the patched statement, notices the break and kills it.
// @verifies breaktest:INV-6
func TestBreakSQLBreakStillRunsSwapGroup(t *testing.T) {
	script := `if grep -qF 'true OR (' "$1"; then echo 'not ok 1 - policy'; exit 1; fi; echo 'ok 1 - policy'`
	cfg := "break:\n  runners:\n    - name: db\n      match: [\"db/tests/policy_test.sql\"]\n" +
		"      command: [sh, -c, " + shellYAML(script) + ", sh, \"{patched_sql}\", \"{original_sql}\", \"{files}\"]\n"
	dir := sqlFixtureWithConfig(t, cfg)
	res := runBreakExit(t, dir, true, "--promise", "orders:INV-1")
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	r := byPromise(res.doc.Report)["orders:INV-1"]
	if r.Outcome != breaktest.Killed || len(r.RanVerifiers) != 1 || len(r.NotRunVerifiers) != 0 {
		t.Errorf("a SQL break runs the swap group: %+v", r)
	}
}
