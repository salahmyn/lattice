package confirm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
)

// authorHost is a key-listing platform that also names pull request authors.
type authorHost struct {
	*keyHost
	author, kind string
	asked        []string
}

func (a *authorHost) PullAuthor(_ context.Context, sha string) (string, string, error) {
	a.asked = append(a.asked, sha)
	return a.author, a.kind, nil
}

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

// A decision prepared in a pull request opened by a bot or agent account and
// signed by a human counts, and the text names the pull request's author so
// a reviewer sees a machine prepared it. The signer is the human login the
// key is listed and anchored for, never the author; a key listed only for a
// bot or agent login is refused, and a reader refuses a claim naming one.
// @verifies confirm:INV-18
func TestSignatureBotAuthoredPullRequest(t *testing.T) {
	f := newSigFixture(t, sigtest.SKEd25519, nil)
	host := &authorHost{keyHost: hostListing("ana", f.key), author: "release-agent[bot]", kind: "Bot"}
	host.keys["release-agent[bot]"] = host.keys["ana"]
	file := f.compute(host, nil)
	rec := f.record(file, "brd.fees")
	if rec.State != StateConfirmed || rec.ApproverLogin != "ana" || rec.PRAuthor != "release-agent[bot]" || !rec.PRAuthorBot {
		t.Fatalf("a bot-opened pull request signed by ana: %+v", rec)
	}
	if len(host.asked) != 1 || host.asked[0] != f.r.Git("log", "--diff-filter=A", "--format=%H", "-n", "1", "main", "--", "lattice/"+f.d.RelPath()) {
		t.Errorf("the author is asked for the commit that added the decision: %v", host.asked)
	}
	txt := file.Text()
	if !strings.Contains(txt, "human (signed by ana, ") || !strings.Contains(txt, "opened by release-agent[bot], a bot or agent account") {
		t.Errorf("the text must name the method and the author:\n%s", txt)
	}
	human := &authorHost{keyHost: hostListing("ana", f.key), author: "ana", kind: "User"}
	if rec := f.record(f.compute(human, nil), "brd.fees"); rec.PRAuthor != "ana" || rec.PRAuthorBot {
		t.Errorf("a human-opened pull request: %+v", rec)
	}

	for _, login := range []string{"release-agent[bot]", "agent-impl"} {
		g := newSigFixtureFor(t, sigtest.SKEd25519, login, nil)
		file := g.compute(hostListing(login, g.key), nil)
		rec := g.record(file, "brd.fees")
		if rec.State != StatePending || rec.Assurance.Code != signature.CodeNotHuman {
			t.Errorf("a key listed only for %s: %+v", login, rec)
		}
		// A hand-written verdict claiming that login signed does not survive
		// a reader, though the key is listed for it and the signature holds.
		rec.State, rec.ApproverLogin, rec.Assurance.State, rec.Assurance.Code = StateConfirmed, login, StateConfirmed, signature.CodeSigned
		file.Records = []Record{rec}
		lattice := filepath.Join(g.r.Dir, "lattice")
		if err := SaveCache(lattice, Seal(file, *file.Stamp)); err != nil {
			t.Fatal(err)
		}
		if back := LoadCache(lattice); back == nil || back.Records[0].State != StateUnknown || back.Records[0].Assurance.Code != signature.CodeNotReverified {
			t.Errorf("a reader refuses a signature claim naming %s: %+v", login, back)
		}
	}
}
