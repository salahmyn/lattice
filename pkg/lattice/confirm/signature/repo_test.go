package signature_test

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
)

const decisionPath = "lattice/decisions/approvals/brd.fees-v3-9e1f04ab.yaml"

func feesDecision() signature.Decision {
	return signature.Decision{Action: "brd.approve", ID: "brd.fees", Version: 3, PacketHash: "sha256:9e1f04ab"}
}

func decisionYAML(d signature.Decision) string {
	s := "action: " + d.Action + "\nid: " + d.ID + "\nversion: " + strconv.Itoa(d.Version) + "\npacket_hash: " + d.PacketHash + "\n"
	if len(d.Rows) > 0 {
		s += "rows:\n"
		for _, r := range d.Rows {
			s += "  - promise: " + r.Promise + "\n"
		}
	}
	return s + "prepared_by: t <t@example.com>\nprepared_at: \"2026-01-01T00:00:00Z\"\n"
}

// addSigned writes the decision and its signature by k on the current branch.
func addSigned(r *sigtest.Repo, k *sigtest.Key, d signature.Decision, path string) {
	r.Write(path, decisionYAML(d))
	r.Write(signature.SigPath(path), string(k.SignTuple(r.T(), d.Tuple(sigtest.Repository), sigtest.Options{})))
}

func repoAt(r *sigtest.Repo, ref string) signature.Repo {
	return signature.Repo{Dir: r.Dir, LatticeRel: "lattice", Ref: ref, Repository: sigtest.Repository}
}

// A key enrolled on the default branch before the pull request counts; the
// commit allowed_signers is read at is the merge's first parent.
// @verifies confirm:INV-10
func TestEvaluateEnrolledKey(t *testing.T) {
	r := sigtest.NewRepo(t)
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	r.Write("lattice/allowed_signers", k.AllowedLine("ana"))
	enrolled := r.Commit("enrol ana")

	r.Git("checkout", "-q", "-b", "decision/fees")
	addSigned(r, k, feesDecision(), decisionPath)
	r.Commit("sign")
	r.Git("checkout", "-q", "main")
	merge := r.Merge("decision/fees")

	ev, f, ok := repoAt(r, "main").Evaluate(decisionPath, feesDecision())
	if !ok || f != nil {
		t.Fatalf("a signature by an enrolled key must hold: %v %+v", ok, f)
	}
	if ev.Base != enrolled || ev.Introduced != merge || len(ev.Logins) != 1 || ev.Logins[0] != "ana" || ev.Fingerprint != k.Fingerprint() {
		t.Errorf("evidence %+v (want base %s, introduced %s)", ev, enrolled, merge)
	}
	if _, _, ok := repoAt(r, "main").Evaluate("lattice/decisions/approvals/none.yaml", feesDecision()); ok {
		t.Error("a decision without a signature file has no signature")
	}
}

// A key added to allowed_signers in the same pull request as the signature
// does not count, whether the request is merged with a merge commit or
// squashed: allowed_signers is read before the change, never at its head.
// @verifies confirm:INV-10
func TestEvaluateSamePRKeyRejected(t *testing.T) {
	r := sigtest.NewRepo(t)
	k := sigtest.NewKey(t, sigtest.SKEd25519)

	r.Git("checkout", "-q", "-b", "decision/fees")
	r.Write("lattice/allowed_signers", k.AllowedLine("ana"))
	r.Commit("enrol ana")
	addSigned(r, k, feesDecision(), decisionPath)
	r.Commit("sign")
	r.Git("checkout", "-q", "main")
	r.Merge("decision/fees")

	_, f, ok := repoAt(r, "main").Evaluate(decisionPath, feesDecision())
	if !ok || f == nil || f.Code != signature.CodeNotAllowed {
		t.Fatalf("merge commit: a key enrolled in the same PR must be refused, got %+v", f)
	}
	// On the PR branch alone, the enrolling commit is the signature's parent:
	// reading along the branch would wrongly accept it.
	if _, f, _ := repoAt(r, "decision/fees").Evaluate(decisionPath, feesDecision()); f != nil {
		t.Fatalf("control: along the PR branch the key is in its parent: %+v", f)
	}

	sq := sigtest.NewRepo(t)
	sq.Write("lattice/allowed_signers", k.AllowedLine("ana"))
	addSigned(sq, k, feesDecision(), decisionPath)
	sq.Commit("squashed: enrol and sign")
	if _, f, _ := repoAt(sq, "main").Evaluate(decisionPath, feesDecision()); f == nil || f.Code != signature.CodeNotAllowed {
		t.Fatalf("squash: got %+v", f)
	}
}

