package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
