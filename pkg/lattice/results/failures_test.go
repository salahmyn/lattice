package results

import (
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Failures names each failed case with the @verifies refs of the graph
// tests it is the result of: a plain test, a case verifier (its case ref
// stands in for the invariant ref), and a parameterized test, whose red
// expansion fails it. A failed case no tagged test claims has no refs, and a
// passing tagged test adds nothing.
func TestFailuresNameVerifiedRefs(t *testing.T) {
	set := junitOf(t, "tests/fees.spec.ts", map[string]string{
		"fees › rounds to the cent":  "fail",
		"fees › zero amount":         "fail",
		"fees › caps at 5 percent":   "pass",
		"fees › converts USD to EUR": "fail",
		"fees › converts EUR to USD": "pass",
		"fees › prints a receipt":    "fail",
	})
	graph := []schema.GraphSymbol{
		{FQN: "tests.fees.spec.test.rounds-to-the-cent", IsTest: true, Feature: "fees", Verifies: []string{"INV-1"}},
		{FQN: "tests.fees.spec.test.zero-amount", IsTest: true, Feature: "fees",
			Verifies: []string{"fees:INV-3"}, VerifiesCases: []string{"fees:INV-3#a"}},
		{FQN: "tests.fees.spec.test.caps-at-5-percent", IsTest: true, Feature: "fees", Verifies: []string{"fees:INV-2"}},
		{FQN: "tests.fees.spec.test.converts-_-to-_", IsTest: true, Feature: "fees", Verifies: []string{"fees:INV-4"}},
		{FQN: "tests.fees.spec.test.prints-a-receipt", IsTest: true, Feature: "fees"},
	}
	got := set.WithTests(graph).Failures(graph)
	want := []Failure{
		{Case: "tests/fees.spec.ts::fees › converts USD to EUR", Verifies: []string{"fees:INV-4"}},
		{Case: "tests/fees.spec.ts::fees › prints a receipt"},
		{Case: "tests/fees.spec.ts::fees › rounds to the cent", Verifies: []string{"fees:INV-1"}},
		{Case: "tests/fees.spec.ts::fees › zero amount", Verifies: []string{"fees:INV-3#a"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Failures =\n%+v\nwant\n%+v", got, want)
	}
	if green := junitOf(t, "tests/fees.spec.ts", map[string]string{"fees › caps at 5 percent": "pass"}); len(green.Failures(graph)) != 0 {
		t.Errorf("a green run has no failures")
	}
}
