package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A vitest file whose verifiers are parameterized tests (a template literal
// in a loop, test.each, two templates that both fit one name), and a JUnit
// report of their expanded names, through the CLI: the RTM demonstrates the
// invariant whose every expansion passed, fails the one with a red
// expansion, does not count the ambiguous one, validate names it, and the
// test list selects each by a pattern over its expansions.
// @verifies results:INV-8, adapters.typescript:INV-6
func TestParameterizedTestsCLI(t *testing.T) {
	clearCIEnv(t)
	repo := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\n")
	write("lattice/adapters.yaml", "adapters:\n  typescript:\n    enabled: true\n")
	write("lattice/features/fees.yaml", "id: fees\nversion: 1\nstatus: production\npurpose: Charge fees.\n"+
		"owners:\n  business: b\n  engineering: e\ninvariants:\n"+
		"  - id: INV-1\n    statement: A fee is charged per item.\n"+
		"  - id: INV-2\n    statement: A fee rounds half up.\n"+
		"  - id: INV-3\n    statement: A cart fee is capped.\n")
	write("lattice/brds/brd.fees.yaml", "id: brd.fees\nversion: 1\nstatus: draft\ntitle: Fees\nbusiness_problem: Charge fair fees.\n"+
		"success_criteria:\n"+
		"  - id: SC-1\n    statement: A fee is charged per item.\n    maps_to_invariant: fees:INV-1\n"+
		"  - id: SC-2\n    statement: A fee rounds half up.\n    maps_to_invariant: fees:INV-2\n"+
		"  - id: SC-3\n    statement: A cart fee is capped.\n    maps_to_invariant: fees:INV-3\n"+
		"implements_via: [fees]\n")
	write("src/cart/fees.ts", "// @module-feature fees\n\n/** @enforces fees:INV-1, fees:INV-2, fees:INV-3 */\n"+
		"export function fee(n: number): number {\n  return Math.round(n);\n}\n")
	write("src/cart/fees.test.ts", "import { describe, test } from 'vitest';\n"+
		"import { fee } from './fees';\n\n"+
		"describe('fees', () => {\n"+
		"  for (const n of [1, 2]) {\n"+
		"    /** @verifies fees:INV-1 */\n"+
		"    test(`charges ${n} fees`, () => { fee(n); });\n"+
		"  }\n"+
		"  /** @verifies fees:INV-2 */\n"+
		"  test.each([[1.5, 2], [2.5, 3]])('rounds %s to %s', (a, b) => { fee(a); });\n"+
		"  /** @verifies fees:INV-3 */\n"+
		"  test.each([5])('caps fees at %s in cart', (n) => { fee(n); });\n"+
		"  test.each([['5', 'cart']])('caps fees at %s in %s', (n, kind) => { fee(1); });\n"+
		"});\n")
	write("junit.xml", `<testsuites><testsuite name="src/cart/fees.test.ts">
<testcase classname="src/cart/fees.test.ts" name="fees &gt; charges 1 fees"/>
<testcase classname="src/cart/fees.test.ts" name="fees &gt; charges 2 fees"/>
<testcase classname="src/cart/fees.test.ts" name="fees &gt; rounds 1.5 to 2"/>
<testcase classname="src/cart/fees.test.ts" name="fees &gt; rounds 2.5 to 3"><failure message="expected 3"/></testcase>
<testcase classname="src/cart/fees.test.ts" name="fees &gt; caps fees at 5 in cart"/>
</testsuite></testsuites>`)

	if out, err := run(t, repo, newResultsCommand, "ingest", filepath.Join(repo, "junit.xml")); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	out, err := run(t, repo, newRTMCommand)
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
	if status["SC-1"] != rtm.StatusDemonstrated || status["SC-2"] != rtm.StatusFailing || status["SC-3"] != rtm.StatusVerified {
		t.Errorf("rtm statuses = %v, want SC-1 demonstrated, SC-2 failing, SC-3 verified (ambiguous result not counted)", status)
	}

	out, _ = run(t, repo, newValidateCommand)
	var rep validateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	var ambiguous []string
	for _, v := range rep.Violations {
		if v.Code == schema.CodeResultAmbiguous {
			ambiguous = append(ambiguous, v.Subject)
		}
	}
	if len(ambiguous) != 1 || ambiguous[0] != "src.cart.fees.test.test.caps-fees-at-_-in-cart" {
		t.Errorf("RESULT_AMBIGUOUS subjects = %v", ambiguous)
	}

	out, err = run(t, repo, newResultsCommand, "tests", "--verifiers")
	if err != nil {
		t.Fatalf("results tests: %v\n%s", err, out)
	}
	var list struct {
		Tests []results.Selector `json:"tests"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("results tests output: %v\n%s", err, out)
	}
	filters := map[string]string{}
	for _, s := range list.Tests {
		filters[s.Test] = strings.Join(s.Filters["vitest"], " ")
	}
	if got := filters["src.cart.fees.test.test.charges-_-fees"]; got != `-t charges .+? fees$` {
		t.Errorf("vitest filter = %q (all %v)", got, filters)
	}
}
