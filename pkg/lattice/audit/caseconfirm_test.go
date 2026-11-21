package audit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// stampsOf records the cases of feeGraph as a decision would, then lets the
// test change them to what the confirmed packet held.
func stampsOf(edit func(cs []schema.CaseStamp) []schema.CaseStamp) []schema.PromiseCases {
	var cs []schema.CaseStamp
	for _, c := range feeGraph(nil).Features[0].Invariants[0].Cases {
		cs = append(cs, schema.NewCaseStamp(c.ID, c.Kind, c.Statement, c.Enforcer, c.Retired))
	}
	if edit != nil {
		cs = edit(cs)
	}
	return []schema.PromiseCases{{Promise: "f:INV-1", Cases: cs}}
}

func caseRowsWith(t *testing.T, cd *ConfirmData, change func(kg *schema.KnowledgeGraph)) Row {
	t.Helper()
	kg := feeGraph(map[string][]string{"f.TestHundred": {"f:INV-1#a"}})
	kg.BRDs = []schema.BRD{brdFor("b", schema.BRDDraft, 2, nil)}
	if change != nil {
		change(&kg)
	}
	return rowOf(t, Compute(Sources{KG: kg, Results: pass("f.TestHundred"), Confirm: cd}), "f:INV-1")
}

// approvedWith is a confirm.json with one approval of b v2 whose case list
// lattice confirm verified against the packet.
func approvedWith(cases []schema.PromiseCases, state string) *ConfirmData {
	check := ""
	if cases != nil {
		check = "verified"
	}
	return &ConfirmData{Records: []ConfirmRecord{{ID: "b", Version: 2, Action: "brd.approve", State: state,
		ApproverLogin: "ana", PR: 7, Cases: cases, CasesCheck: check}}}
}

// Unchanged cases under a current, confirmed decision stay confirmed, with
// the decision behind them named, and the promise reads human as before:
// per-case state changes nothing about the promise's signal.
// @verifies confirm:INV-20#c
func TestCaseUnchangedStaysConfirmed(t *testing.T) {
	r := caseRowsWith(t, approvedWith(stampsOf(nil), "confirmed"), nil)
	if r.Signals.Confirmed.Value != ConfirmedHuman {
		t.Fatalf("promise = %s", r.Signals.Confirmed.Value)
	}
	for _, c := range r.Cases {
		if c.Confirmation.State != CaseConfirmed || c.Confirmation.Code != CaseCodeConfirmed ||
			!strings.Contains(c.Confirmation.Reason, "approved by ana on PR #7") {
			t.Errorf("%s = %+v", c.Label, c.Confirmation)
		}
	}
}

// An unchanged case is confirmed only under a current, confirmed decision
// for the BRD's current version. Under a stale decision (its packet changed)
// or one of an earlier version it reads unconfirmed, CASE_DECISION_STALE,
// while a case changed since still names its change.
// @verifies confirm:INV-20#e
func TestCaseUnderStaleDecisionIsUnconfirmed(t *testing.T) {
	older := approvedWith(stampsOf(nil), "confirmed")
	older.Records[0].Version = 1
	for name, cd := range map[string]*ConfirmData{"stale": approvedWith(stampsOf(nil), "stale"), "earlier version": older} {
		r := caseRowsWith(t, cd, nil)
		if r.Signals.Confirmed.Value != ConfirmedDrifted {
			t.Errorf("%s: promise = %s", name, r.Signals.Confirmed.Value)
		}
		for _, c := range r.Cases {
			if c.Confirmation.State != CaseUnconfirmed || c.Confirmation.Code != CaseCodeDecisionStale ||
				!strings.Contains(c.Confirmation.Reason, "unchanged since the last confirmation") ||
				!strings.Contains(c.Confirmation.Reason, "the promise needs confirming again") {
				t.Errorf("%s: %s = %+v", name, c.Label, c.Confirmation)
			}
		}
	}
	held := stampsOf(func(cs []schema.CaseStamp) []schema.CaseStamp { return cs[:2] })
	if c := caseOf(t, caseRowsWith(t, approvedWith(held, "stale"), nil), "c"); c.Confirmation.Code != CaseCodeAdded {
		t.Errorf("a case added since still says so: %+v", c.Confirmation)
	}
}

// A case the confirmed decision did not hold reads unconfirmed, added after
// confirmation, even while the promise still reads human (confirm.json was
// computed before the case was added).
// @verifies confirm:INV-20#a
func TestCaseAddedAfterConfirmation(t *testing.T) {
	held := stampsOf(func(cs []schema.CaseStamp) []schema.CaseStamp { return cs[:2] })
	r := caseRowsWith(t, approvedWith(held, "confirmed"), nil)
	c := caseOf(t, r, "c")
	if c.Confirmation.State != CaseUnconfirmed || c.Confirmation.Code != CaseCodeAdded ||
		!strings.Contains(c.Confirmation.Reason, "added after confirmation") {
		t.Errorf("c = %+v", c.Confirmation)
	}
	if a := caseOf(t, r, "a"); a.Confirmation.State != CaseConfirmed {
		t.Errorf("a = %+v", a.Confirmation)
	}
	if r.Signals.Confirmed.Value != ConfirmedHuman {
		t.Errorf("the promise signal keeps its meaning: %s", r.Signals.Confirmed.Value)
	}
}

