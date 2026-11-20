package signature_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
)

const signersPath = "lattice/" + signature.AllowedSignersFile

// rootedRepo is a repository whose first allowed_signers grants ana's key:
// the root of trust.
func rootedRepo(t *testing.T) (*sigtest.Repo, *sigtest.Key, string) {
	t.Helper()
	r := sigtest.NewRepo(t)
	ana := sigtest.NewKey(t, sigtest.SKEd25519)
	r.Write(signersPath, ana.AllowedLine("ana"))
	return r, ana, r.Commit("enrol ana")
}

// signDecision merges a pull request holding the fees decision signed by k.
func signDecision(r *sigtest.Repo, k *sigtest.Key) {
	r.Git("checkout", "-q", "-b", "decision/fees")
	addSigned(r, k, feesDecision(), decisionPath)
	r.Commit("sign")
	r.Git("checkout", "-q", "main")
	r.Merge("decision/fees")
	r.Git("branch", "-q", "-D", "decision/fees")
}

// mergeSigners merges a pull request that rewrites allowed_signers, endorsed
// by endorser when it is not nil.
func mergeSigners(r *sigtest.Repo, endorser *sigtest.Key, body string) string {
	r.Git("checkout", "-q", "-b", "signers")
	if endorser != nil {
		r.Endorse(endorser, "main", body)
	} else {
		r.Write(signersPath, body)
	}
	r.Commit("change allowed_signers")
	r.Git("checkout", "-q", "main")
	m := r.Merge("signers")
	r.Git("branch", "-q", "-D", "signers")
	return m
}

func evaluate(r *sigtest.Repo) (signature.Evidence, *signature.Failure) {
	ev, f, ok := repoAt(r, "main").Evaluate(decisionPath, feesDecision())
	if !ok {
		r.T().Fatal("the signature file is missing")
	}
	return ev, f
}

func wantCode(t *testing.T, what string, f *signature.Failure, code string, unknown bool) {
	t.Helper()
	switch {
	case code == "" && f != nil:
		t.Errorf("%s: want it to hold, got %+v", what, f)
	case code != "" && (f == nil || f.Code != code || f.Unknown != unknown):
		t.Errorf("%s: want %s (unknown %v), got %+v", what, code, unknown, f)
	}
}

// The first allowed_signers is the root of trust: accepted as is, and
// reported as ROOT_SIGNERS at the commit that added it.
// @verifies confirm:INV-17
func TestTrustRootAccepted(t *testing.T) {
	r, ana, root := rootedRepo(t)
	signDecision(r, ana)
	ev, f := evaluate(r)
	wantCode(t, "a key in the root allowed_signers", f, "", false)
	if ev.Root != root {
		t.Errorf("evidence root %s, want %s", ev.Root, root)
	}
	tr := repoAt(r, "main").Trust("main")
	if tr.Root != root || len(tr.Changes) != 1 || tr.Changes[0].Code != signature.CodeRootSigners ||
		tr.Changes[0].Added[0] != "ana "+ana.Fingerprint() {
		t.Fatalf("root report: %+v", tr)
	}
}

// The rebase-merge hole of the base-commit rule: a key added in the same
// pull request as the signature, landed by a rebase merge, sits in the
// signature commit's parent. It does not count without an endorsement, so the
// decision reads unknown, SIGNER_NOT_ENDORSED.
// @verifies confirm:INV-17
func TestTrustSamePRKeyRebaseRefused(t *testing.T) {
	r, ana, _ := rootedRepo(t)
	bo := sigtest.NewKey(t, sigtest.SKEd25519)
	r.Git("checkout", "-q", "-b", "decision/fees")
	r.Write(signersPath, ana.AllowedLine("ana")+bo.AllowedLine("bo"))
	r.Commit("enrol bo")
	addSigned(r, bo, feesDecision(), decisionPath)
	r.Commit("sign")
	r.Git("checkout", "-q", "main")
	r.RebaseMerge("decision/fees")

	ev, f := evaluate(r)
	wantCode(t, "rebase merge", f, signature.CodeNotEndorsed, true)
	if ev.Base == "" || r.Git("rev-parse", ev.Base+":"+signersPath) == "" {
		t.Fatalf("control: the base commit holds bo's line: %+v", ev)
	}
	if f != nil && !strings.Contains(f.Text, "bo") {
		t.Errorf("the text names the login: %s", f.Text)
	}
}

