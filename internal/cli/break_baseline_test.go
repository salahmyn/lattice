package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

const sharedGuard = `/** @module-feature orders */

/** @enforces orders:INV-1 */
export function positive(n) {
  return n > 0;
}

/** @enforces orders:INV-2 */
export function nonEmpty(s) {
  return s !== '';
}
`

const sharedTests = `/** @module-feature orders */
import test from 'node:test';
import { positive, nonEmpty } from '../src/guard.js';

/** @verifies orders:INV-1 */
test('positive rejects zero', () => {
  positive(0);
});

/** @verifies orders:INV-2 */
test('nonEmpty rejects empty', () => {
  nonEmpty('');
});
`

const sharedFeature = `id: orders
version: 1
status: production
purpose: |
  Two promises whose verifiers share one test file.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: Zero is not positive.
  - id: INV-2
    statement: An empty string is not a value.
`

// sharedRunner logs each invocation to $1 as "base" or "broken", copies
// report.xml to the JUnit path $3 and, after a break, fails ($2 = fail) or
// prints a build error ($2 = build).
const sharedRunner = `log=$1; mode=$2; junit=$3; shift 3
if grep -q 'return undefined' src/guard.js; then
  echo "broken $*" >> "$log"
  cp report.xml "$junit"
  if [ "$mode" = build ]; then echo 'src/guard.js:4:3: ERROR: Expected ";"'; exit 1; fi
  echo 'FAIL positive rejects zero'; exit 1
fi
echo "base $*" >> "$log"
cp report.xml "$junit"
`

// junitCase is one passing case of the fixture's test file.
func junitCase(name string) string {
	return `    <testcase classname="tests/guard.test.js" name="` + name + `"/>` + "\n"
}

