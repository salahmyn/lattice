package confirm

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// --unsigned lists a current version that rests on a typed note (an
// approval or an ack, with or without the local mark files written before
// it lack), a council's approval its delegating human has not acked, and
// each review-route confirmation of a current version, with the exact
// commands; it leaves out what a signature already confirms, older
// versions, and an agent's own approval.
// @verifies confirm:INV-19
func TestListUnsigned(t *testing.T) {
	local := func(id string, v int, by string) schema.BRD {
		return schema.BRD{ID: id, Version: v, Status: schema.BRDApproved,
			Approval: &schema.BRDApproval{ApprovedBy: by, ApprovedVersion: v, Local: true}}
	}
	acked := local("brd.wallet", 2, "council:m-brd")
	acked.Approval.Mandate, acked.Approval.HumanReview, acked.Approval.ReviewedBy = "m-brd", schema.HumanReviewAcknowledged, "bo@example.com"
	council := local("brd.loans", 1, "council:m-brd")
	council.Approval.Mandate, council.Approval.Local, council.Approval.DelegatedBy = "m-brd", false, "owner@example.com"
	council.Approval.HumanReview = schema.HumanReviewPending
	// Written before the local mark existed: an ack and an approval.
	legacyAck := local("brd.rates", 5, "council:m-brd")
	legacyAck.Approval.Mandate, legacyAck.Approval.HumanReview, legacyAck.Approval.ReviewedBy = "m-brd", schema.HumanReviewAcknowledged, "owner@example.com"
	legacyAck.Approval.Local = false
	legacyApprove := local("brd.limits", 2, "ana@example.com")
	legacyApprove.Approval.Local = false
	agent := local("brd.fx", 1, "agent-drafter")
	agent.Approval.Local = false
	brds := []schema.BRD{local("brd.fees", 3, "ana@example.com"), acked, local("brd.orders", 1, "ana@example.com"), council,
		{ID: "brd.cards", Version: 4, Status: schema.BRDApproved}, legacyAck, legacyApprove, agent}

	pcPath := "lattice/decisions/approvals/brd.orders-v1.confirm-1a2b3c4d-77aa88bb.yaml"
	cardsPath := "lattice/decisions/approvals/brd.cards-v4-55cc66dd.yaml"
	found := []Found{
		{Decision: Decision{ID: "brd.orders", Version: 1, Action: ActionPromiseConfirm}, Path: pcPath, AddedSHA: "c1", OnDefault: true},
		{Decision: Decision{ID: "brd.cards", Version: 4, Action: ActionApprove}, Path: cardsPath, AddedSHA: "c2", OnDefault: true},
		{Decision: Decision{ID: "brd.cards", Version: 3, Action: ActionApprove}, Path: "lattice/decisions/approvals/brd.cards-v3-11.yaml", AddedSHA: "c0", OnDefault: true},
	}
	cache := &File{Contract: Schema, Records: []Record{
		{ID: "brd.orders", Version: 1, Action: ActionApprove, State: StateConfirmed, Via: ViaSignature, ApproverLogin: "ana"},
		{ID: "brd.orders", Version: 1, Action: ActionPromiseConfirm, State: StateConfirmed, Via: ViaReview, ApproverLogin: "bo", PR: 7, DecisionSHA: "c1", Rows: []string{"orders:INV-1"}},
		{ID: "brd.cards", Version: 4, Action: ActionApprove, State: StateConfirmed, Via: ViaReview, ApproverLogin: "cy", PR: 9, DecisionSHA: "c2"},
		{ID: "brd.cards", Version: 3, Action: ActionApprove, State: StateConfirmed, Via: ViaReview, ApproverLogin: "cy", PR: 5, DecisionSHA: "c0"},
	}}
	stem := func(b schema.BRD, action string) (string, bool) {
		return Decision{ID: b.ID, Version: b.Version, Action: action, PacketHash: "sha256:abcdef0123"}.Stem(), true
	}
	l := ListUnsigned(UnsignedInput{BRDs: brds, Found: found, Cache: cache, Stem: stem})
	got := map[string]Unsigned{}
	for _, u := range l.Decisions {
		got[u.Action+" "+u.ID+" v"+itoa(u.Version)] = u
	}
	want := map[string][]string{
		"brd.approve brd.fees v3":       {"lattice prepare brd approve brd.fees --version 3", "git switch decision/brd.fees-v3-abcdef01", "lattice sign brd.fees-v3-abcdef01"},
		"brd.ack brd.wallet v2":         {"lattice prepare brd ack brd.wallet", "git switch decision/brd.wallet-v2.ack-abcdef01", "lattice sign brd.wallet-v2.ack-abcdef01"},
		"promise.confirm brd.orders v1": {"lattice sign " + pcPath},
		"brd.approve brd.cards v4":      {"lattice sign " + cardsPath},
		"brd.ack brd.loans v1":          {"lattice prepare brd ack brd.loans", "git switch decision/brd.loans-v1.ack-abcdef01", "lattice sign brd.loans-v1.ack-abcdef01"},
		"brd.ack brd.rates v5":          {"lattice prepare brd ack brd.rates", "git switch decision/brd.rates-v5.ack-abcdef01", "lattice sign brd.rates-v5.ack-abcdef01"},
		"brd.approve brd.limits v2":     {"lattice prepare brd approve brd.limits --version 2", "git switch decision/brd.limits-v2-abcdef01", "lattice sign brd.limits-v2-abcdef01"},
	}
	if len(got) != len(want) {
		t.Errorf("listed %d, want %d: %+v", len(got), len(want), l.Decisions)
	}
	for k, cmds := range want {
		u, ok := got[k]
		if !ok {
			t.Errorf("%s is not listed", k)
			continue
		}
		if strings.Join(u.Commands, "\n") != strings.Join(cmds, "\n") {
			t.Errorf("%s: commands %q, want %q", k, u.Commands, cmds)
		}
	}
	if u := got["brd.approve brd.fees v3"]; u.Rests != RestsLocal || u.By != "ana@example.com" || u.DecisionPath != "" {
		t.Errorf("a local note: %+v", u)
	}
	if u := got["brd.ack brd.loans v1"]; u.Rests != RestsCouncil || u.By != "owner@example.com" || !strings.Contains(u.Text, "read the council's decision") {
		t.Errorf("a council's approval awaiting its human: %+v", u)
	}
	if u := got["brd.ack brd.rates v5"]; u.Rests != RestsLocal || u.By != "owner@example.com" {
		t.Errorf("an ack written before the local mark: %+v", u)
	}
	if u := got["brd.approve brd.cards v4"]; u.Rests != RestsReview || u.By != "cy" || u.PR != 9 || u.DecisionPath != cardsPath {
		t.Errorf("a review: %+v", u)
	}
	txt := l.Text()
	if !strings.Contains(txt, "review  brd.approve brd.cards v4") || !strings.Contains(txt, "    lattice sign "+cardsPath) {
		t.Errorf("text:\n%s", txt)
	}

	none := ListUnsigned(UnsignedInput{BRDs: brds[:1], Stem: stem})
	if len(none.Decisions) != 1 || none.Note == "" {
		t.Errorf("without confirm data only local notes are listed, with a note: %+v", none)
	}
	if strings.TrimSpace(ListUnsigned(UnsignedInput{}).Text()) == "" {
		t.Error("an empty list says so")
	}
}
