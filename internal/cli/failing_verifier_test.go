package cli

import (
	"bytes"
	"encoding/json"
	"os"
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
