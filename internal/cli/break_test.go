package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

const breakFixtureGuard = `package guard

// @module-feature guard

// @enforces guard:INV-1
func Positive(n int) bool {
	return n > 0
}

// @enforces guard:INV-2
func NonEmpty(s string) bool {
	return s != ""
}

// @enforces guard:INV-3
func Small(n int) (bool, error) {
	return n < 10, nil
}
`

const breakFixtureTests = `package guard

import "testing"

// @verifies guard:INV-1
func TestPositiveRejectsZero(t *testing.T) {
	if Positive(0) || !Positive(1) {
		t.Fatal("wrong")
	}
}

// @verifies guard:INV-2
func TestNonEmptyCalledOnly(t *testing.T) {
	NonEmpty("x")
}

// @verifies guard:INV-3
func TestSmallBaselineFails(t *testing.T) {
	Small(1)
	t.Fatal("always fails")
}
`

const breakFixtureFeature = `id: guard
version: 1
status: production
purpose: |
  A tiny fixture for break testing.
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
`

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// breakFixture is a committed Go repo with three promises.
func breakFixture(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                      "module fixture\n\ngo 1.21\n",
		".gitignore":                  "lattice/.cache/\nlattice/lattice.json\n",
		"guard/guard.go":              breakFixtureGuard,
		"guard/guard_test.go":         breakFixtureTests,
		"lattice/config.yaml":         "{}\n",
		"lattice/features/guard.yaml": breakFixtureFeature,
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

func runBreak(t *testing.T, dir string, args ...string) (breaktest.Report, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	io := &IO{Repo: dir, JSON: true, Out: &out, Err: &errOut}
	cmd := newBreakCommand(io)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	var rep breaktest.Report
	if err == nil {
		if jerr := json.Unmarshal(out.Bytes(), &rep); jerr != nil {
			t.Fatalf("output is not a report: %v\n%s", jerr, out.String())
		}
	}
	return rep, out.String() + errOut.String(), err
}

func byPromise(rep breaktest.Report) map[string]breaktest.Run {
	m := map[string]breaktest.Run{}
	for _, r := range rep.Runs {
		m[r.Promise] = r
	}
	return m
}

func worktreeCount(t *testing.T, dir string) int {
	t.Helper()
	return strings.Count(runGit(t, dir, "worktree", "list", "--porcelain"), "worktree ")
}

// Three promises on a tiny Go repo: a real verifier is killed, a verifier that
// calls the enforcer and asserts nothing survives, and a failing baseline is
// unknown. The working tree is untouched and the temporary worktree is gone.
// @verifies breaktest:INV-2
// @verifies breaktest:INV-3
// @verifies breaktest:INV-4
func TestBreakEndToEnd(t *testing.T) {
	dir := breakFixture(t)
	before := runGit(t, dir, "status", "--porcelain", "--ignored")

	rep, out, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if rep.Schema != "lattice.breaks/1" || rep.Dirty || len(rep.Commit) < 40 {
		t.Errorf("header = %+v", rep)
	}
	runs := byPromise(rep)
	for promise, want := range map[string]struct{ outcome, baseline string }{
		"guard:INV-1": {breaktest.Killed, "pass"},
		"guard:INV-2": {breaktest.Survived, "pass"},
		"guard:INV-3": {breaktest.Unknown, "fail"},
	} {
		r, ok := runs[promise]
		if !ok {
			t.Errorf("%s: no run in %+v", promise, rep.Runs)
			continue
		}
		if r.Outcome != want.outcome || r.Baseline != want.baseline {
			t.Errorf("%s: outcome %s baseline %s (%s), want %s %s", promise, r.Outcome, r.Baseline, r.Reason, want.outcome, want.baseline)
		}
		if r.Method != "early_return" || r.EnforcerHash == "" || r.VerifierHash == "" || len(r.Verifiers) != 1 {
			t.Errorf("%s: run = %+v", promise, r)
		}
	}
	if r := runs["guard:INV-3"]; !strings.Contains(r.Reason, "baseline fails") {
		t.Errorf("failing baseline reason = %q", r.Reason)
	}
	if r := runs["guard:INV-1"]; r.Enforcer != "guard.Positive" || r.EnforcerFile != "guard/guard.go" {
		t.Errorf("enforcer = %s in %s", r.Enforcer, r.EnforcerFile)
	}

	// The report is on disk for the audit, and the tree is as it was.
	if _, err := os.Stat(filepath.Join(dir, "lattice", ".cache", "results", "breaks.json")); err != nil {
		t.Errorf("breaks.json not written: %v", err)
	}
	if after := runGit(t, dir, "status", "--porcelain", "--ignored"); after != before && !strings.Contains(after, "lattice/.cache") {
		t.Errorf("working tree changed:\n%s\n->\n%s", before, after)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "guard", "guard.go")); string(got) != breakFixtureGuard {
		t.Error("the enforcer in the working tree was edited")
	}
	if n := worktreeCount(t, dir); n != 1 {
		t.Errorf("%d worktrees left, want only the main one", n)
	}

	// A second run reuses the killed and survived outcomes, not the unknown.
	rep, out, err = runBreak(t, dir)
	if err != nil {
		t.Fatalf("second break: %v\n%s", err, out)
	}
	runs = byPromise(rep)
	if !runs["guard:INV-1"].Cached || !runs["guard:INV-2"].Cached || runs["guard:INV-3"].Cached {
		t.Errorf("cache: %+v", rep.Runs)
	}

	// One promise, by name.
	rep, _, err = runBreak(t, dir, "--promise", "guard:INV-2")
	if err != nil || len(rep.Runs) != 3 {
		t.Errorf("--promise keeps the other runs of the same commit: %v %d", err, len(rep.Runs))
	}
}

