package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/stamp"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// writeSealed writes a confirm.json whose stamp is intact: what a careful
// forger can produce, since the stamp holds no secret.
func writeSealed(t *testing.T, latticeDir string, recs []ConfirmRecord) {
	t.Helper()
	st, err := stamp.Seal(stamp.Stamp{Commit: "h1", CIRun: "4812", Platform: "github"}, recs)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{"contract": confirmSchema, "commit": "h1", "branch": "main", "stamp": st, "records": recs})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(latticeDir, ".cache", "confirm.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A signature verdict in confirm.json is re-verified from the repository
// on every read. With the signature in the tree it reads human, with its
// method; a well-stamped file claiming a signature the tree does not hold
// reads unknown, never human.
// @verifies confirm:INV-12
// @verifies confirm:INV-14
func TestSignatureVerdictIsReverified(t *testing.T) {
	r := sigtest.NewRepo(t)
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	r.Write("lattice/allowed_signers", k.AllowedLine("ana"))
	r.Commit("enrol")
	signedPath := "lattice/decisions/approvals/b-v2-aa.yaml"
	d := signature.Decision{Action: "brd.approve", ID: "b", Version: 2, PacketHash: "sha256:aa"}
	r.Write(signedPath, "action: brd.approve\nid: b\nversion: 2\npacket_hash: sha256:aa\nprepared_by: t\nprepared_at: x\n")
	r.Write(signature.SigPath(signedPath), string(k.SignTuple(t, d.Tuple(sigtest.Repository), sigtest.Options{})))
	r.Write("lattice/decisions/approvals/c-v2-bb.yaml", "action: brd.approve\nid: c\nversion: 2\npacket_hash: sha256:bb\nprepared_by: t\nprepared_at: x\n")
	r.Commit("decisions")
	latticeDir := filepath.Join(r.Dir, "lattice")

	claim := func(id, path string) ConfirmRecord {
		return ConfirmRecord{ID: id, Version: 2, Action: "brd.approve", State: "confirmed", Via: ViaSignature,
			ApproverLogin: "ana", Fingerprint: k.Fingerprint(), DecisionPath: path}
	}
	writeSealed(t, latticeDir, []ConfirmRecord{claim("b", signedPath), claim("c", "lattice/decisions/approvals/c-v2-bb.yaml"), claim("d", "")})
	cd := LoadConfirm(latticeDir)
	if cd == nil || cd.SetAside != "" {
		t.Fatalf("the stamp is intact: %+v", cd)
	}

	good := confirmedOf(t, []schema.BRD{brdFor("b", schema.BRDDraft, 2, nil)}, cd).Signals.Confirmed
	if good.Value != ConfirmedHuman || !strings.Contains(good.Detail, "human (signed by ana, "+signature.Short(k.Fingerprint())+")") {
		t.Errorf("a signature in the tree reads human, with its method: %+v", good)
	}
	for _, id := range []string{"c", "d"} {
		s := confirmedOf(t, []schema.BRD{brdFor(id, schema.BRDDraft, 2, nil)}, cd).Signals.Confirmed
		if s.Value != ConfirmedUnknown || !strings.Contains(s.Detail, signature.CodeNotReverified) {
			t.Errorf("%s: a claimed signature the tree does not hold must read unknown: %+v", id, s)
		}
	}
}

// Acceptance 12: a local ack never reads human, with or without confirm
// data, and a signature claim that does not re-verify does not lift it.
// @verifies audit:INV-5
func TestLocalAckNeverHuman(t *testing.T) {
	local := &schema.BRDApproval{ApprovedBy: "council:m1", Mandate: "m1", ApprovedVersion: 2, Local: true,
		HumanReview: schema.HumanReviewAcknowledged, ReviewedBy: "ana@example.com"}
	typed := &schema.BRDApproval{ApprovedBy: "ana@example.com", ApprovedVersion: 2, Local: true}
	unverified := &ConfirmData{Records: []ConfirmRecord{{ID: "b", Version: 2, Action: "brd.ack", State: ConfirmedUnknown, Via: ViaSignature,
		ApproverLogin: "ana", Note: signature.CodeNotReverified + ": no signature"}}}
	for _, ap := range []*schema.BRDApproval{local, typed} {
		for _, cd := range []*ConfirmData{nil, {}, unverified} {
			s := confirmedOf(t, []schema.BRD{brdFor("b", schema.BRDApproved, 2, ap)}, cd).Signals.Confirmed
			if s.Value == ConfirmedHuman {
				t.Errorf("a local ack read human: %+v (confirm data %+v)", s, cd)
			}
		}
	}
}