// A key added with an endorsement by a key the previous version trusted
// counts, whatever the merge style.
// @verifies confirm:INV-17
func TestTrustEndorsedKeyCounts(t *testing.T) {
	for _, style := range []string{"merge", "rebase"} {
		r, ana, _ := rootedRepo(t)
		bo := sigtest.NewKey(t, sigtest.SKECDSA)
		r.Git("checkout", "-q", "-b", "decision/fees")
		r.Endorse(ana, "main", ana.AllowedLine("ana")+bo.AllowedLine("bo"))
		r.Commit("enrol bo, endorsed by ana")
		addSigned(r, bo, feesDecision(), decisionPath)
		r.Commit("sign")
		r.Git("checkout", "-q", "main")
		if style == "merge" {
			r.Merge("decision/fees")
		} else {
			r.RebaseMerge("decision/fees")
		}
		ev, f := evaluate(r)
		if style == "merge" {
			// The merge commit's first parent predates the enrolment.
			wantCode(t, style, f, signature.CodeNotAllowed, false)
			continue
		}
		wantCode(t, style, f, "", false)
		if len(ev.Logins) != 1 || ev.Logins[0] != "bo" {
			t.Errorf("%s: logins %v", style, ev.Logins)
		}
		ch := repoAt(r, "main").Trust("main").Changes
		if len(ch) != 2 || ch[1].Code != signature.CodeEndorsed || len(ch[1].EndorsedBy) != 1 || ch[1].EndorsedBy[0] != "ana" || ch[1].Fingerprint != ana.Fingerprint() {
			t.Errorf("%s: the change report: %+v", style, ch)
		}
	}

	// Enrolled through its own pull request first, then used.
	r, ana, _ := rootedRepo(t)
	bo := sigtest.NewKey(t, sigtest.SKEd25519)
	mergeSigners(r, ana, ana.AllowedLine("ana")+bo.AllowedLine("bo"))
	signDecision(r, bo)
	_, f := evaluate(r)
	wantCode(t, "enrolled in an earlier pull request", f, "", false)
}

// A key added without a valid endorsement does not count, from that commit
// on: no endorsement, one by the added key itself, one by a software key, one
// over other content, and an old endorsement replayed after the key was
// removed all read SIGNER_NOT_ENDORSED.
// @verifies confirm:INV-17
func TestTrustUnendorsedKey(t *testing.T) {
	bo := sigtest.NewKey(t, sigtest.SKEd25519)
	cases := map[string]func(r *sigtest.Repo, ana *sigtest.Key, body string){
		"no endorsement": func(r *sigtest.Repo, _ *sigtest.Key, body string) { mergeSigners(r, nil, body) },
		"endorsed by the added key": func(r *sigtest.Repo, _ *sigtest.Key, body string) {
			mergeSigners(r, bo, body)
		},
		"endorsed by a software key listed for ana": func(r *sigtest.Repo, ana *sigtest.Key, body string) {
			soft := sigtest.NewKey(t, sigtest.Software)
			mergeSigners(r, ana, ana.AllowedLine("ana")+soft.AllowedLine("ana"))
			mergeSigners(r, soft, body+soft.AllowedLine("ana"))
		},
		"an endorsement of other content": func(r *sigtest.Repo, ana *sigtest.Key, body string) {
			r.Git("checkout", "-q", "-b", "signers")
			r.Endorse(ana, "main", body)
			r.Write(signersPath, body+sigtest.NewKey(t, sigtest.SKEd25519).AllowedLine("cy"))
			r.Commit("endorse one file, commit another")
			r.Git("checkout", "-q", "main")
			r.Merge("signers")
		},
		"an endorsement replayed after the key was removed": func(r *sigtest.Repo, ana *sigtest.Key, body string) {
			mergeSigners(r, ana, body)
			sig := r.Git("show", "HEAD:lattice/"+signature.SignersSigFile)
			mergeSigners(r, nil, ana.AllowedLine("ana"))
			r.Write(signersPath, body)
			r.Write("lattice/"+signature.SignersSigFile, sig+"\n")
			r.Commit("re-add bo with the old endorsement")
		},
	}
	for name, add := range cases {
		r, ana, _ := rootedRepo(t)
		add(r, ana, ana.AllowedLine("ana")+bo.AllowedLine("bo"))
		signDecision(r, bo)
		_, f := evaluate(r)
		wantCode(t, name, f, signature.CodeNotEndorsed, true)
		ch := repoAt(r, "main").Trust("main").Changes
		if last := ch[len(ch)-1]; last.Code != signature.CodeNotEndorsed {
			t.Errorf("%s: the change adding bo reads %s: %+v", name, last.Code, last)
		}
	}
}

