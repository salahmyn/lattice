package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// partialRunner logs each invocation to $1 as "base" or "broken" and copies
// report.xml to the JUnit path $3. After a break it fails when $2 is fail
// and passes otherwise.
const partialRunner = `log=$1; mode=$2; junit=$3; shift 3
cp report.xml "$junit"
if grep -q 'return undefined' src/guard.js; then
  echo "broken $*" >> "$log"
  if [ "$mode" = fail ]; then echo 'FAIL positive rejects zero'; exit 1; fi
  exit 0
fi
echo "base $*" >> "$log"
`

// partialFixture is the shared-file repo of sharedFixture with a second
// verifier of orders:INV-1 that no runner matches: INV-1 has two verifier
// groups, one with a runner and one without, and INV-2 has one verifier,
// which the runner runs. mode is the runner's behaviour after a break.
func partialFixture(t *testing.T, mode string) (dir, log string) {
	t.Helper()
	for _, tool := range []string{"git", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir, log = t.TempDir(), filepath.Join(t.TempDir(), "invocations.log")
	report := junitCase("positive rejects zero") + junitCase("nonEmpty rejects empty")
	files := map[string]string{
		".gitignore":                   "lattice/.cache/\nlattice/lattice.json\n",
		"package.json":                 "{\"type\":\"module\"}\n",
		"src/guard.js":                 sharedGuard,
		"tests/guard.test.js":          sharedTests,
		"other/extra.test.js":          "/** @module-feature orders */\nimport test from 'node:test';\nimport { positive } from '../src/guard.js';\n\n/** @verifies orders:INV-1 */\ntest('positive is checked elsewhere', () => {\n  positive(1);\n});\n",
		"run.sh":                       partialRunner,
		"report.xml":                   "<testsuites>\n  <testsuite name=\"guard\">\n" + report + "  </testsuite>\n</testsuites>\n",
		"lattice/config.yaml":          partialConfig(log, mode),
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

func partialConfig(log, mode string) string {
	return "break:\n  runners:\n    - name: file-runner\n      match: [\"tests/**\"]\n" +
		"      command: [\"sh\", \"run.sh\", \"" + log + "\", \"" + mode + "\", \"{junit}\", \"{files}\"]\n"
}

// A break that the verifiers that ran did not notice, while another verifier
// of the promise did not run (its file has no runner), reads unknown with a
// partial reason, never survived; a promise whose verifiers all ran and
// passed both times stays survived. The partial reading holds with a shared
// baseline and with one kept from an earlier run at the same tree, and a
// partial run counts as unknown, not survived, for --fail-on.
// @verifies breaktest:INV-4
// @verifies breaktest:INV-7
func TestBreakPartialRunReadsUnknown(t *testing.T) {
	dir, log := partialFixture(t, "pass")
	check := func(res breakResult, label string) {
		t.Helper()
		if res.code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", label, res.code, res.out, res.err)
		}
		runs := byPromise(res.doc.Report)
		r := runs["orders:INV-1"]
		if r.Outcome != breaktest.Unknown || r.Baseline != breaktest.BaselinePass || r.Cached ||
			len(r.RanVerifiers) != 1 || len(r.NotRunVerifiers) != 1 || !strings.Contains(r.NotRunVerifiers[0], "extra") ||
			!strings.HasPrefix(r.Reason, "partial: the 1 verifier(s) that ran passed after the break; 1 verifier(s) did not run: ") ||
			!strings.Contains(r.Reason, "no runner matches other/extra.test.js") {
			t.Errorf("%s: INV-1 must read unknown (partial): %+v", label, r)
		}
		if r := runs["orders:INV-2"]; r.Outcome != breaktest.Survived || len(r.RanVerifiers) != 1 || len(r.NotRunVerifiers) != 0 {
			t.Errorf("%s: INV-2 ran every verifier and must read survived: %+v", label, r)
		}
		s := res.doc.Summary
		if s.Survived != 1 || s.Unknown != 1 || len(s.UnknownReasons) != 1 ||
			s.UnknownReasons[0] != (reasonCount{"partial: some verifiers did not run", 1}) {
			t.Errorf("%s: summary = %+v", label, s)
		}
	}

	// The two promises share one baseline invocation.
	check(runBreakExit(t, dir, true), "shared")
	if n := invocations(t, log, "base"); n != 1 {
		t.Errorf("the shared baseline ran %d times, want 1", n)
	}
	// The same tree again: the baseline is the kept one, and the partial run
	// is not cached, so it is broken again.
	before := invocations(t, log, "broken")
	check(runBreakExit(t, dir, true), "persisted")
	if n := invocations(t, log, "base"); n != 1 {
		t.Errorf("the kept baseline was run again (%d runs)", n)
	}
	if n := invocations(t, log, "broken") - before; n != 1 {
		t.Errorf("%d broken runs, want 1 (the partial run; the survived one is cached)", n)
	}

	res := runBreakExit(t, dir, true, "--fail-on", "survived")
	if res.code != 1 || res.doc.Error != "1 survived; --fail-on survived" {
		t.Errorf("--fail-on survived counts the full survival only: exit %d, %q", res.code, res.doc.Error)
	}
	// With only the partial run shown, no break ran.
	res = runBreakExit(t, dir, false, "--promise", "orders:INV-1")
	if !strings.Contains(res.out, "no break ran: 1 of 1 run unknown (partial: some verifiers did not run ×1)") {
		t.Errorf("a partial run is not a break that ran:\n%s", res.out)
	}
}

// A verifier that ran and failed after the break kills it, even when another
// verifier of the promise did not run.
// @verifies breaktest:INV-4
func TestBreakPartialRunKilledByGroupThatRan(t *testing.T) {
	dir, _ := partialFixture(t, "fail")
	res := runBreakExit(t, dir, true, "--promise", "orders:INV-1")
	if res.code != 0 {
		t.Fatalf("break: exit %d\n%s%s", res.code, res.out, res.err)
	}
	r := byPromise(res.doc.Report)["orders:INV-1"]
	if r.Outcome != breaktest.Killed || r.Baseline != breaktest.BaselinePass || len(r.NotRunVerifiers) != 1 {
		t.Errorf("a group that failed after the break kills it: %+v", r)
	}
}