// Removing the key from allowed_signers un-confirms what it signed, and a
// signature with no base commit reads unknown.
// @verifies confirm:INV-10
func TestEvaluateRevokedAndNoBase(t *testing.T) {
	r := sigtest.NewRepo(t)
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	r.Write("lattice/allowed_signers", k.AllowedLine("ana"))
	r.Commit("enrol")
	addSigned(r, k, feesDecision(), decisionPath)
	r.Commit("sign")
	if _, f, _ := repoAt(r, "main").Evaluate(decisionPath, feesDecision()); f != nil {
		t.Fatalf("before revocation: %+v", f)
	}
	r.Write("lattice/allowed_signers", "# ana left\n")
	r.Commit("revoke ana")
	if _, f, _ := repoAt(r, "main").Evaluate(decisionPath, feesDecision()); f == nil || f.Code != signature.CodeRevoked {
		t.Fatalf("after revocation: %+v", f)
	}

	r.Git("checkout", "-q", "--orphan", "fresh")
	r.Write("lattice/allowed_signers", k.AllowedLine("ana"))
	r.Commit("root")
	if _, f, _ := repoAt(r, "fresh").Evaluate(decisionPath, feesDecision()); f == nil || f.Code != signature.CodeNoBase || !f.Unknown {
		t.Fatalf("a signature in a root commit has no base: %+v", f)
	}
}

// Recheck re-verifies a confirm.json claim from the repository and refuses a
// claim with no signature behind it, one for another key or promises, and
// one pointing outside the decision directory.
// @verifies confirm:INV-12
func TestRecheck(t *testing.T) {
	r := sigtest.NewRepo(t)
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	r.Write("lattice/allowed_signers", k.AllowedLine("ana"))
	r.Commit("enrol")
	pc := signature.Decision{Action: "promise.confirm", ID: "brd.fees", Version: 3, PacketHash: "sha256:9e1f04ab"}
	pc.Rows = append(pc.Rows, struct {
		Promise string `yaml:"promise"`
	}{"fees:INV-1"})
	pcPath := "lattice/decisions/approvals/brd.fees-v3.confirm-1a2b3c4d-9e1f04ab.yaml"
	addSigned(r, k, feesDecision(), decisionPath)
	addSigned(r, k, pc, pcPath)
	r.Write("lattice/decisions/approvals/brd.wallet-v1-aa.yaml", decisionYAML(signature.Decision{Action: "brd.approve", ID: "brd.wallet", Version: 1, PacketHash: "sha256:aa"}))
	r.Commit("decisions")

	good := signature.Claim{ID: "brd.fees", Version: 3, Action: "brd.approve", DecisionPath: decisionPath, Login: "ana", Fingerprint: k.Fingerprint()}
	cases := []struct {
		name string
		c    signature.Claim
		code string
	}{
		{"holds", good, ""},
		{"login case", func() signature.Claim { c := good; c.Login = "ANA"; return c }(), ""},
		{"promise confirm holds", signature.Claim{ID: "brd.fees", Version: 3, Action: "promise.confirm", Rows: []string{"fees:INV-1"}, DecisionPath: pcPath, Login: "ana", Fingerprint: k.Fingerprint()}, ""},
		{"more promises than signed", signature.Claim{ID: "brd.fees", Version: 3, Action: "promise.confirm", Rows: []string{"fees:INV-1", "fees:INV-2"}, DecisionPath: pcPath, Login: "ana", Fingerprint: k.Fingerprint()}, signature.CodeNotReverified},
		{"no signature behind it", signature.Claim{ID: "brd.wallet", Version: 1, Action: "brd.approve", DecisionPath: "lattice/decisions/approvals/brd.wallet-v1-aa.yaml", Login: "ana", Fingerprint: k.Fingerprint()}, signature.CodeNotReverified},
		{"another key", func() signature.Claim { c := good; c.Fingerprint = "SHA256:other"; return c }(), signature.CodeNotReverified},
		{"another login", func() signature.Claim { c := good; c.Login = "bo"; return c }(), signature.CodeNotReverified},
		{"another version", func() signature.Claim { c := good; c.Version = 4; return c }(), signature.CodeNotReverified},
		{"outside the decisions", func() signature.Claim {
			c := good
			c.DecisionPath = "lattice/decisions/approvals/../../brds/x.yaml"
			return c
		}(), signature.CodeNotReverified},
		{"no path", func() signature.Claim { c := good; c.DecisionPath = ""; return c }(), signature.CodeNotReverified},
	}
	claims := make([]signature.Claim, len(cases))
	for i, c := range cases {
		claims[i] = c.c
	}
	got := signature.Recheck(filepath.Join(r.Dir, "lattice"), "main", claims)
	for i, c := range cases {
		code := ""
		if got[i] != nil {
			code = got[i].Code
		}
		if code != c.code {
			t.Errorf("%s: code %q, want %q (%+v)", c.name, code, c.code, got[i])
		}
	}
	if f := signature.Recheck(filepath.Join(r.Dir, "lattice"), "", []signature.Claim{good})[0]; f == nil {
		t.Error("with no branch to read, a claim does not hold")
	}
	if f := signature.Recheck(t.TempDir(), "main", []signature.Claim{good})[0]; f == nil {
		t.Error("outside a repository, a claim does not hold")
	}
}