// A dirty tree is refused, then tested as it stands with --allow-dirty.
// @verifies breaktest:INV-2
func TestBreakDirtyTree(t *testing.T) {
	dir := breakFixture(t)
	edited := strings.Replace(breakFixtureGuard, "return n > 0", "return n >= 0", 1)
	if err := os.WriteFile(filepath.Join(dir, "guard", "guard.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, out, err := runBreak(t, dir); err == nil || !strings.Contains(out, "uncommitted changes") {
		t.Fatalf("a dirty tree should be refused: %v %s", err, out)
	}
	rep, out, err := runBreak(t, dir, "--allow-dirty", "--promise", "guard:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if !rep.Dirty {
		t.Error("dirty: true was not recorded")
	}
	// The edited enforcer makes the baseline fail: Positive(0) is now true.
	if r := byPromise(rep)["guard:INV-1"]; r.Outcome != breaktest.Unknown || r.Baseline != "fail" {
		t.Errorf("the dirty tree was not what ran: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "guard", "guard.go")); string(got) != edited {
		t.Error("the user's edit was changed")
	}
	if n := worktreeCount(t, dir); n != 1 {
		t.Errorf("%d worktrees left", n)
	}
}

// Lattice's own ledger, appended by `results ingest`, does not make the tree
// dirty, so `results ingest` then `break` works without --allow-dirty.
// @verifies breaktest:INV-2
func TestBreakIgnoresLedger(t *testing.T) {
	dir := breakFixture(t)
	ledger := filepath.Join(dir, "lattice", ".ledger", "ledger.jsonl")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte(`{"kind":"results_ingested"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, out, err := runBreak(t, dir, "--promise", "guard:INV-1")
	if err != nil {
		t.Fatalf("a ledger-only change should not be refused: %v\n%s", err, out)
	}
	if rep.Dirty {
		t.Error("a ledger-only change was recorded as dirty")
	}
}

// A script at lattice/breaks/<feature>/<INV>.patch replaces the generated break.
// @verifies breaktest:INV-1
func TestBreakScripted(t *testing.T) {
	dir := breakFixture(t)
	p := filepath.Join(dir, "guard", "guard.go")
	if err := os.WriteFile(p, []byte(strings.Replace(breakFixtureGuard, "return n > 0", "return true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := runGit(t, dir, "diff")
	runGit(t, dir, "checkout", "--", "guard/guard.go")
	script := filepath.Join(dir, "lattice", "breaks", "guard", "INV-1.patch")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "script")

	rep, out, err := runBreak(t, dir, "--promise", "guard:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	r := byPromise(rep)["guard:INV-1"]
	if r.Method != "scripted" || r.Script != "lattice/breaks/guard/INV-1.patch" || r.Outcome != breaktest.Killed {
		t.Errorf("scripted run = %+v", r)
	}
	// A script that does not apply is unknown, not a crash.
	if err := os.WriteFile(script, []byte("--- a/nope\n+++ b/nope\n@@ -1 +1 @@\n-x\n+y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "bad script")
	rep, _, err = runBreak(t, dir, "--promise", "guard:INV-1")
	if r := byPromise(rep)["guard:INV-1"]; err != nil || r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "does not apply") {
		t.Errorf("bad script: %v %+v", err, r)
	}
}

// Languages without a runner template read unknown.
// @verifies breaktest:INV-3
func TestBreakNoRunnerConfigured(t *testing.T) {
	dir := breakFixture(t)
	files := map[string]string{
		"lattice/features/web.yaml": "id: web\nversion: 1\nstatus: production\npurpose: x\nowners:\n  business: n\n  engineering: n\ninvariants:\n  - id: INV-1\n    statement: A thing.\n",
		"web/a.ts":                  "/** @module-feature web */\n\n/** @enforces web:INV-1 */\nexport function f(a: number): number {\n  return a;\n}\n",
		"web/a.test.ts":             "/** @module-feature web */\n\n/** @verifies web:INV-1 */\ntest('f', () => {});\n",
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
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "web")
	rep, out, err := runBreak(t, dir, "--feature", "web")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	r := byPromise(rep)["web:INV-1"]
	if r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "no runner configured") {
		t.Errorf("no runner: %+v", r)
	}

	// With a command template the same promise is run. This one passes until
	// the generated TypeScript break is in the enforcer.
	cfg := "break:\n  commands:\n    typescript: \"! grep -q 'if (true)' web/a.ts\"\n"
	if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "runner")
	rep, out, err = runBreak(t, dir, "--feature", "web")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if r := byPromise(rep)["web:INV-1"]; r.Outcome != breaktest.Killed || r.Baseline != "pass" {
		t.Errorf("template runner: %+v", r)
	}
}

// ingestFixture is breakFixture laid out with its workspace at ws (relative
// to the checkout) and the given .gitignore, with the ledger committed as
// `lattice init` leaves it. It returns the checkout and the workspace.
func ingestFixture(t *testing.T, ws, gitignore string) (string, string) {
	t.Helper()
	dir := breakFixture(t)
	runGit(t, dir, "rm", "-q", "-r", "--cached", ".")
	if ws != "lattice" {
		if err := os.Rename(filepath.Join(dir, "lattice"), filepath.Join(dir, ws)); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		".gitignore": gitignore,
		filepath.Join(ws, ".ledger", "ledger.jsonl"): "",
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
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "layout")
	return dir, filepath.Join(dir, ws)
}

// A fresh `results ingest` in a clean checkout leaves `break` able to run
// without --allow-dirty, wherever the workspace sits and whether or not the
// project's .gitignore (written before `lattice init`) ignores the cache:
// the cache that ingest writes is Lattice's own state, and ingest leaves the
// tracked ledger alone.
// @verifies breaktest:INV-9
func TestBreakAfterIngestIsClean(t *testing.T) {
	junit := filepath.Join(t.TempDir(), "junit.xml")
	if err := os.WriteFile(junit, []byte(`<testsuite name="guard"><testcase classname="fixture/guard" name="TestPositiveRejectsZero"/></testsuite>`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, layout := range map[string]struct{ ws, gitignore string }{
		"default layout":                {"lattice", "lattice/.cache/\nlattice/lattice.json\n"},
		"cache not ignored":             {"lattice", "lattice/lattice.json\n"},
		"workspace not named lattice":   {"specs", "specs/.cache/\nspecs/lattice.json\n"},
		"other name, cache not ignored": {"specs", "specs/lattice.json\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, ws := ingestFixture(t, layout.ws, layout.gitignore)
			if out, err := run(t, ws, newResultsCommand, "ingest", junit); err != nil {
				t.Fatalf("ingest: %v\n%s", err, out)
			}
			// Ingest records its run in the cache, never in the tracked
			// ledger (a dirty ledger is covered by TestBreakIgnoresLedger).
			if st := runGit(t, dir, "status", "--porcelain"); strings.Contains(st, layout.ws+"/.ledger/") {
				t.Fatalf("ingest must not touch the tracked ledger:\n%s", st)
			}
			rep, out, err := runBreak(t, ws, "--promise", "guard:INV-1")
			if err != nil {
				t.Fatalf("break after ingest was refused: %v\n%s", err, out)
			}
			if rep.Dirty {
				t.Error("Lattice's own state was recorded as dirty: true")
			}
			if r := byPromise(rep)["guard:INV-1"]; r.Outcome != breaktest.Killed {
				t.Errorf("the promise was not broken: %+v", r)
			}
		})
	}
}

// dirty in breaks.json is the tree's real state with --allow-dirty: an
// edited source file or an untracked one is dirty, even when git is set not
// to show untracked files, and the tree as it stands is what was tested.
// The refusal without --allow-dirty names the files.
// @verifies breaktest:INV-9
func TestBreakAllowDirtyRecordsDirty(t *testing.T) {
	dir := breakFixture(t)
	edited := strings.Replace(breakFixtureGuard, "return n > 0", "return n > 0 // edited", 1)
	if err := os.WriteFile(filepath.Join(dir, "guard", "guard.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, out, err := runBreak(t, dir); err == nil || !strings.Contains(out, "guard/guard.go") {
		t.Errorf("the refusal should name the dirty file: %v\n%s", err, out)
	}
	if _, _, err := runBreak(t, dir, "--allow-dirty", "--promise", "guard:INV-1"); err != nil {
		t.Fatal(err)
	}
	if rep := breaktest.Read(filepath.Join(dir, "lattice")); !rep.Dirty {
		t.Error("breaks.json: an edited source file must be dirty: true")
	}
	runGit(t, dir, "checkout", "--", "guard/guard.go")

	// Only an untracked source file, with untracked files hidden from
	// `git status`. It makes the baseline fail, so its presence is visible.
	runGit(t, dir, "config", "status.showUntrackedFiles", "no")
	extra := "package guard\n\nfunc init() { panic(\"untracked\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "guard", "extra.go"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, out, err := runBreak(t, dir, "--allow-dirty", "--promise", "guard:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if !rep.Dirty || !breaktest.Read(filepath.Join(dir, "lattice")).Dirty {
		t.Error("an untracked source file must be recorded as dirty: true")
	}
	if r := byPromise(rep)["guard:INV-1"]; r.Baseline != "fail" {
		t.Errorf("the untracked file was not in the tested tree: %+v", r)
	}
}

// `lattice break --promise <promise>#<case>` breaks the case's enforcer
// against only its tagged tests, records the run under the case ref and
// keeps the promise's own runs; an unknown case is refused.
// @verifies breaktest:INV-11
func TestBreakOneCase(t *testing.T) {
	dir := breakFixture(t)
	feature := strings.Replace(breakFixtureFeature, "    statement: Zero is not positive.\n",
		"    statement: Zero is not positive.\n    cases:\n      - id: a\n        kind: edge\n        statement: One is positive.\n", 1)
	tests := breakFixtureTests + "\n// @verifies guard:INV-1#a\nfunc TestPositiveOne(t *testing.T) {\n\tif !Positive(1) {\n\t\tt.Fatal(\"one\")\n\t}\n}\n"
	for name, body := range map[string]string{"lattice/features/guard.yaml": feature, "guard/guard_test.go": tests} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "commit", "-q", "-am", "cases")

	if _, out, err := runBreak(t, dir, "--promise", "guard:INV-1"); err != nil {
		t.Fatalf("promise break: %v\n%s", err, out)
	}
	rep, out, err := runBreak(t, dir, "--promise", "guard:INV-1#a")
	if err != nil {
		t.Fatalf("case break: %v\n%s", err, out)
	}
	runs := byPromise(rep)
	c, p := runs["guard:INV-1#a"], runs["guard:INV-1"]
	if c.Outcome != breaktest.Killed || len(c.Verifiers) != 1 || !strings.HasSuffix(c.Verifiers[0], "TestPositiveOne") {
		t.Errorf("case run = %+v", c)
	}
	if p.Outcome != breaktest.Killed || len(p.Verifiers) != 2 {
		t.Errorf("the promise's run must be kept with all its verifiers: %+v", p)
	}
	if _, out, err := runBreak(t, dir, "--promise", "guard:INV-1#z"); err == nil || !strings.Contains(out, "CASE_NOT_FOUND") {
		t.Errorf("an unknown case must be refused: %v\n%s", err, out)
	}
}

// breakResult is one `lattice break` invocation: its exit code, its output
// and, with --json, the document it printed.
type breakResult struct {
	code     int
	out, err string
	doc      struct {
		breaktest.Report
		Summary breakSummary `json:"summary"`
		Error   string       `json:"error"`
		Code    string       `json:"code"`
	}
}

func runBreakExit(t *testing.T, dir string, jsonOut bool, args ...string) breakResult {
	t.Helper()
	var out, errOut bytes.Buffer
	io := &IO{Repo: dir, JSON: jsonOut, Out: &out, Err: &errOut}
	cmd := newBreakCommand(io)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	res := breakResult{out: out.String(), err: errOut.String()}
	var ec exitCodeError
	switch {
	case err == nil:
	case errors.As(err, &ec):
		res.code = ec.code
	case IsExit(err):
		res.code = 1
	default:
		t.Fatalf("break %v: %v\n%s%s", args, err, res.out, res.err)
	}
	if jsonOut && res.code != 2 {
		if jerr := json.Unmarshal(out.Bytes(), &res.doc); jerr != nil {
			t.Fatalf("break %v: output is not one JSON document: %v\n%s", args, jerr, res.out)
		}
	}
	return res
}

// noRunnerFixture is breakFixture plus a TypeScript promise no runner covers.
func noRunnerFixture(t *testing.T) string {
	t.Helper()
	dir := breakFixture(t)
	files := map[string]string{
		"lattice/features/web.yaml": "id: web\nversion: 1\nstatus: production\npurpose: x\nowners:\n  business: n\n  engineering: n\ninvariants:\n  - id: INV-1\n    statement: A thing.\n",
		"web/a.ts":                  "/** @module-feature web */\n\n/** @enforces web:INV-1 */\nexport function f(a: number): number {\n  return a;\n}\n",
		"web/a.test.ts":             "/** @module-feature web */\n\n/** @verifies web:INV-1 */\ntest('f', () => {});\n",
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
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "web")
	return dir
}

// When no shown break ran, the output says so and names why; the exit code
// stays 0 unless --fail-on unknown asks for 1, with the counts.
// @verifies breaktest:INV-12
func TestBreakFailOnUnknownWhenNothingRan(t *testing.T) {
	dir := noRunnerFixture(t)

	res := runBreakExit(t, dir, false, "--feature", "web")
	if res.code != 0 {
		t.Fatalf("without --fail-on the exit code must be 0, got %d\n%s%s", res.code, res.out, res.err)
	}
	want := "no break ran: 1 of 1 run unknown (no runner configured ×1); missing runners: typescript ×1 (set break.runners or break.commands); break_tested stays unknown\n"
	if !strings.HasSuffix(res.out, want) || !strings.Contains(res.out, "0 killed, 0 survived, 1 unknown\n") {
		t.Errorf("text output must keep the counts and end with the no-break line:\n%s", res.out)
	}

	res = runBreakExit(t, dir, false, "--feature", "web", "--fail-on", "unknown")
	if res.code != 1 || !strings.Contains(res.err, "error: 1 unknown; --fail-on unknown") || !strings.HasSuffix(res.out, want) {
		t.Errorf("--fail-on unknown: exit %d\n%s%s", res.code, res.out, res.err)
	}

	res = runBreakExit(t, dir, true, "--feature", "web", "--fail-on", "unknown")
	s := res.doc.Summary
	if res.code != 1 || res.doc.Code != "BREAK_OUTCOME" || res.doc.Error != "1 unknown; --fail-on unknown" {
		t.Errorf("--json --fail-on unknown: exit %d, code %q, error %q", res.code, res.doc.Code, res.doc.Error)
	}
	if s.Runs != 1 || s.Unknown != 1 || s.Killed != 0 || s.Survived != 0 || s.Ran ||
		len(s.UnknownReasons) != 1 || s.UnknownReasons[0] != (reasonCount{"no runner configured", 1}) ||
		len(s.MissingRunners) != 1 || s.MissingRunners[0] != (reasonCount{"typescript", 1}) {
		t.Errorf("summary = %+v", s)
	}
}

// --fail-on survived exits 1 only when a shown run survived: a survived run
// outside --promise does not count, and killed or unknown runs never do.
// @verifies breaktest:INV-12
func TestBreakFailOnSurvived(t *testing.T) {
	dir := breakFixture(t) // INV-1 killed, INV-2 survived, INV-3 unknown

	res := runBreakExit(t, dir, true)
	s := res.doc.Summary
	if res.code != 0 || res.doc.Code != "" || s.Runs != 3 || s.Killed != 1 || s.Survived != 1 || s.Unknown != 1 || !s.Ran {
		t.Fatalf("default: exit %d, summary %+v\n%s", res.code, s, res.out)
	}
	if res := runBreakExit(t, dir, false); res.code != 0 || strings.Contains(res.out, "no break ran") {
		t.Errorf("a run that was killed or survived prints no no-break line: exit %d\n%s", res.code, res.out)
	}

	res = runBreakExit(t, dir, true, "--fail-on", "survived")
	if res.code != 1 || res.doc.Code != "BREAK_OUTCOME" || res.doc.Error != "1 survived; --fail-on survived" {
		t.Errorf("--fail-on survived: exit %d, code %q, error %q", res.code, res.doc.Code, res.doc.Error)
	}
	for _, args := range [][]string{
		{"--fail-on", "survived,unknown"},
		{"--fail-on", "survived", "--fail-on", "unknown"},
	} {
		res := runBreakExit(t, dir, true, args...)
		if res.code != 1 || res.doc.Error != "1 survived, 1 unknown; --fail-on survived,unknown" {
			t.Errorf("%v: exit %d, error %q", args, res.code, res.doc.Error)
		}
	}

	// Only the shown runs count: breaks.json still holds the survived run.
	for promise, why := range map[string]string{"guard:INV-1": "killed", "guard:INV-3": "unknown"} {
		res := runBreakExit(t, dir, false, "--promise", promise, "--fail-on", "survived")
		if res.code != 0 {
			t.Errorf("--promise %s (%s) --fail-on survived: exit %d\n%s%s", promise, why, res.code, res.out, res.err)
		}
	}
	if res := runBreakExit(t, dir, true, "--promise", "guard:INV-1", "--fail-on", "survived"); res.code != 0 || len(res.doc.Runs) != 3 || res.doc.Summary.Runs != 1 {
		t.Errorf("--promise scopes the summary, not the report: exit %d, %d runs, summary %+v", res.code, len(res.doc.Runs), res.doc.Summary)
	}
	if res := runBreakExit(t, dir, false, "--promise", "guard:INV-2", "--fail-on", "survived"); res.code != 1 {
		t.Errorf("--promise guard:INV-2 --fail-on survived: exit %d", res.code)
	}
}

// A bad --fail-on value is a usage error, refused before anything runs; with
// --fails-on-base only unknown applies.
// @verifies breaktest:INV-12
func TestBreakFailOnBadValue(t *testing.T) {
	dir := t.TempDir() // no workspace: the flag is checked first
	for _, args := range [][]string{
		{"--fail-on", "killed"},
		{"--fail-on", "survived,nope"},
		{"--fails-on-base", "main", "--fail-on", "survived"},
	} {
		res := runBreakExit(t, dir, true, args...)
		if res.code != 2 || !strings.Contains(res.out, `"code": "BAD_FLAG"`) {
			t.Errorf("%v: exit %d\n%s", args, res.code, res.out)
		}
	}
}

// The no-break line names the three most common reasons by count.
// @verifies breaktest:INV-12
func TestBreakNothingRanLine(t *testing.T) {
	var runs []breaktest.Run
	for reason, n := range map[string]int{"a": 1, "b": 5, "c": 2, "d": 2} {
		for i := 0; i < n; i++ {
			runs = append(runs, breaktest.Run{Outcome: breaktest.Unknown, Reason: reason})
		}
	}
	s := summarize(runs)
	if got, want := nothingRanLine(s), "no break ran: 10 of 10 runs unknown (b ×5, c ×2, d ×2, …); break_tested stays unknown"; got != want {
		t.Errorf("line =\n%s\nwant\n%s", got, want)
	}
	if line := nothingRanLine(summarize(append(runs, breaktest.Run{Outcome: breaktest.Killed}))); line != "" {
		t.Errorf("a killed run means a break ran: %q", line)
	}
	if line := nothingRanLine(summarize(nil)); line != "" {
		t.Errorf("no runs: %q", line)
	}
}

// With --fails-on-base, --fail-on unknown counts the shown verifiers that
// read unknown; fails_on_base and passes_on_base never trigger it.
// @verifies breaktest:INV-12
func TestBreakFailOnWithFailsOnBase(t *testing.T) {
	clearCIEnv(t)
	dir := baseProofFixture(t).dir
	res := runBreakExit(t, dir, true, "--fails-on-base", "main", "--promise", "web:INV-1", "--fail-on", "unknown")
	if res.code != 1 || res.doc.Code != "BREAK_OUTCOME" || res.doc.Error != "1 unknown; --fail-on unknown" {
		t.Errorf("unknown verifier: exit %d, code %q, error %q\n%s", res.code, res.doc.Code, res.doc.Error, res.out)
	}
	res = runBreakExit(t, dir, false, "--fails-on-base", "main", "--promise", "fees:INV-1", "--fail-on", "unknown")
	if res.code != 0 || !strings.Contains(res.out, "1 fail on base, 1 pass on base, 0 unknown") {
		t.Errorf("fails_on_base and passes_on_base: exit %d\n%s%s", res.code, res.out, res.err)
	}
	if res := runBreakExit(t, dir, false, "--fails-on-base", "main", "--promise", "web:INV-1"); res.code != 0 {
		t.Errorf("without --fail-on the exit code must be 0, got %d", res.code)
	}
}

// Counts of 100 runs or more print in full: a real project had 651 runs,
// and the line read "651 of q1 runs" through a two-digit helper.
// @verifies breaktest:INV-12
func TestBreakNothingRanLineLargeCounts(t *testing.T) {
	runs := make([]breaktest.Run, 651)
	for i := range runs {
		runs[i] = breaktest.Run{Outcome: breaktest.Unknown, Reason: "no runner"}
	}
	if got, want := nothingRanLine(summarize(runs)), "no break ran: 651 of 651 runs unknown (no runner ×651); break_tested stays unknown"; got != want {
		t.Errorf("line =\n%s\nwant\n%s", got, want)
	}
	for n, want := range map[int]string{1: "1 run", 9: "9 runs", 10: "10 runs", 100: "100 runs", 1234: "1234 runs"} {
		if got := plural(n, "run"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}

// Runs that lack runners for different sets of languages count under one
// reason, and the summary names each missing runner once with the runs that
// need it, so one fix is never listed twice.
// @verifies breaktest:INV-12
func TestBreakSummaryGroupsMissingRunners(t *testing.T) {
	reason := func(langs string) string {
		return "no runner configured for " + langs + " (set break.runners or break.commands in config.yaml)"
	}
	var runs []breaktest.Run
	add := func(n int, r string) {
		for i := 0; i < n; i++ {
			runs = append(runs, breaktest.Run{Outcome: breaktest.Unknown, Reason: r})
		}
	}
	add(344, reason("typescript"))
	add(68, reason("postgres, typescript"))
	add(228, "no generated break for postgres; add a script")
	add(11, "constructor: no generated break; add a script")
	s := summarize(runs)
	if want := []reasonCount{{"no runner configured", 412}, {"no generated break for postgres; add a script", 228}, {"constructor: no generated break; add a script", 11}}; !reflect.DeepEqual(s.UnknownReasons, want) {
		t.Errorf("unknown reasons = %v, want %v", s.UnknownReasons, want)
	}
	if want := []reasonCount{{"typescript", 412}, {"postgres", 68}}; !reflect.DeepEqual(s.MissingRunners, want) {
		t.Errorf("missing runners = %v, want %v", s.MissingRunners, want)
	}
	want := "no break ran: 651 of 651 runs unknown (no runner configured ×412, no generated break for postgres; add a script ×228, constructor: no generated break; add a script ×11); missing runners: typescript ×412, postgres ×68 (set break.runners or break.commands); break_tested stays unknown"
	if got := nothingRanLine(s); got != want {
		t.Errorf("line =\n%s\nwant\n%s", got, want)
	}
	if langs, ok := breaktest.NoRunnerLanguages(reason("postgres, typescript")); !ok || !reflect.DeepEqual(langs, []string{"postgres", "typescript"}) {
		t.Errorf("NoRunnerLanguages = %v, %v", langs, ok)
	}
	if _, ok := breaktest.NoRunnerLanguages("no generated break for postgres; add a script"); ok {
		t.Error("another reason read as a missing runner")
	}
}

// A break run holds lattice/.cache/results/break.lock while it runs and
// removes it after; a second run against a held lock fails without running
// anything and names the holder.
// @verifies breaktest:INV-14
func TestBreakRunLock(t *testing.T) {
	dir := breakFixture(t)
	lock := filepath.Join(dir, "lattice", ".cache", "results", "break.lock")

	if _, out, err := runBreak(t, dir, "--promise", "guard:INV-1"); err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the lock outlived the run: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	held := fmt.Sprintf(`{"pid":%d,"started":"2026-01-02T03:04:05Z"}`, os.Getpid())
	if err := os.WriteFile(lock, []byte(held), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, err := runBreak(t, dir, "--promise", "guard:INV-1")
	if err == nil || !strings.Contains(out, "BREAK_LOCKED") ||
		!strings.Contains(out, fmt.Sprintf("another lattice break is running (pid %d, started 2026-01-02T03:04:05Z)", os.Getpid())) {
		t.Fatalf("a held lock should stop the run: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(lock); string(got) != held {
		t.Errorf("the refused run touched the holder's lock: %q", got)
	}
	if n := worktreeCount(t, dir); n != 1 {
		t.Errorf("%d worktrees: the refused run should not have made one", n)
	}
}
