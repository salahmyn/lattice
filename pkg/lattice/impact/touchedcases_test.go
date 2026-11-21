package impact

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const casesFeature = `id: fees
version: 1
status: production
purpose: Charges a fee on each payment.
owners:
  business: payments
  engineering: payments-eng
invariants:
  - id: INV-1
    statement: A fee never exceeds the cap.
    cases:
      - id: a
        kind: happy
        statement: A fee of 500 is capped at 100.
      - id: b
        kind: error
        statement: A negative fee is refused first.
        enforcer: fees.Check
      - id: c
        kind: edge
        statement: A fee of 100 stays 100.
`

const casesCode = `package fees

// @module-feature fees

// Cap caps the fee.
// @enforces fees:INV-1
func Cap(fee int) int {
	if fee > 100 {
		return 100
	}
	return fee
}

// Check refuses a negative fee.
// @enforces fees:INV-1
func Check(fee int) bool {
	return fee >= 0
}
`

const casesTest = `package fees

// @module-feature fees

import "testing"

// @verifies fees:INV-1#a
func TestCapHigh(t *testing.T) {
	if Cap(500) != 100 {
		t.Fatal("cap")
	}
}

// @verifies INV-1#c
func TestCapEdge(t *testing.T) {
	if Cap(100) != 100 {
		t.Fatal("edge")
	}
}
`

// impact names the cases a change touches: the case whose own enforcer
// changed, and the cases a changed test is tagged with. A change to an
// enforcer the case does not name (the invariant's, here fees.Cap) touches
// the promise, not case a or c.
// @verifies impact:INV-10
func TestImpactNamesTouchedCases(t *testing.T) {
	r := newRepo(t)
	r.write("lattice/features/fees.yaml", casesFeature)
	r.write("fees/fees.go", casesCode)
	r.write("fees/fees_test.go", casesTest)
	base := r.commit("base")
	r.git("checkout", "-q", "-b", "change")

	// Only the invariant's enforcer changes: no case is touched.
	r.write("fees/fees.go", strings.Replace(casesCode, "fee > 100", "fee >= 101", 1))
	r.commit("cap")
	if rep := r.report(r.input(base)); len(rep.TouchedCases) != 0 {
		t.Fatalf("a change to the invariant's enforcer touches no case: %+v", rep.TouchedCases)
	}

	// The case's own enforcer and a case-tagged test change.
	r.write("fees/fees.go", strings.Replace(strings.Replace(casesCode, "fee > 100", "fee >= 101", 1), "fee >= 0", "fee > -1", 1))
	r.write("fees/fees_test.go", strings.Replace(casesTest, "Cap(100) != 100", "Cap(100) != 100 || Cap(99) != 99", 1))
	r.commit("check and test")
	rep := r.report(r.input(base))
	var got []string
	for _, c := range rep.TouchedCases {
		got = append(got, c.Label+" "+c.Promise+" "+c.Why+" "+c.Symbol+" "+c.Change)
	}
	want := []string{
		"1b fees:INV-1 enforcer_changed fees.Check modified",
		"1c fees:INV-1 verifier_changed fees.TestCapEdge modified",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("touched cases:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	body, _ := json.Marshal(rep)
	if !strings.Contains(string(body), `"touched_cases":[{"promise":"fees:INV-1","case":"b","label":"1b"`) {
		t.Errorf("json: %s", body)
	}
	var text, md bytes.Buffer
	Text(&text, rep)
	Markdown(&md, rep)
	if !strings.Contains(text.String(), "TOUCHED CASES") || !strings.Contains(text.String(), "enforcer fees.Check modified") {
		t.Errorf("text:\n%s", text.String())
	}
	if !strings.Contains(md.String(), "### Touched cases") || !strings.Contains(md.String(), "verifier fees.TestCapEdge modified") {
		t.Errorf("markdown:\n%s", md.String())
	}
}
