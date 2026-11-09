package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

const jsGuard = `/** @module-feature web */

/** @enforces web:INV-1 */
export function positive(n) {
  return n > 0;
}

/** @enforces web:INV-2 */
export function nonEmpty(s) {
  return s !== '';
}

/** @enforces web:INV-3 */
export function small(n) {
  return n < 10;
}

/** @enforces web:INV-4 */
export const isEven = (n) => n % 2 === 0;
`

const jsHead = `/** @module-feature web */
import test from 'node:test';
import assert from 'node:assert/strict';
import { positive, nonEmpty, small, isEven } from '../src/guard.js';
`

const jsFeature = `id: web
version: 1
status: production
purpose: |
  Two JavaScript test frameworks over one module.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: Zero is not positive.
  - id: INV-2
    statement: An empty string is not a value.
  - id: INV-3
    statement: Ten is not small.
  - id: INV-4
    statement: Even numbers are even.
`

// nodeRunner is a runner of Node's built-in test runner with a JUnit report.
func nodeRunner(name, glob string, extra ...string) string {
	cmd := []string{"node", "--test", "--test-reporter=junit", "--test-reporter-destination={junit}"}
	cmd = append(cmd, extra...)
	var q []string
	for _, c := range append(cmd, "{files}") {
		q = append(q, `"`+c+`"`)
	}
	return "    - name: " + name + "\n      match: [\"" + glob + "\"]\n      command: [" + strings.Join(q, ", ") + "]\n"
}

