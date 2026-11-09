package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/check"
)

// checkRepo is a git repository on branch main with one approved promise, its
// enforcer and its verifier. Tests branch from it.
type checkFixture struct {
	t   *testing.T
	dir string
}

func newCheckFixture(t *testing.T) *checkFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &checkFixture{t: t, dir: t.TempDir()}
	f.git("init", "-q", "-b", "main")
	f.git("config", "user.email", "test@example.com")
	f.git("config", "user.name", "test")
	f.git("config", "maintenance.auto", "false")
	f.git("config", "gc.auto", "0")
	f.write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\n")
	f.write("lattice/adapters.yaml", "adapters:\n  go:\n    enabled: true\n")
	f.write("lattice/features/shop.yaml", shopFeature(1))
	f.write("lattice/brds/brd.shop.yaml", "id: brd.shop\nversion: 1\nstatus: approved\ntitle: Shop\nbusiness_problem: Sell books.\nsuccess_criteria:\n  - id: SC-1\n    statement: A price is positive.\n    maps_to_invariant: shop:INV-1\nimplements_via: [shop]\n")
	f.write("shop/shop.go", shopGo(true))
	f.write("shop/shop_test.go", "package shop\n\nimport \"testing\"\n\n// @verifies shop:INV-1\nfunc TestPriceIsPositive(t *testing.T) {\n\tif !Positive(1) {\n\t\tt.Fatal(\"want positive\")\n\t}\n}\n")
	f.write(".gitignore", "lattice/.cache/\nlattice/lattice.json\nlattice/.ledger/\n")
	f.commit("base")
	return f
}

func shopFeature(n int) string {
	s := "id: shop\nversion: 1\nstatus: production\npurpose: Sell books.\nowners:\n  business: b\n  engineering: e\ninvariants:\n  - id: INV-1\n    statement: A price is positive.\n"
	if n >= 2 {
		s += "  - id: INV-2\n    statement: A discount never exceeds the price.\n"
	}
	return s
}

func shopGo(tagged bool) string {
	tag := ""
	if tagged {
		tag = "// @enforces shop:INV-1\n"
	}
	return "package shop\n\n// @module-feature shop\n\n// Positive guards a price.\n" + tag + "func Positive(n int) bool {\n\treturn n > 0\n}\n"
}

func (f *checkFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *checkFixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *checkFixture) commit(msg string) {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-q", "-m", msg)
}

// check runs `lattice check --json` and returns the report and the exit code.
func (f *checkFixture) check(args ...string) (check.Report, int) {
	f.t.Helper()
	out, err := run(f.t, f.dir, newCheckCommand, args...)
	code := 0
	var ec exitCodeError
	switch {
	case errors.As(err, &ec):
		code = ec.code
	case err != nil:
		f.t.Fatalf("check %v: %v\n%s", args, err, out)
	}
	var rep check.Report
	if code != 2 {
		if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil {
			f.t.Fatalf("check output: %v\n%s", jerr, out)
		}
	}
	return rep, code
}

func clearCIEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"GITHUB_RUN_ID", "CI_PIPELINE_ID", "BUILDKITE_BUILD_ID", "CIRCLE_WORKFLOW_ID", "CI_JOB_ID", "BUILD_ID", "TF_BUILD_BUILDID"} {
		t.Setenv(v, "")
	}
}

// Dropping the @enforces tag reads BROKEN and exits 1, with no base audit at
// all: a structural fall is never missing data.
// @verifies check:INV-1
func TestCheckCLIEnforcerRemovedIsBroken(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)
	f.git("checkout", "-q", "-b", "drop-tag")
	f.write("shop/shop.go", shopGo(false))
	f.commit("drop the tag")

	rep, code := f.check("--base", "main")
	if code != 1 || rep.Verdict.State != check.Broken || rep.MergeAllowed {
		t.Fatalf("exit %d verdict %s merge %v: %+v", code, rep.Verdict.State, rep.MergeAllowed, rep.Weakenings)
	}
	if rep.Contract != check.Contract || rep.Commit != f.git("rev-parse", "HEAD") || rep.Base.SHA != f.git("rev-parse", "main") {
		t.Errorf("header = %s %s base %s", rep.Contract, rep.Commit, rep.Base.SHA)
	}
	found := false
	for _, w := range rep.Weakenings {
		found = found || (w.Code == check.CodeEnforcerRemoved && w.Promise == "shop:INV-1")
	}
	if !found || rep.Evidence.BaseAudit.Present {
		t.Errorf("weakenings %+v base audit %+v", rep.Weakenings, rep.Evidence.BaseAudit)
	}
}

