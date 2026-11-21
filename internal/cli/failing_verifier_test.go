package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// failingRun is the coverage example with an invariant no BRD maps
// (fees:INV-2) and a run where its verifier and the verifier of the mapped
// wallet:INV-2 both fail.
const failingRun = `<?xml version="1.0" encoding="utf-8"?>
<testsuites>
	<testcase name="charges two percent in USD" classname="test"/>
	<testcase name="rejects an overdraft" classname="test"/>
	<testcase name="rejects a non-positive deposit" classname="test"><failure message="no throw"/></testcase>
	<testcase name="formats a statement" classname="test"/>
	<testcase name="never charges a negative fee" classname="test"><failure message="-1"/></testcase>
</testsuites>
`

func failingExample(t *testing.T) (repo, report string) {
	t.Helper()
	repo = copyTree(t, "../../examples/coverage-prototype")
	write := func(rel, s string, appendTo bool) {
		p := filepath.Join(repo, rel)
		if appendTo {
			old, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			s = string(old) + s
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lattice/features/fees.yaml", "  - id: INV-2\n    statement: A fee is never negative.\n", true)
	write("tests/fees.test.js", "\n/** @verifies fees:INV-2 */\ntest(\"never charges a negative fee\", () => {\n  assert.ok(computeFee(0, \"USD\", \"standard\") >= 0);\n});\n", true)
	report = filepath.Join(repo, "coverage", "red.xml")
	write("coverage/red.xml", failingRun, false)
	return repo, report
}

// The ingest names the failed tests after the totals, with the @verifies
// refs of the graph tests they are the results of; --json adds a failed list.
func TestIngestListsFailedTests(t *testing.T) {
	repo, report := failingExample(t)

	var text bytes.Buffer
	io := &IO{Repo: repo, Out: &text, Err: &text, Actor: "test"}
	cmd := newResultsCommand(io)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"ingest", report})
	cmd.SetOut(&text)
	cmd.SetErr(&text)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ingest: %v\n%s", err, text.String())
	}
	for _, want := range []string{
		"ingested 5 test results (3 pass, 2 fail, 0 skip)\nfailed:\n",
		"  test::never charges a negative fee  (@verifies fees:INV-2)\n",
		"  test::rejects a non-positive deposit  (@verifies wallet:INV-2)\n",
	} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("ingest output lacks %q:\n%s", want, text.String())
		}
	}

	out, err := run(t, repo, newResultsCommand, "ingest", report)
	if err != nil {
		t.Fatalf("ingest --json: %v\n%s", err, out)
	}
	var ing struct {
		Fail   int               `json:"fail"`
		Failed []results.Failure `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out), &ing); err != nil || ing.Fail != 2 || len(ing.Failed) != 2 ||
		ing.Failed[0].Case != "test::never charges a negative fee" ||
		strings.Join(ing.Failed[0].Verifies, ",") != "fees:INV-2" {
		t.Errorf("ingest --json: %v\n%s", err, out)
	}
}

// After that ingest, validate raises VERIFIER_FAILING for the unmapped
// invariant and, once, for the mapped one (on its criterion); the audit
// reads the mapped promise as not demonstrated, failing.
// @verifies validation:INV-16
func TestFailingVerifierAcrossSurfaces(t *testing.T) {
	repo, report := failingExample(t)
	if out, err := run(t, repo, newResultsCommand, "ingest", report); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	out, _ := run(t, repo, newValidateCommand)
	var rep validateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	failing := map[string]string{}
	for _, v := range rep.Violations {
		if v.Code == schema.CodeVerifierFailing {
			failing[v.Subject] = v.Message
		}
	}
	if len(failing) != 2 ||
		!strings.Contains(failing["fees:INV-2"], "never-charges-a-negative-fee") ||
		!strings.Contains(failing["brd.wallet:SC-2"], "wallet:INV-2:") {
		t.Errorf("VERIFIER_FAILING findings = %v", failing)
	}

	row := auditRow(t, auditJSON(t, repo), "wallet:INV-2")
	if row.Class != audit.ClassNotDemonstrated || row.Kind != audit.KindFailing {
		t.Errorf("audit wallet:INV-2 = %s/%s, want not_demonstrated/failing", row.Class, row.Kind)
	}
}

// validateFindings runs validate in repo and returns its findings of code by
// subject.
func validateFindings(t *testing.T, repo, code string) map[string]string {
	t.Helper()
	out, _ := run(t, repo, newValidateCommand)
	var rep validateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, v := range rep.Violations {
		if v.Code == code {
			got[v.Subject] = v.Message
		}
	}
	return got
}

// Results ingested one commit before head stay current when the commits in
// between only touch Lattice's own ledger (or nothing at all): validate reads
// them for VERIFIER_FAILING, raises no RESULTS_STALE, and the audit reads
// them as matching head. A commit that changes code makes them stale again.
// @verifies validation:INV-16
// @verifies validation:INV-17
// @verifies audit:INV-9
func TestResultsCurrentAcrossLedgerOnlyCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo, report := failingExample(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("lattice/lattice.json\nlattice/.cache/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "base")
	if out, err := run(t, repo, newResultsCommand, "ingest", report); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}

	check := func(step string, wantCurrent bool) {
		t.Helper()
		stale := validateFindings(t, repo, schema.CodeResultsStale)
		failing := validateFindings(t, repo, schema.CodeVerifierFailing)
		matches := auditJSON(t, repo).Inputs.Results.MatchesHead
		if wantCurrent {
			if len(stale) != 0 {
				t.Errorf("%s: want no RESULTS_STALE, got %v", step, stale)
			}
			msg, ok := failing["fees:INV-2"]
			if !ok || !strings.Contains(msg, "whose tree matches the graph's apart from Lattice's ledger and cache") {
				t.Errorf("%s: want VERIFIER_FAILING on fees:INV-2 naming the equivalent commit, got %v", step, failing)
			}
			if !matches {
				t.Errorf("%s: the audit's inputs.results.matches_head must be true", step)
			}
			return
		}
		if len(stale) != 1 {
			t.Errorf("%s: want one RESULTS_STALE, got %v", step, stale)
		}
		if _, ok := failing["fees:INV-2"]; ok {
			t.Errorf("%s: stale results must not raise VERIFIER_FAILING, got %v", step, failing)
		}
		if matches {
			t.Errorf("%s: the audit's inputs.results.matches_head must be false", step)
		}
	}

	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "empty")
	check("empty commit", true)

	if err := os.MkdirAll(filepath.Join(repo, "lattice", ".ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lattice", ".ledger", "ledger.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "ledger only")
	check("ledger-only commit", true)

	f, err := os.OpenFile(filepath.Join(repo, "tests", "fees.test.js"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n// edited\n")
	_ = f.Close()
	runGit(t, repo, "commit", "-q", "-am", "code")
	check("code commit", false)
}