// jsFixture is a committed repo with two "frameworks" (two node --test
// runners, one per directory), each its own runner entry.
func jsFixture(t *testing.T, config string) string {
	t.Helper()
	for _, tool := range []string{"git", "node"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":                "lattice/.cache/\nlattice/lattice.json\nnode_modules/\n",
		"package.json":              "{\"type\":\"module\"}\n",
		"src/guard.js":              jsGuard,
		"lattice/config.yaml":       config,
		"lattice/features/web.yaml": jsFeature,
		"node_modules/dep/index.js": "export default 1;\n",
		"fw1/guard.test.js":         jsHead + "\n/** @verifies web:INV-1 */\ntest('fw1 positive checks both sides', () => {\n  assert.equal(positive(1), true);\n  assert.equal(positive(0), false);\n});\n\n/** @verifies web:INV-2 */\ntest('fw1 nonEmpty only calls it', () => {\n  nonEmpty('x');\n});\n\n/** @verifies web:INV-3 */\ntest('fw1 small only calls it', () => {\n  small(1);\n});\n",
		"fw2/guard.test.js":         jsHead + "\n/** @verifies web:INV-1 */\ntest('fw2 positive only calls it', () => {\n  positive(1);\n});\n\n/** @verifies web:INV-2 */\ntest('fw2 nonEmpty is checked', () => {\n  assert.equal(nonEmpty('x'), true);\n});\n",
		"fw3/orphan.test.js":        jsHead + "\n/** @verifies web:INV-3 */\ntest('fw3 small is checked', () => {\n  assert.equal(small(1), true);\n});\n",
		"fw4/even.test.js":          jsHead + "\n/** @verifies web:INV-4 */\ntest('fw4 even is checked', () => {\n  assert.equal(isEven(2), true);\n});\n",
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
	return dir
}

func has(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// A promise whose verifiers span two frameworks runs both: it is killed when
// either group fails after the break, and each framework's tests are
// recorded as run. A verifier no runner covers is not run, so a survived
// outcome carries it in not_run_verifiers; a runner whose filter selects no
// test leaves the baseline unknown, never survived.
// @verifies breaktest:INV-3
// @verifies breaktest:INV-6
// @verifies breaktest:INV-7
func TestBreakRunnersSpanFrameworks(t *testing.T) {
	cfg := "break:\n  runners:\n" +
		nodeRunner("fw-one", "fw1/**", "--test-name-pattern={filter}") +
		nodeRunner("fw-two", "fw2/**", "--test-name-pattern={filter}") +
		nodeRunner("fw-zero", "fw4/**", "--test-name-pattern=no-such-test-anywhere")
	dir := jsFixture(t, cfg)

	rep, out, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	runs := byPromise(rep)

	// INV-1: only the first framework's verifier asserts, so it is the one
	// that fails; both frameworks ran.
	r := runs["web:INV-1"]
	if r.Outcome != breaktest.Killed || r.Baseline != "pass" {
		t.Fatalf("INV-1: %+v", r)
	}
	if len(r.RanVerifiers) != 2 || len(r.NotRunVerifiers) != 0 {
		t.Errorf("INV-1 ran %v, not run %v", r.RanVerifiers, r.NotRunVerifiers)
	}
	if len(r.FailedTests) != 1 || !strings.Contains(r.FailedTests[0], "fw1 positive checks both sides") {
		t.Errorf("INV-1 failed_tests = %v", r.FailedTests)
	}
	if !strings.HasPrefix(r.Reason, "failed: ") || !strings.Contains(r.Reason, "fw1 positive") {
		t.Errorf("INV-1 reason = %q", r.Reason)
	}

	// INV-2: the second framework is the one that notices.
	r = runs["web:INV-2"]
	if r.Outcome != breaktest.Killed || len(r.RanVerifiers) != 2 || len(r.FailedTests) != 1 || !strings.Contains(r.FailedTests[0], "fw2 nonEmpty is checked") {
		t.Errorf("INV-2: %+v", r)
	}

	// INV-3: the checking verifier is under a glob no runner matches. The
	// other one survives, but the promise is not fully run.
	r = runs["web:INV-3"]
	if r.Outcome != breaktest.Survived || len(r.RanVerifiers) != 1 || len(r.NotRunVerifiers) != 1 ||
		!strings.Contains(r.NotRunVerifiers[0], "orphan") || !strings.Contains(r.Reason, "no runner matches fw3/orphan.test.js") {
		t.Errorf("INV-3: %+v", r)
	}

	// INV-4: a filter that selects no test.
	r = runs["web:INV-4"]
	if r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "the filter matched no tests") {
		t.Errorf("INV-4: %+v", r)
	}
	if len(r.RanVerifiers) != 0 {
		t.Errorf("INV-4 ran %v", r.RanVerifiers)
	}
}

// A changed runner command changes the cache key: the outcome is run again,
// not reused, while an unchanged config reuses it.
// @verifies breaktest:INV-5
func TestBreakRunnerChangeInvalidatesCache(t *testing.T) {
	cfg := "break:\n  runners:\n" + nodeRunner("fw-one", "fw1/**", "--test-name-pattern={filter}")
	dir := jsFixture(t, cfg)
	rep, out, err := runBreak(t, dir, "--promise", "web:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if r := byPromise(rep)["web:INV-1"]; r.Outcome != breaktest.Killed || r.Cached || r.ConfigHash == "" {
		t.Fatalf("first run: %+v", r)
	}
	rep, _, err = runBreak(t, dir, "--promise", "web:INV-1")
	if r := byPromise(rep)["web:INV-1"]; err != nil || !r.Cached {
		t.Fatalf("an unchanged config should reuse the outcome: %v %+v", err, r)
	}

	// The same promise with a runner that cannot find its tests: a stale
	// "killed" must not survive the change.
	changed := "break:\n  runners:\n" + nodeRunner("fw-one", "fw1/**", "--test-name-pattern=nothing-matches-this")
	if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "runner")
	rep, out, err = runBreak(t, dir, "--promise", "web:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	r := byPromise(rep)["web:INV-1"]
	if r.Cached || r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "the filter matched no tests") {
		t.Errorf("a changed runner must be run again: %+v", r)
	}
}

// break.setup runs in the worktree with {repo} set, and node_modules is
// linked from the main checkout unless link_node_modules is false.
// @verifies breaktest:INV-8
func TestBreakSetupAndNodeModules(t *testing.T) {
	// The verifier runs only if setup wrote its marker and the link is there,
	// and the enforcer is intact (until broken).
	check := "test -f setup-marker.txt && test -f \"$(cat repo-path.txt)/node_modules/dep/index.js\" && test -f node_modules/dep/index.js && ! grep -q 'if (true)' src/guard.js"
	cfg := "break:\n  setup:\n    - \"echo ran > setup-marker.txt\"\n    - \"echo {repo} > repo-path.txt\"\n  commands:\n    typescript: " + shellYAML(check) + "\n"
	dir := jsFixture(t, cfg)

	rep, out, err := runBreak(t, dir, "--promise", "web:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if r := byPromise(rep)["web:INV-1"]; r.Outcome != breaktest.Killed || r.Baseline != "pass" {
		t.Errorf("setup and the node_modules link should be in place: %+v", r)
	}

	// Opting out of the link leaves the baseline failing: unknown.
	off := cfg + "  link_node_modules: false\n"
	if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte(off), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "no link")
	rep, out, err = runBreak(t, dir, "--promise", "web:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if r := byPromise(rep)["web:INV-1"]; r.Outcome != breaktest.Unknown || r.Baseline != "fail" {
		t.Errorf("without the link the baseline must fail: %+v", r)
	}

	// A failing setup reads unknown, with the command named.
	bad := "break:\n  setup:\n    - \"exit 3\"\n  commands:\n    typescript: \"true\"\n"
	if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "bad setup")
	rep, out, err = runBreak(t, dir, "--promise", "web:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if r := byPromise(rep)["web:INV-1"]; r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "setup failed: exit 3") {
		t.Errorf("failing setup: %+v", r)
	}
	if n := worktreeCount(t, dir); n != 1 {
		t.Errorf("%d worktrees left", n)
	}
}