// Removing a key needs no endorsement: the removal counts at once, and the
// keys left keep counting.
// @verifies confirm:INV-17
func TestTrustRemovalNeedsNoEndorsement(t *testing.T) {
	r := sigtest.NewRepo(t)
	ana, bo := sigtest.NewKey(t, sigtest.SKEd25519), sigtest.NewKey(t, sigtest.SKEd25519)
	r.Write(signersPath, ana.AllowedLine("ana")+bo.AllowedLine("bo"))
	r.Commit("enrol ana and bo")
	signDecision(r, bo)
	_, f := evaluate(r)
	wantCode(t, "before the removal", f, "", false)

	mergeSigners(r, nil, ana.AllowedLine("ana"))
	_, f = evaluate(r)
	wantCode(t, "removed without an endorsement", f, signature.CodeRevoked, false)
	tr := repoAt(r, "main").Trust("main")
	if last := tr.Changes[len(tr.Changes)-1]; last.Code != signature.CodeRemoved || len(last.Removed) != 1 {
		t.Errorf("the removal report: %+v", last)
	}
	if got, _ := tr.Logins(ana.Pub); len(got) != 1 || got[0] != "ana" {
		t.Errorf("the key left still counts: %v", got)
	}
}

// Trust follows a chain of endorsements: ana endorses bo, then bo endorses
// cy while removing ana (a rotation), and cy's signature counts. A chain
// whose first link is not endorsed does not: bo was never trusted, so bo's
// endorsement of cy counts for nothing.
// @verifies confirm:INV-17
func TestTrustEndorsementChain(t *testing.T) {
	r, ana, _ := rootedRepo(t)
	bo, cy := sigtest.NewKey(t, sigtest.SKEd25519), sigtest.NewKey(t, sigtest.SKECDSA)
	mergeSigners(r, ana, ana.AllowedLine("ana")+bo.AllowedLine("bo"))
	mergeSigners(r, bo, bo.AllowedLine("bo")+cy.AllowedLine("cy"))
	signDecision(r, cy)
	ev, f := evaluate(r)
	wantCode(t, "a chain of two endorsements", f, "", false)
	if len(ev.Logins) != 1 || ev.Logins[0] != "cy" {
		t.Errorf("logins %v", ev.Logins)
	}
	codes := []string{}
	for _, c := range repoAt(r, "main").Trust("main").Changes {
		codes = append(codes, c.Code)
	}
	if strings.Join(codes, " ") != "ROOT_SIGNERS SIGNERS_ENDORSED SIGNERS_ENDORSED" {
		t.Errorf("the chain: %v", codes)
	}

	broken, ana2, _ := rootedRepo(t)
	mergeSigners(broken, nil, ana2.AllowedLine("ana")+bo.AllowedLine("bo"))
	mergeSigners(broken, bo, ana2.AllowedLine("ana")+bo.AllowedLine("bo")+cy.AllowedLine("cy"))
	signDecision(broken, cy)
	_, f = evaluate(broken)
	wantCode(t, "a chain from an unendorsed key", f, signature.CodeNotEndorsed, true)
}

// A shallow clone cannot show where trust started, so a signature reads
// unknown rather than taking the visible history for the root.
// @verifies confirm:INV-17
func TestTrustShallowCloneUnknown(t *testing.T) {
	r, ana, _ := rootedRepo(t)
	bo := sigtest.NewKey(t, sigtest.SKEd25519)
	mergeSigners(r, nil, ana.AllowedLine("ana")+bo.AllowedLine("bo"))
	signDecision(r, ana)
	dir := filepath.Join(t.TempDir(), "shallow")
	if out, err := exec.Command("git", "clone", "-q", "--depth", "3", "file://"+r.Dir, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	_, f, _ := signature.Repo{Dir: dir, LatticeRel: "lattice", Ref: "HEAD", Repository: sigtest.Repository}.Evaluate(decisionPath, feesDecision())
	if f == nil || f.Code != signature.CodeSignersHistory || !f.Unknown {
		t.Fatalf("a shallow clone: %+v", f)
	}
}