// A new promise and new code under a file tag only: UNPROVEN, the audit
// snapshot of the base is found, the untraced symbol is in the blind spot,
// and the rule blocks the merge.
// @verifies check:INV-5
// @verifies check:INV-3
func TestCheckCLINewPromiseAndUntracedLines(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)

	// The base's audit is saved by a check on the base commit.
	out, err := run(t, f.dir, newCheckCommand, "--snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v\n%s", err, out)
	}
	base := f.git("rev-parse", "HEAD")
	if _, err := os.Stat(check.SnapshotPath(filepath.Join(f.dir, "lattice"), base)); err != nil {
		t.Fatalf("snapshot not written: %v\n%s", err, out)
	}

	f.git("checkout", "-q", "-b", "grow")
	f.write("lattice/features/shop.yaml", shopFeature(2))
	f.write("shop/discount.go", "package shop\n\n// @module-feature shop\n\n// Discount returns the discounted price.\n// @enforces shop:INV-2\nfunc Discount(price, off int) int {\n\tif off > price {\n\t\treturn price\n\t}\n\treturn price - off\n}\n\n// Format renders a price.\nfunc Format(n int) string {\n\treturn \"price\"\n}\n")
	f.commit("add a discount")

	rep, code := f.check("--base", "main")
	if !rep.Evidence.BaseAudit.Present {
		t.Fatalf("the base snapshot was not found: %+v", rep.Evidence.BaseAudit)
	}
	if rep.Verdict.State != check.Unproven || code != 1 || rep.MergeAllowed {
		t.Fatalf("exit %d verdict %s merge %v rule %+v", code, rep.Verdict.State, rep.MergeAllowed, rep.Merge)
	}
	var newPromise bool
	for _, p := range rep.Promises {
		newPromise = newPromise || (p.Promise == "shop:INV-2" && p.NewPromise)
	}
	if !newPromise {
		t.Errorf("shop:INV-2 is not a new touched promise: %+v", rep.Promises)
	}
	if g := rep.BlindSpot; len(g.Untraced) != 1 || g.Untraced[0].Symbol != "shop.Format" || !g.Untraced[0].New ||
		!strings.Contains(g.Untraced[0].Reason, "tag only") {
		t.Errorf("blind spot = %+v", g)
	}
}

// Under --ci, a local ingest is set aside and reads unknown.
// @verifies check:INV-4
func TestCheckCLICIRefusesLocalEvidence(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)
	f.git("checkout", "-q", "-b", "edit")
	f.write("shop/shop.go", strings.Replace(shopGo(true), "n > 0", "n >= 1", 1))
	f.commit("edit")

	junit := filepath.Join(f.dir, "junit.xml")
	if err := os.WriteFile(junit, []byte(`<testsuites><testsuite name="s"><testcase classname="shop" name="TestPriceIsPositive"/></testsuite></testsuites>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, f.dir, newResultsCommand, "ingest", junit); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}

	rep, _ := f.check("--base", "main")
	if !rep.Evidence.Results.Counted || rep.Evidence.Results.Tied || !rep.Evidence.Suite.Present {
		t.Errorf("without --ci a local run is advice: %+v", rep.Evidence.Results)
	}
	rep, code := f.check("--base", "main", "--ci")
	if rep.Evidence.Results.Counted || rep.Evidence.Results.Code != check.CodeEvidenceNotCI || rep.Evidence.Suite.Present {
		t.Fatalf("with --ci: %+v", rep.Evidence)
	}
	if !rep.CI || rep.Verdict.State != check.Unproven || code != 1 {
		t.Errorf("exit %d verdict %s", code, rep.Verdict.State)
	}
	var unknown bool
	for _, g := range rep.Gaps {
		unknown = unknown || (g.Signal == check.SigSuiteGreen && g.State == check.Unknown && g.Code == check.CodeEvidenceNotCI)
	}
	if !unknown {
		t.Errorf("suite_green must read unknown (EVIDENCE_NOT_CI): %+v", rep.Gaps)
	}
}

// Usage errors exit 2, and the Markdown form starts with the marker.
func TestCheckCLIUsageAndMarkdown(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)
	if _, code := f.check(); code != 2 {
		t.Errorf("no --base: exit %d, want 2", code)
	}
	if _, code := f.check("--base", "no-such-rev"); code != 2 {
		t.Errorf("bad --base: exit %d, want 2", code)
	}
	if _, code := f.check("--base", "main", "--markdown"); code != 2 {
		t.Errorf("--markdown with --json: exit %d, want 2", code)
	}

	var out strings.Builder
	io := &IO{Repo: f.dir, Out: &out, Err: &out}
	cmd := newCheckCommand(io)
	cmd.SetArgs([]string{"--base", "main", "--markdown"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("markdown: %v\n%s", err, out.String())
	}
	if !strings.HasPrefix(out.String(), check.Marker+"\n") || !strings.Contains(out.String(), "ASSURED") {
		t.Errorf("markdown:\n%s", out.String())
	}
}