func shellYAML(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// A runtime failure with a stack trace is a failing test, so it reads killed
// when the baseline passed; only an explicit build marker reads unknown.
// @verifies breaktest:INV-3
func TestBreakRuntimeFailureVersusBuildMarker(t *testing.T) {
	dir := jsFixture(t, "{}\n")
	runtime := "if grep -q 'return undefined' src/guard.js; then printf 'TypeError: x is not a function\\n    at positive (src/guard.js:3:10)\\n}\\n'; exit 1; fi"
	build := "if grep -q 'return undefined' src/guard.js; then printf 'src/guard.js:3:10: ERROR: Expected \";\"\\n}\\n'; exit 1; fi"
	for _, c := range []struct {
		name, cmd, outcome, reason string
	}{
		{"runtime", runtime, breaktest.Killed, "TypeError: x is not a function"},
		{"build", build, breaktest.Unknown, "does not compile"},
	} {
		cfg := "break:\n  commands:\n    typescript: " + shellYAML(c.cmd) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "commit", "-q", "-a", "-m", c.name, "--allow-empty")
		rep, out, err := runBreak(t, dir, "--promise", "web:INV-1")
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, out)
		}
		r := byPromise(rep)["web:INV-1"]
		if r.Outcome != c.outcome || !strings.Contains(r.Reason, c.reason) {
			t.Errorf("%s: %+v", c.name, r)
		}
		if r.Reason == "}" || strings.HasSuffix(r.Reason, ": }") {
			t.Errorf("%s: unreadable reason %q", c.name, r.Reason)
		}
		// A runner with no {junit} says the executed count was not checked.
		if !strings.Contains(r.Reason, "not checked") && c.outcome == breaktest.Killed {
			t.Errorf("%s: reason should note the unchecked count: %q", c.name, r.Reason)
		}
	}
}

// The text table shows only the selected runs, while breaks.json keeps every
// run of the commit.
// @verifies breaktest:INV-2
func TestBreakPromiseTableShowsSelected(t *testing.T) {
	dir := breakFixture(t)
	if _, out, err := runBreak(t, dir); err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	var out, errOut bytes.Buffer
	io := &IO{Repo: dir, Out: &out, Err: &errOut}
	cmd := newBreakCommand(io)
	cmd.SetArgs([]string{"--promise", "guard:INV-2"})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("break: %v\n%s", err, out.String())
	}
	text := out.String()
	if !strings.Contains(text, "guard:INV-2") || strings.Contains(text, "guard:INV-1") || strings.Contains(text, "guard:INV-3") {
		t.Errorf("the table should show only the selected promise:\n%s", text)
	}
	if !strings.Contains(text, "1 killed, 0 survived") && !strings.Contains(text, "0 killed, 1 survived") {
		t.Errorf("the summary should count only the selected run:\n%s", text)
	}
	rep := breaktest.Read(filepath.Join(dir, "lattice"))
	if len(rep.Runs) != 3 {
		t.Errorf("breaks.json should keep all three runs, has %d", len(rep.Runs))
	}
}

// dirty is recorded for any uncommitted change that is not the ledger, even
// when the ledger is dirty too; a ledger-only change stays clean.
// @verifies breaktest:INV-2
func TestBreakDirtyWithLedgerAndCode(t *testing.T) {
	dir := breakFixture(t)
	ledger := filepath.Join(dir, "lattice", ".ledger", "ledger.jsonl")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte(`{"kind":"results_ingested"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, out, err := runBreak(t, dir, "--allow-dirty", "--promise", "guard:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if !rep.Dirty {
		t.Error("an untracked non-ledger file must be recorded as dirty: true")
	}
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	rep, out, err = runBreak(t, dir, "--promise", "guard:INV-1")
	if err != nil || rep.Dirty {
		t.Errorf("ledger only: %v dirty=%v\n%s", err, rep.Dirty, out)
	}
}
