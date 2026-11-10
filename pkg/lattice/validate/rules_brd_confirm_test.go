package validate

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A machine-written draft whose current version a human confirmed on the
// platform is approved. A typed approved_by, another version, a pending or
// stale verdict, a promise-only confirmation, or a confirmed return is not.
// @verifies brds:INV-9
func TestBRDUnapprovedLLMConfirmedByPlatform(t *testing.T) {
	rec := func(version int, action, state, who string) confirm.Record {
		return confirm.Record{ID: "brd.checkout", Version: version, Action: action, State: state, ApproverLogin: who, PR: 7}
	}
	file := func(rs ...confirm.Record) *confirm.File { return &confirm.File{Records: rs} }
	for name, tc := range map[string]struct {
		cf      *confirm.File
		typed   bool // a hand-typed approval block; status stays draft
		warning bool
	}{
		"no confirm data":        {nil, false, true},
		"confirmed approve":      {file(rec(2, confirm.ActionApprove, confirm.StateConfirmed, "alice")), false, false},
		"confirmed ack":          {file(rec(2, confirm.ActionAck, confirm.StateConfirmed, "alice")), false, false},
		"older version":          {file(rec(1, confirm.ActionApprove, confirm.StateConfirmed, "alice")), false, true},
		"pending":                {file(rec(2, confirm.ActionApprove, confirm.StatePending, "")), false, true},
		"stale":                  {file(rec(2, confirm.ActionApprove, confirm.StateStale, "alice")), false, true},
		"promise confirm only":   {file(rec(2, confirm.ActionPromiseConfirm, confirm.StateConfirmed, "alice")), false, true},
		"no approver":            {file(rec(2, confirm.ActionApprove, confirm.StateConfirmed, "")), false, true},
		"returned after":         {file(rec(2, confirm.ActionApprove, confirm.StateConfirmed, "alice"), rec(2, confirm.ActionReturn, confirm.StateConfirmed, "bob")), false, true},
		"typed approved_by only": {nil, true, true},
		"typed, empty confirm":   {file(), true, true},
		"typed, other version":   {file(rec(1, confirm.ActionApprove, confirm.StateConfirmed, "alice")), true, true},
	} {
		b := schema.BRD{
			ID: "brd.checkout", Version: 2, Status: schema.BRDDraft, Title: "x", BusinessProblem: "p",
			Provenance: schema.BRDProvenance{Source: schema.BRDSourceLLMFromCode},
		}
		if tc.typed {
			b.Approval = &schema.BRDApproval{ApprovedBy: "pm@example.com", ApprovedVersion: 2}
		}
		kg := schema.KnowledgeGraph{BRDs: []schema.BRD{b}}
		got := codes(Validate(kg, config.Default(), Options{Confirm: tc.cf}))[schema.CodeBRDUnapprovedLLM]
		if got != tc.warning {
			t.Errorf("%s: BRD_UNAPPROVED_LLM = %v, want %v", name, got, tc.warning)
		}
	}
}
