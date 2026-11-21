package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/contract"
)

// The feature drawer's promise carries its cases with the audit's coverage.
// The summary is a pass only when every live case is covered, fails on a
// failing case, and is unknown otherwise; the payload passes the contract
// lint either way. An invariant without cases keeps NOT_COMPUTED.
// @verifies api:INV-2
func TestPromiseCases(t *testing.T) {
	row := audit.Row{Promise: "f:INV-1", Feature: "f", Statement: "s", Tier: 1, Class: audit.ClassBreakTested}
	if p := (&v2world{}).promiseOf(row, nil); p.Cases.Code != contract.CodeNotComputed || p.CaseRows != nil {
		t.Errorf("no cases: %+v %+v", p.Cases, p.CaseRows)
	}

	covered := audit.CaseRow{ID: "a", Label: "1a", Kind: "happy", Statement: "x", Coverage: audit.CaseCovered, Verifiers: []string{"f.T"}}
	retired := audit.CaseRow{ID: "b", Label: "1b", Kind: "edge", Statement: "y", Coverage: audit.CaseUncovered, Retired: true}
	partial := audit.CaseRow{ID: "c", Label: "1c", Kind: "error", Statement: "z", Coverage: audit.CasePartial}
	failing := audit.CaseRow{ID: "d", Label: "1d", Kind: "security", Statement: "w", Coverage: audit.CaseFailing}
	for name, tc := range map[string]struct {
		cases []audit.CaseRow
		state string
		code  string
	}{
		"all live covered": {[]audit.CaseRow{covered, retired}, contract.StatePass, "CASES_COVERED"},
		"one partial":      {[]audit.CaseRow{covered, partial}, contract.StateUnknown, "CASES_NOT_COVERED"},
		"one failing":      {[]audit.CaseRow{covered, partial, failing}, contract.StateFail, "CASES_FAILING"},
		"all retired":      {[]audit.CaseRow{retired}, contract.StateUnknown, "CASES_ALL_RETIRED"},
	} {
		r := row
		r.Cases = tc.cases
		p := (&v2world{}).promiseOf(r, nil)
		if p.Cases.State != tc.state || p.Cases.Code != tc.code || len(p.CaseRows) != len(tc.cases) {
			t.Errorf("%s: cases = %+v rows %d", name, p.Cases, len(p.CaseRows))
		}
		if p.CaseRows[0].Label != tc.cases[0].Label || p.CaseRows[0].Coverage != tc.cases[0].Coverage || p.CaseRows[0].Verifiers == nil {
			t.Errorf("%s: first row = %+v", name, p.CaseRows[0])
		}
		// The hand-built row has no signals; lint the part this adds.
		part, _ := json.Marshal(map[string]interface{}{"cases": p.Cases, "case_rows": p.CaseRows})
		if bad, err := contract.Lint(part, contract.LintOptions{}); err != nil || len(bad) > 0 {
			t.Errorf("%s: lint %v %v", name, bad, err)
		}
		if body, _ := json.Marshal(p); !strings.Contains(string(body), `"case_rows":[`) {
			t.Errorf("%s: payload = %s", name, body)
		}
	}
}

// Each case row carries the audit's confirmation state: pass only for a
// confirmed case, drifted for one edited or retired since, unknown for one
// added since, not recorded or never confirmed, and unknown for a row the
// audit gave no state. Every one passes the contract lint.
// @verifies api:INV-2
// @verifies confirm:INV-20
func TestPromiseCaseConfirmation(t *testing.T) {
	row := audit.Row{Promise: "f:INV-1", Feature: "f", Statement: "s", Tier: 1, Class: audit.ClassBreakTested}
	for _, tc := range []struct {
		conf  audit.CaseConfirmation
		state string
	}{
		{audit.CaseConfirmation{State: audit.CaseConfirmed, Code: audit.CaseCodeConfirmed, Reason: "as confirmed"}, contract.StatePass},
		{audit.CaseConfirmation{State: audit.CaseUnconfirmed, Code: audit.CaseCodeEdited, Reason: "edited"}, contract.StateDrifted},
		{audit.CaseConfirmation{State: audit.CaseUnconfirmed, Code: audit.CaseCodeRetired, Reason: "retired"}, contract.StateDrifted},
		{audit.CaseConfirmation{State: audit.CaseUnconfirmed, Code: audit.CaseCodeAdded, Reason: "added"}, contract.StateUnknown},
		{audit.CaseConfirmation{State: audit.CaseUnconfirmed, Code: audit.CaseCodeNotRecorded, Reason: "old"}, contract.StateUnknown},
		{audit.CaseConfirmation{State: audit.CaseUnconfirmed, Code: audit.CaseCodeNeverConfirmed, Reason: "none"}, contract.StateUnknown},
		{audit.CaseConfirmation{}, contract.StateUnknown},
	} {
		r := row
		r.Cases = []audit.CaseRow{{ID: "a", Label: "1a", Kind: "happy", Statement: "x", Coverage: audit.CaseCovered, Confirmation: tc.conf}}
		p := (&v2world{}).promiseOf(r, nil)
		got := p.CaseRows[0].Confirmation
		if got.State != tc.state || got.Code == "" || got.Honesty != contract.Proxy {
			t.Errorf("%+v: confirmation = %+v", tc.conf, got)
		}
		part, _ := json.Marshal(map[string]interface{}{"case_rows": p.CaseRows})
		if bad, err := contract.Lint(part, contract.LintOptions{}); err != nil || len(bad) > 0 {
			t.Errorf("%+v: lint %v %v", tc.conf, bad, err)
		}
	}
}
