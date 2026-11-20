package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// feesGraph is a valid feature wallet.fees whose INV-1 has the given cases,
// enforced by wallet.fees.charge and verified by a test carrying refs.
func feesGraph(cases []schema.InvariantCase, refs ...string) schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		Features: []schema.Manifest{{
			ID: "wallet.fees", Version: 1, Status: schema.StatusProduction, SourcePath: "lattice/features/fees.yaml",
			Purpose: "Fees.", Owners: schema.Owners{Business: "b", Engineering: "e"},
			Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A fee never exceeds five percent of the amount.", Cases: cases}},
		}, {
			ID: "wallet", Version: 1, Status: schema.StatusProduction,
			Purpose: "Wallet.", Owners: schema.Owners{Business: "b", Engineering: "e"},
		}},
		Symbols: []schema.GraphSymbol{{FQN: "wallet.fees.charge", Feature: "wallet.fees", EnforcesInvariants: []string{"INV-1"}},
			{FQN: "wallet.fees.format", Feature: "wallet.fees"}},
		Tests: []schema.GraphSymbol{{FQN: "wallet.fees.TestFee", File: "fees_test.go", Line: 4, IsTest: true,
			Verifies: []string{"wallet.fees:INV-1"}, VerifiesCases: refs}},
	}
}

func findings(vs []schema.Violation, code string) []schema.Violation {
	var out []schema.Violation
	for _, v := range vs {
		if v.Code == code {
			out = append(out, v)
		}
	}
	return out
}

var goodCases = []schema.InvariantCase{
	{ID: "a", Kind: schema.CaseHappy, Statement: "A 100.00 withdrawal pays at most 5.00."},
	{ID: "b", Kind: schema.CaseEdge, Statement: "A 0.01 withdrawal pays no fee.", Retired: true},
	{ID: "c", Kind: schema.CaseError, Statement: "A negative amount is rejected first.", Enforcer: "wallet.fees.charge"},
}

// Well-formed cases and refs raise nothing new: a case-tagged verifier is a
// verifier of the invariant, so UNVERIFIED_INVARIANT stays quiet.
func TestValidCasesAreClean(t *testing.T) {
	kg := feesGraph(goodCases, "wallet.fees:INV-1#a", "INV-1#c")
	kg.Tests[0].Feature = "wallet.fees"
	vs := Validate(kg, config.Default(), Options{})
	for _, code := range []string{schema.CodeCaseSchema, schema.CodeCaseIDFormat, schema.CodeCaseIDDuplicate,
		schema.CodeOrphanAnnotationCase, schema.CodeCaseRetiredVerified, schema.CodeCaseEnforcerUnknown,
		schema.CodeUnverifiedInvariant, schema.CodeInvariantBroad} {
		if f := findings(vs, code); len(f) > 0 {
			t.Errorf("unexpected %s: %+v", code, f)
		}
	}
}

// A case with a bad id, a repeated id, an unknown kind or an empty statement
// is an error, with the case as its subject.
// @verifies validation:INV-14#a
func TestCaseSchema(t *testing.T) {
	kg := feesGraph([]schema.InvariantCase{
		{ID: "a", Kind: schema.CaseHappy, Statement: "ok"},
		{ID: "a", Kind: schema.CaseEdge, Statement: "repeated"},
		{ID: "B1", Kind: schema.CaseEdge, Statement: "bad id"},
		{ID: "c", Kind: "sad", Statement: "unknown kind"},
		{ID: "d", Kind: schema.CaseSecurity, Statement: "  "},
	})
	vs := Validate(kg, config.Default(), Options{ReviewMode: true})
	if f := findings(vs, schema.CodeCaseIDDuplicate); len(f) != 1 || f[0].Subject != "wallet.fees:INV-1#a" || !f[0].IsError() {
		t.Errorf("CASE_ID_DUPLICATE = %+v", f)
	}
	if f := findings(vs, schema.CodeCaseIDFormat); len(f) != 1 || !strings.Contains(f[0].Message, `"B1"`) || !f[0].IsError() {
		t.Errorf("CASE_ID_FORMAT = %+v", f)
	}
	f := findings(vs, schema.CodeCaseSchema)
	if len(f) != 2 || !strings.Contains(f[0].Message+f[1].Message, `kind "sad"`) || !strings.Contains(f[0].Message+f[1].Message, "no statement") {
		t.Errorf("CASE_SCHEMA = %+v", f)
	}
}

// A verifier ref to a case the invariant does not declare is an error; a
// ref to a missing invariant stays ORPHAN_ANNOTATION_INVARIANT and is not
// reported twice.
// @verifies validation:INV-14#b
func TestOrphanCaseRef(t *testing.T) {
	kg := feesGraph(goodCases, "wallet.fees:INV-1#z", "wallet.fees:INV-9#a")
	kg.Tests[0].Verifies = append(kg.Tests[0].Verifies, "wallet.fees:INV-9")
	vs := Validate(kg, config.Default(), Options{})
	f := findings(vs, schema.CodeOrphanAnnotationCase)
	if len(f) != 1 || f[0].Subject != "wallet.fees:INV-1#z" || !f[0].IsError() || f[0].Location.File != "fees_test.go" {
		t.Errorf("ORPHAN_ANNOTATION_CASE = %+v", f)
	}
	if len(findings(vs, schema.CodeOrphanAnnotationInvariant)) != 1 {
		t.Errorf("the missing invariant is reported once, as before: %+v", findings(vs, schema.CodeOrphanAnnotationInvariant))
	}
}

