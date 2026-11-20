package confirm

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
)

// A decision signed by a key that was added to allowed_signers without an
// endorsement reads unknown, SIGNER_NOT_ENDORSED, never human; confirm
// reports the root of trust and the unendorsed change, and a reader of the
// file re-verifies the same way.
// @verifies confirm:INV-17
func TestSignatureUnendorsedKeyUnknown(t *testing.T) {
	r := sigtest.NewRepo(t)
	ana, bo := sigtest.NewKey(t, sigtest.SKEd25519), sigtest.NewKey(t, sigtest.SKEd25519)
	r.Write("lattice/allowed_signers", ana.AllowedLine("ana"))
	root := r.Commit("enrol ana")
	r.Write("lattice/allowed_signers", ana.AllowedLine("ana")+bo.AllowedLine("bo"))
	r.Commit("enrol bo, unendorsed")

	d := feesApproval()
	r.Git("checkout", "-q", "-b", d.Branch())
	f := &sigFixture{t: t, r: r, key: bo, d: d}
	f.writeDecision(d, bo.SignTuple(t, SignatureTuple(d, sigtest.Repository), sigtest.Options{}))
	r.Commit("decision")
	r.Git("checkout", "-q", "main")
	r.Merge(d.Branch())

	file := f.compute(hostListing("bo", bo), nil)
	rec := f.record(file, "brd.fees")
	if rec.State != StateUnknown || rec.Assurance.Code != signature.CodeNotEndorsed || rec.Via != ViaSignature || rec.ApproverLogin != "" {
		t.Fatalf("an unendorsed key: %+v", rec)
	}
	if file.Signers == nil || file.Signers.Root != root || file.Signers.Assurance.Code != signature.CodeRootSigners ||
		len(file.Signers.Changes) != 2 || file.Signers.Changes[1].Code != signature.CodeNotEndorsed {
		t.Fatalf("the trust report: %+v", file.Signers)
	}
	txt := file.Text()
	for _, want := range []string{"[ROOT_SIGNERS]", "trust starts at " + short(root), "[SIGNER_NOT_ENDORSED]"} {
		if !strings.Contains(txt, want) {
			t.Errorf("the text must show %q:\n%s", want, txt)
		}
	}
}