// A case whose kind, statement or enforcer changed since the decision
// recorded it reads edited after confirmation and names what changed; a
// newly retired case reads retired after confirmation.
// @verifies confirm:INV-20#b
func TestCaseEditedAfterConfirmation(t *testing.T) {
	held := stampsOf(func(cs []schema.CaseStamp) []schema.CaseStamp {
		cs[1] = schema.NewCaseStamp("b", schema.CaseHappy, cs[1].Statement, "", false)
		cs[2] = schema.NewCaseStamp("c", schema.CaseError, "A negative amount is refused.", "f.Validate", false)
		return cs
	})
	r := caseRowsWith(t, approvedWith(held, "confirmed"), func(kg *schema.KnowledgeGraph) {
		kg.Features[0].Invariants[0].Cases[0].Retired = true
	})
	b, c := caseOf(t, r, "b"), caseOf(t, r, "c")
	if b.Confirmation.Code != CaseCodeEdited || !strings.Contains(b.Confirmation.Reason, "edited after confirmation: kind happy->edge") {
		t.Errorf("b = %+v", b.Confirmation)
	}
	if c.Confirmation.Code != CaseCodeEdited || !strings.Contains(c.Confirmation.Reason, "statement") {
		t.Errorf("c = %+v", c.Confirmation)
	}
	if a := caseOf(t, r, "a"); a.Confirmation.State != CaseUnconfirmed || a.Confirmation.Code != CaseCodeRetired {
		t.Errorf("a = %+v", a.Confirmation)
	}

	// A stamp whose hash does not match its own fields never reads unchanged.
	forged := stampsOf(func(cs []schema.CaseStamp) []schema.CaseStamp { cs[0].Hash = "sha256:00"; return cs })
	if a := caseOf(t, caseRowsWith(t, approvedWith(forged, "confirmed"), nil), "a"); a.Confirmation.State != CaseUnconfirmed {
		t.Errorf("forged stamp: a = %+v", a.Confirmation)
	}
}

// A decision prepared before decisions recorded their cases still reads as
// it did: the promise is human, and every case reads unconfirmed with a
// reason that says why and what to do. With no confirmation at all, every
// case reads unconfirmed, never confirmed.
// @verifies confirm:INV-20#d
func TestCaseOldDecisionReadsUnconfirmed(t *testing.T) {
	r := caseRowsWith(t, approvedWith(nil, "confirmed"), nil)
	if r.Signals.Confirmed.Value != ConfirmedHuman {
		t.Fatalf("an old decision must still confirm the promise: %s", r.Signals.Confirmed.Value)
	}
	for _, c := range r.Cases {
		if c.Confirmation.State != CaseUnconfirmed || c.Confirmation.Code != CaseCodeNotRecorded ||
			!strings.Contains(c.Confirmation.Reason, "before decisions recorded their cases") {
			t.Errorf("%s = %+v", c.Label, c.Confirmation)
		}
	}

	for name, cd := range map[string]*ConfirmData{
		"no confirm data": nil,
		"no record":       {},
		"set aside":       approvedWith(stampsOf(nil), "confirmed").setAside("CONFIRM_TAMPERED", "digest"),
		"returned": {Records: []ConfirmRecord{{ID: "b", Version: 2, Action: "brd.approve", State: "confirmed", ApproverLogin: "ana", PR: 7, Cases: stampsOf(nil)},
			{ID: "b", Version: 2, Action: "brd.return", State: "confirmed", ApproverLogin: "ana", PR: 8}}},
	} {
		for _, c := range caseRowsWith(t, cd, nil).Cases {
			if c.Confirmation.State != CaseUnconfirmed || c.Confirmation.Code != CaseCodeNeverConfirmed {
				t.Errorf("%s: %s = %+v", name, c.Label, c.Confirmation)
			}
		}
	}

	// The state is on the audit row's case entries.
	body, _ := json.Marshal(r.Cases[0])
	if !strings.Contains(string(body), `"confirmation":{"state":"unconfirmed","code":"CASE_NOT_RECORDED"`) {
		t.Errorf("json = %s", body)
	}
}

// A case list lattice confirm found to contradict the confirmed packet, or
// never checked, confirms no case, even one that matches it.
// @verifies confirm:INV-20#f
func TestCaseStampMustBeBoundToThePacket(t *testing.T) {
	for check, code := range map[string]string{"mismatch": CaseCodeStampMismatch, "": CaseCodeStampUnverified} {
		cd := approvedWith(stampsOf(nil), "confirmed")
		cd.Records[0].CasesCheck = check
		r := caseRowsWith(t, cd, nil)
		if r.Signals.Confirmed.Value != ConfirmedHuman {
			t.Errorf("%q: the promise signal keeps its meaning: %s", check, r.Signals.Confirmed.Value)
		}
		for _, c := range r.Cases {
			if c.Confirmation.State != CaseUnconfirmed || c.Confirmation.Code != code {
				t.Errorf("%q: %s = %+v", check, c.Label, c.Confirmation)
			}
		}
	}
}