// A verifier ref to a retired case is a warning, not an error.
// @verifies validation:INV-14#c
func TestRetiredCaseRef(t *testing.T) {
	vs := Validate(feesGraph(goodCases, "wallet.fees:INV-1#b"), config.Default(), Options{})
	f := findings(vs, schema.CodeCaseRetiredVerified)
	if len(f) != 1 || f[0].Severity != schema.SeverityWarning || f[0].Subject != "wallet.fees:INV-1#b" {
		t.Errorf("CASE_RETIRED_VERIFIED = %+v", f)
	}
}

// A case enforcer must be a symbol that enforces the case's invariant: an
// unknown symbol and a symbol of the feature that enforces nothing are both
// errors naming the invariant's real enforcers.
// @verifies validation:INV-14#d
func TestCaseEnforcer(t *testing.T) {
	cases := []schema.InvariantCase{
		{ID: "a", Kind: schema.CaseHappy, Statement: "ok", Enforcer: "wallet.fees.charge"},
		{ID: "b", Kind: schema.CaseError, Statement: "unknown symbol", Enforcer: "wallet.fees.nope"},
		{ID: "c", Kind: schema.CaseError, Statement: "not an enforcer", Enforcer: "wallet.fees.format"},
	}
	f := findings(Validate(feesGraph(cases), config.Default(), Options{}), schema.CodeCaseEnforcerUnknown)
	if len(f) != 2 || f[0].Subject != "wallet.fees:INV-1#b" || f[1].Subject != "wallet.fees:INV-1#c" ||
		!strings.Contains(f[0].Message, "its enforcers are wallet.fees.charge") || !f[0].IsError() {
		t.Errorf("CASE_ENFORCER_UNKNOWN = %+v", f)
	}
}

// INVARIANT_BROAD names an invariant with no cases whose statement is long
// and makes several claims, in two sentences or in three sentences or
// semicolon clauses that each have a verb, or is very long however it is
// punctuated. A shorter multi-sentence statement, a long single claim under
// the length bound, and a short rule with its exception stay quiet.
// @verifies validation:INV-15
func TestInvariantBroad(t *testing.T) {
	omnibus := "A withdrawal fee is charged once per withdrawal and never exceeds five percent of the amount, rounded " +
		"down to the cent; a refund returns the fee in full and is recorded against the original withdrawal on the " +
		"same day; a reversed refund charges the fee again, and the ledger shows both entries with the same reference and the same date."
	twoSentences := "An order is charged its fee once, when the payment settles, in the currency of the order and at " +
		"the rate that applied when the order was placed. A refunded order returns the full fee to the wallet it came " +
		"from, on the same day as the refund, and the statement for that month lists both the charge and the return."
	threeClaims := "A wallet balance is never negative at the end of a transaction, whatever the order of the debits " +
		"in it; a debit larger than the balance is refused with the shortfall named in the error and the account " +
		"flagged for review; a refused debit records no ledger entry and leaves the balance exactly as it was before the debit was tried."
	veryLong := "A fee is computed from the amount " + strings.Repeat("and the currency ", 22) + "of the withdrawal."
	longClaim := "A fee is computed from the amount " + strings.Repeat("and the currency ", 18) + "of the withdrawal."
	mediumTwo := "An order is charged its fee once, when the payment settles, in the currency of the order. " +
		"A refunded order returns the full fee to the wallet it came from, on the same day as the refund."
	short := "A fee is never negative; it is never charged twice."
	shortTwo := "An order is charged once. A refund returns the fee."
	for name, tc := range map[string]struct {
		statement string
		cases     []schema.InvariantCase
		fires     bool
	}{
		"several clauses":      {omnibus, nil, true},
		"two sentences":        {twoSentences, nil, true},
		"three semicolons":     {threeClaims, nil, true},
		"very long, one claim": {veryLong, nil, true},
		"long single claim":    {longClaim, nil, false},
		"with cases":           {omnibus, goodCases[:1], false},
		"two medium sentences": {mediumTwo, nil, false},
		"two short":            {short, nil, false},
		"two short sentences":  {shortTwo, nil, false},
	} {
		kg := feesGraph(tc.cases)
		kg.Features[0].Invariants[0].Statement = tc.statement
		f := findings(Validate(kg, config.Default(), Options{ReviewMode: true}), schema.CodeInvariantBroad)
		if tc.fires != (len(f) == 1) {
			sentences, n := claims(tc.statement)
			t.Errorf("%s: INVARIANT_BROAD = %+v, want fires=%v (%d sentences, %d claims, %d words)", name, f, tc.fires,
				sentences, n, len(strings.Fields(tc.statement)))
			continue
		}
		if tc.fires && (f[0].Severity != schema.SeverityInfo || f[0].NextAction == nil ||
			!strings.Contains(f[0].NextAction.Detail, "add cases") || !strings.Contains(f[0].NextAction.Detail, "split")) {
			t.Errorf("%s: %+v", name, f[0])
		}
	}
}