// sharedFixture is a committed repo whose two promises are verified by two
// tests of one file, run by a fake file-level runner. extra are arguments
// before {files} ("--filter={filter}" makes it per test). The log of the
// runner's invocations is returned with the repo.
func sharedFixture(t *testing.T, mode, report string, extra ...string) (dir, log string) {
	t.Helper()
	for _, tool := range []string{"git", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir, log = t.TempDir(), filepath.Join(t.TempDir(), "invocations.log")
	args := []string{"sh", "run.sh", log, mode, "{junit}"}
	args = append(args, extra...)
	var q []string
	for _, a := range append(args, "{files}") {
		q = append(q, `"`+a+`"`)
	}
	cfg := "break:\n  runners:\n    - name: file-runner\n      match: [\"tests/**\"]\n      command: [" + strings.Join(q, ", ") + "]\n"
	files := map[string]string{
		".gitignore":                   "lattice/.cache/\nlattice/lattice.json\n",
		"package.json":                 "{\"type\":\"module\"}\n",
		"src/guard.js":                 sharedGuard,
		"tests/guard.test.js":          sharedTests,
		"run.sh":                       sharedRunner,
		"report.xml":                   "<testsuites>\n  <testsuite name=\"guard\">\n" + report + "  </testsuite>\n</testsuites>\n",
		"lattice/config.yaml":          cfg,
		"lattice/features/orders.yaml": sharedFeature,
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

// invocations counts the runner's logged invocations of a kind ("base" or
// "broken").
func invocations(t *testing.T, log, kind string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, kind+" ") {
			n++
		}
	}
	return n
}

// baselineCounts reads summary.baselines from `lattice break --json`.
func baselineCounts(t *testing.T, out string) (run, reused int) {
	t.Helper()
	var doc struct {
		Summary struct {
			Baselines map[string]int `json:"baselines"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, out)
	}
	return doc.Summary.Baselines["run"], doc.Summary.Baselines["reused"]
}

// Two promises whose verifiers are different tests of one file, with a
// runner that takes whole files, render the same baseline command: it runs
// once, and each promise reads its own verifiers from the shared report.
// @verifies breaktest:INV-13
func TestBreakSharesBaselineOfOneFile(t *testing.T) {
	dir, log := sharedFixture(t, "fail", junitCase("positive rejects zero")+junitCase("nonEmpty rejects empty"))
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	if n := invocations(t, log, "base"); n != 1 {
		t.Errorf("the shared baseline ran %d times, want 1", n)
	}
	if n := invocations(t, log, "broken"); n != 2 {
		t.Errorf("%d broken runs, want one per promise", n)
	}
	runs := byPromise(res.doc.Report)
	for p, test := range map[string]string{"orders:INV-1": "positive-rejects-zero", "orders:INV-2": "nonempty-rejects-empty"} {
		r := runs[p]
		if r.Outcome != breaktest.Killed || r.Baseline != breaktest.BaselinePass {
			t.Errorf("%s: %+v", p, r)
		}
		if len(r.RanVerifiers) != 1 || !strings.Contains(strings.ToLower(r.RanVerifiers[0]), test) || len(r.NotRunVerifiers) != 0 {
			t.Errorf("%s ran %v, not run %v", p, r.RanVerifiers, r.NotRunVerifiers)
		}
	}
	if run, reused := baselineCounts(t, res.out); run != 1 || reused != 1 {
		t.Errorf("summary.baselines = %d run, %d reused, want 1 and 1", run, reused)
	}
}

// A runner that takes {filter} renders one command per promise, so nothing
// is shared.
// @verifies breaktest:INV-13
func TestBreakFilterRunnerDoesNotShareBaseline(t *testing.T) {
	dir, log := sharedFixture(t, "fail", junitCase("positive rejects zero")+junitCase("nonEmpty rejects empty"), "--filter={filter}")
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	if n := invocations(t, log, "base"); n != 2 {
		t.Errorf("per-test baselines ran %d times, want 2", n)
	}
	if run, reused := baselineCounts(t, res.out); run != 2 || reused != 0 {
		t.Errorf("summary.baselines = %d run, %d reused, want 2 and 0", run, reused)
	}
}

// A shared baseline still proves each promise's own verifiers ran: the
// report lists only the first promise's test, so the second reads unknown,
// never survived or killed.
// @verifies breaktest:INV-7
// @verifies breaktest:INV-13
func TestBreakSharedBaselineChecksEachPromise(t *testing.T) {
	dir, log := sharedFixture(t, "fail", junitCase("positive rejects zero"))
	res := runBreakExit(t, dir, true)
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	if n := invocations(t, log, "base"); n != 1 {
		t.Errorf("the shared baseline ran %d times, want 1", n)
	}
	runs := byPromise(res.doc.Report)
	if r := runs["orders:INV-1"]; r.Outcome != breaktest.Killed || len(r.RanVerifiers) != 1 {
		t.Errorf("INV-1: %+v", r)
	}
	if r := runs["orders:INV-2"]; r.Outcome != breaktest.Unknown || len(r.RanVerifiers) != 0 ||
		!strings.Contains(r.Reason, "the filter matched no tests") {
		t.Errorf("INV-2 did not run its verifier and must read unknown: %+v", r)
	}
}

// A passing baseline is kept across invocations at the same tree: a second
// break starts none, a commit runs them again, and a dirty tree neither
// reuses nor replaces them.
// @verifies breaktest:INV-13
func TestBreakPersistsBaselinesPerTree(t *testing.T) {
	// The break does not compile, so the runs read unknown and are not
	// cached: each invocation makes them again.
	dir, log := sharedFixture(t, "build", junitCase("positive rejects zero")+junitCase("nonEmpty rejects empty"))
	run := func(want int, args ...string) {
		t.Helper()
		before := invocations(t, log, "base")
		res := runBreakExit(t, dir, true, args...)
		if res.code != 0 {
			t.Fatalf("break %v: exit %d\n%s%s", args, res.code, res.out, res.err)
		}
		for _, r := range res.doc.Runs {
			if r.Outcome != breaktest.Unknown || r.Baseline != breaktest.BaselinePass || !strings.Contains(r.Reason, "does not compile") {
				t.Fatalf("break %v: %+v", args, r)
			}
		}
		if got := invocations(t, log, "base") - before; got != want {
			t.Errorf("break %v started %d baselines, want %d", args, got, want)
		}
	}
	run(1)
	run(0) // the same tree
	if run, reused := baselineCounts(t, runBreakExit(t, dir, true).out); run != 0 || reused != 2 {
		t.Errorf("summary.baselines = %d run, %d reused, want 0 and 2", run, reused)
	}

	// A commit that changes the code runs them again.
	if err := os.WriteFile(filepath.Join(dir, "src", "other.js"), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "code")
	run(1)

	// A dirty tree is not the commit's tree: it runs its own baseline and
	// keeps the commit's.
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(1, "--allow-dirty")
	run(1, "--allow-dirty")
	if err := os.Remove(notes); err != nil {
		t.Fatal(err)
	}
	run(0)
}

// The text summary says how many baselines ran and how many were reused.
func TestBreakTextSummaryCountsBaselines(t *testing.T) {
	dir, _ := sharedFixture(t, "fail", junitCase("positive rejects zero")+junitCase("nonEmpty rejects empty"))
	res := runBreakExit(t, dir, false)
	if res.code != 0 || !strings.Contains(res.out, "2 killed, 0 survived, 0 unknown; baselines: 1 run, 1 reused") {
		t.Errorf("exit %d, summary line:\n%s", res.code, res.out)
	}
}
