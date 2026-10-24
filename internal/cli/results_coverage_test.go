package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// copyTree copies the example into a scratch directory, so a run writes no
// cache, ledger or graph into the repository (and runs outside git, so the
// commit stamp and dirty flag are empty and stable).
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if d.Name() == ".cache" || d.Name() == ".ledger" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if d.Name() == "lattice.json" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// run executes one command against the workspace and returns its output.
func run(t *testing.T, repo string, mk func(*IO) *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	io := &IO{Repo: repo, JSON: true, Out: &out, Err: &out, Actor: "test"}
	cmd := mk(io)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

// The coverage example end to end through the CLI: ingest the run's JUnit,
// suite LCOV and per-test LCOV in one call, then rtm and validate. The
// stubbed verifier misses its enforcer, the honest ones are exercised, an
// exercised invariant is below its floor, and the tier-3 criterion that is
// only demonstrated fails require_level.
func TestCoverageExampleCLI(t *testing.T) {
	repo := copyTree(t, "../../examples/coverage-prototype")
	abs := func(p string) string { return filepath.Join(repo, p) }

	out, err := run(t, repo, newResultsCommand, "ingest", abs("coverage/junit.xml"), abs("coverage/all.lcov"),
		"--per-test", abs("coverage/per-test"))
	if err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	var ing struct {
		Pass     int `json:"pass"`
		Coverage struct {
			PerTest int `json:"per_test"`
			Skipped int `json:"non_verifiers_skipped"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(out), &ing); err != nil || ing.Pass != 4 || ing.Coverage.PerTest != 3 || ing.Coverage.Skipped != 1 {
		t.Fatalf("ingest output: %v\n%s", err, out)
	}

	out, err = run(t, repo, newRTMCommand)
	if err != nil {
		t.Fatalf("rtm: %v\n%s", err, out)
	}
	var m struct {
		Matrix rtm.Matrix `json:"matrix"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	status := map[string]rtm.Status{}
	for _, r := range m.Matrix.Rows {
		status[r.CriterionID] = r.Status
	}
	if status["SC-1"] != rtm.StatusDemonstrated || status["SC-2"] != rtm.StatusExercised || status["SC-3"] != rtm.StatusExercised {
		t.Errorf("rtm statuses = %v", status)
	}

	out, err = run(t, repo, newValidateCommand)
	if err == nil {
		t.Errorf("validate should fail: SC-1 is tier 3 and only demonstrated")
	}
	var rep validateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	var got []string
	for _, v := range rep.Violations {
		switch v.Code {
		case schema.CodeVerifierMissesEnforcer, schema.CodeCoverageBelowFloor, schema.CodeBRDBelowLevel,
			schema.CodeCoverageStale, schema.CodeVerifierUnproven:
			got = append(got, string(v.Severity)+" "+v.Code+" "+v.Subject)
		}
	}
	sort.Strings(got)
	want := []string{
		"error BRD_BELOW_LEVEL brd.wallet:SC-1",
		"warning COVERAGE_BELOW_FLOOR brd.wallet:SC-1",
		"warning COVERAGE_BELOW_FLOOR brd.wallet:SC-3",
		"warning COVERAGE_BELOW_FLOOR fees:INV-1",
		"warning COVERAGE_BELOW_FLOOR wallet",
		"warning COVERAGE_BELOW_FLOOR wallet:INV-1",
		"warning VERIFIER_MISSES_ENFORCER wallet:INV-1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("validate coverage findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// With no paths, ingest reads config.yaml coverage: (suite + per-test
	// dirs) and leaves the test results in place.
	if out, err := run(t, repo, newResultsCommand, "ingest"); err != nil || !strings.Contains(out, `"per_test": 3`) {
		t.Errorf("ingest from config: %v\n%s", err, out)
	}
	if out, _ := run(t, repo, newRTMCommand); !strings.Contains(out, `"status": "exercised"`) {
		t.Errorf("results kept after a coverage-only ingest:\n%s", out)
	}

	// The helper lists the three verifiers, with the slug each report uses.
	out, err = run(t, repo, newResultsCommand, "tests", "--verifiers", "--runner", "node")
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"rejects-an-overdraft", "rejects-a-non-positive-deposit", "charges-two-percent-in-usd"} {
		if _, err := os.Stat(abs("coverage/per-test/" + slug + ".lcov")); err != nil || !strings.Contains(out, `"slug": "`+slug+`"`) {
			t.Errorf("slug %s: %v\n%s", slug, err, out)
		}
	}
	if strings.Contains(out, "formats-a-statement") {
		t.Error("--verifiers lists only @verifies tests")
	}
}
