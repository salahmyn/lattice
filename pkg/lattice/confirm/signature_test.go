package confirm

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
)

// keyHost is a platform that also lists signing keys.
type keyHost struct {
	fake
	keys map[string][]string
	err  error
	asks int
}

func (k *keyHost) SigningKeys(_ context.Context, login string) ([]string, error) {
	k.asks++
	return k.keys[login], k.err
}

func hostListing(login string, keys ...*sigtest.Key) *keyHost {
	h := &keyHost{keys: map[string][]string{}}
	for _, k := range keys {
		h.keys[login] = append(h.keys[login], k.AuthorizedKey())
	}
	return h
}

type sigFixture struct {
	t        *testing.T
	r        *sigtest.Repo
	key      *sigtest.Key
	d        Decision
	enrolled string
}

func feesApproval() Decision {
	return Decision{Action: ActionApprove, ID: "brd.fees", Version: 3, PacketHash: "sha256:9e1f04ab5c6d7e8f",
		PreparedBy: "Ana <ana@example.com>", PreparedAt: "2026-01-01T00:00:00Z"}
}

// newSigFixture enrols ana's key on main, then merges a decision branch
// holding the decision and sig (the signature file), as a pull request
// would. sig nil signs the decision with the key.
func newSigFixture(t *testing.T, kind string, sig func(f *sigFixture) []byte) *sigFixture {
	t.Helper()
	f := &sigFixture{t: t, r: sigtest.NewRepo(t), key: sigtest.NewKey(t, kind), d: feesApproval()}
	f.r.Write("lattice/allowed_signers", f.key.AllowedLine("ana"))
	f.enrolled = f.r.Commit("enrol ana")
	if sig == nil {
		sig = func(f *sigFixture) []byte {
			return f.key.SignTuple(t, SignatureTuple(f.d, sigtest.Repository), sigtest.Options{})
		}
	}
	f.r.Git("checkout", "-q", "-b", f.d.Branch())
	f.writeDecision(f.d, sig(f))
	f.r.Commit("decision and signature")
	f.r.Git("checkout", "-q", "main")
	f.r.Merge(f.d.Branch())
	return f
}

func (f *sigFixture) writeDecision(d Decision, sig []byte) {
	f.t.Helper()
	body, err := d.Marshal()
	if err != nil {
		f.t.Fatal(err)
	}
	p := "lattice/" + d.RelPath()
	f.r.Write(p, string(body))
	if sig != nil {
		f.r.Write(signature.SigPath(p), string(sig))
	}
}

// compute runs confirm over main with the signature route on.
func (f *sigFixture) compute(p Platform, packet PacketFunc) File {
	f.t.Helper()
	found, warnings, err := Scan(Git{Dir: f.r.Dir}, "main", "lattice")
	if err != nil || len(warnings) > 0 {
		f.t.Fatalf("scan: %v %v", err, warnings)
	}
	if packet == nil {
		packet = func(d Decision) (string, bool) { return d.PacketHash, true }
	}
	return Compute(context.Background(), Input{Commit: "head", Branch: "main", Found: found, Platform: p, Packet: packet, Now: fixedNow,
		Signed: &signature.Repo{Dir: f.r.Dir, LatticeRel: "lattice", Ref: "main", Repository: sigtest.Repository}})
}

func (f *sigFixture) record(file File, id string) Record {
	f.t.Helper()
	for _, r := range file.Records {
		if r.ID == id {
			return r
		}
	}
	f.t.Fatalf("no record for %s: %+v", id, file.Records)
	return Record{}
}

// Acceptance 1: a valid security-key signature, with the key in
// allowed_signers at the base commit and among the signer's GitHub signing
// keys, confirms: human, via signature, with the login and fingerprint.
// Readers re-verify it and keep it.
// @verifies confirm:INV-8
// @verifies confirm:INV-11
// @verifies confirm:INV-14
func TestSignatureConfirms(t *testing.T) {
	for _, kind := range []string{sigtest.SKEd25519, sigtest.SKECDSA} {
		f := newSigFixture(t, kind, nil)
		host := hostListing("ana", f.key)
		file := f.compute(host, nil)
		r := f.record(file, "brd.fees")
		if r.State != StateConfirmed || r.Via != ViaSignature || r.ApproverLogin != "ana" || r.Fingerprint != f.key.Fingerprint() ||
			r.Assurance.Code != signature.CodeSigned || r.PR != 0 || r.SignatureBase != f.enrolled || r.DecisionPath != "lattice/"+f.d.RelPath() {
			t.Fatalf("%s: %+v", kind, r)
		}
		if host.calls != 0 {
			t.Errorf("a decision confirmed by signature needs no review lookup (%d calls)", host.calls)
		}
		if txt := file.Text(); !strings.Contains(txt, "human (signed by ana, "+signature.Short(f.key.Fingerprint())+")") {
			t.Errorf("text must show the method: %s", txt)
		}
		lattice := filepath.Join(f.r.Dir, "lattice")
		if err := SaveCache(lattice, file); err != nil {
			t.Fatal(err)
		}
		if back := LoadCache(lattice); back == nil || back.Records[0].State != StateConfirmed {
			t.Fatalf("a reader re-verifies and keeps a good signature: %+v", back)
		}
	}
}

// Acceptance 3: a key in allowed_signers but not among the signer's GitHub
// signing keys is refused; a platform that cannot be read, or none, reads
// unknown, never confirmed.
// @verifies confirm:INV-11
func TestSignatureGitHubAnchor(t *testing.T) {
	f := newSigFixture(t, sigtest.SKEd25519, nil)
	other := sigtest.NewKey(t, sigtest.SKEd25519)
	cases := []struct {
		name  string
		p     Platform
		state string
		code  string
	}{
		{"not a GitHub signing key", hostListing("ana", other), StatePending, signature.CodeNotOnHost},
		{"listed for someone else", hostListing("bo", f.key), StatePending, signature.CodeNotOnHost},
		{"GitHub unreachable", &keyHost{err: errors.New("dial tcp: no route")}, StateUnknown, signature.CodeAnchorUnknown},
		{"no token", nil, StateUnknown, signature.CodeAnchorUnknown},
		{"a platform with no signing keys", &fake{}, StateUnknown, signature.CodeAnchorUnknown},
	}
	for _, c := range cases {
		r := f.record(f.compute(c.p, nil), "brd.fees")
		if r.State != c.state || r.Assurance.Code != c.code || r.Via != ViaSignature || r.Fingerprint != f.key.Fingerprint() {
			t.Errorf("%s: got %s/%s %+v", c.name, r.State, r.Assurance.Code, r)
		}
	}
}

// Acceptance 4: a software key is refused with SOFTWARE_KEY, and so is a
// security-key signature without user presence.
// @verifies confirm:INV-8
func TestSignatureSoftwareKey(t *testing.T) {
	f := newSigFixture(t, sigtest.Software, nil)
	r := f.record(f.compute(hostListing("ana", f.key), nil), "brd.fees")
	if r.State != StatePending || r.Assurance.Code != signature.CodeSoftwareKey {
		t.Fatalf("software key: %+v", r)
	}
	f = newSigFixture(t, sigtest.SKEd25519, func(f *sigFixture) []byte {
		return f.key.SignTuple(t, SignatureTuple(f.d, sigtest.Repository), sigtest.Options{NoPresence: true})
	})
	if r := f.record(f.compute(hostListing("ana", f.key), nil), "brd.fees"); r.State != StatePending || r.Assurance.Code != signature.CodeNoPresence {
		t.Fatalf("no user presence: %+v", r)
	}
}

// Acceptance 5: a changed packet reads stale; editing the decision's packet
// hash to follow the change breaks the signature.
// @verifies confirm:INV-3
// @verifies confirm:INV-9
func TestSignatureStalePacket(t *testing.T) {
	f := newSigFixture(t, sigtest.SKEd25519, nil)
	host := hostListing("ana", f.key)
	r := f.record(f.compute(host, func(Decision) (string, bool) { return "sha256:changed", true }), "brd.fees")
	if r.State != StateStale || r.Assurance.Code != "PACKET_MISMATCH" || r.Via != ViaSignature || !strings.HasPrefix(r.Assurance.Text, "signed by ana") {
		t.Fatalf("changed packet: %+v", r)
	}
	edited := f.d
	edited.PacketHash = "sha256:changed"
	body, _ := edited.Marshal()
	f.r.Write("lattice/"+f.d.RelPath(), string(body))
	f.r.Commit("follow the packet")
	if r := f.record(f.compute(host, nil), "brd.fees"); r.State != StatePending || r.Assurance.Code != signature.CodeInvalid {
		t.Fatalf("an edited packet hash: %+v", r)
	}
}

// Acceptance 6 and 7: a signature replayed onto another decision, version,
// action or repository fails, and so does one in another namespace.
// @verifies confirm:INV-9
func TestSignatureReplay(t *testing.T) {
	f := newSigFixture(t, sigtest.SKEd25519, nil)
	sig := f.key.SignTuple(t, SignatureTuple(f.d, sigtest.Repository), sigtest.Options{})
	host := hostListing("ana", f.key)
	for name, mut := range map[string]func(*Decision){
		"decision": func(d *Decision) { d.ID = "brd.wallet" },
		"version":  func(d *Decision) { d.Version = 4 },
		"action":   func(d *Decision) { d.Action = ActionAck },
	} {
		d := f.d
		mut(&d)
		f.writeDecision(d, sig)
		f.r.Commit("replay onto another " + name)
		seen := false
		for _, r := range f.compute(host, nil).Records {
			if r.ID == d.ID && r.Version == d.Version && r.Action == d.Action {
				seen = true
				if r.State == StateConfirmed || r.Assurance.Code != signature.CodeInvalid {
					t.Errorf("replayed onto another %s: %+v", name, r)
				}
			}
		}
		if !seen {
			t.Errorf("replayed onto another %s: no record", name)
		}
	}

	found, _, _ := Scan(Git{Dir: f.r.Dir}, "main", "lattice")
	other := Compute(context.Background(), Input{Commit: "head", Branch: "main", Found: found, Platform: host, Now: fixedNow,
		Signed: &signature.Repo{Dir: f.r.Dir, LatticeRel: "lattice", Ref: "main", Repository: "acme/other"}})
	if r := f.record(other, "brd.fees"); r.Assurance.Code != signature.CodeInvalid {
		t.Errorf("replayed onto another repository: %+v", r)
	}

	ns := newSigFixture(t, sigtest.SKEd25519, func(f *sigFixture) []byte {
		return f.key.SignTuple(t, SignatureTuple(f.d, sigtest.Repository), sigtest.Options{Namespace: "file"})
	})
	if r := ns.record(ns.compute(hostListing("ana", ns.key), nil), "brd.fees"); r.Assurance.Code != signature.CodeNamespace {
		t.Errorf("wrong namespace: %+v", r)
	}
}

// Acceptance 10: removing the key from either anchor un-confirms what it
// signed, on the next confirm run and for every reader of an earlier file.
// @verifies confirm:INV-10
// @verifies confirm:INV-11
// @verifies confirm:INV-12
func TestSignatureRevocation(t *testing.T) {
	f := newSigFixture(t, sigtest.SKEd25519, nil)
	lattice := filepath.Join(f.r.Dir, "lattice")
	before := f.compute(hostListing("ana", f.key), nil)
	if f.record(before, "brd.fees").State != StateConfirmed {
		t.Fatal("control: confirmed before revocation")
	}
	if err := SaveCache(lattice, before); err != nil {
		t.Fatal(err)
	}

	if r := f.record(f.compute(hostListing("ana"), nil), "brd.fees"); r.State != StatePending || r.Assurance.Code != signature.CodeNotOnHost {
		t.Errorf("removed from GitHub: %+v", r)
	}

	f.r.Write("lattice/allowed_signers", "# ana's key was lost\n")
	f.r.Commit("revoke ana")
	if r := f.record(f.compute(hostListing("ana", f.key), nil), "brd.fees"); r.State != StatePending || r.Assurance.Code != signature.CodeRevoked {
		t.Errorf("removed from allowed_signers: %+v", r)
	}
	back := LoadCache(lattice)
	if back == nil || back.Records[0].State != StateUnknown || back.Records[0].Assurance.Code != signature.CodeRevoked {
		t.Errorf("the earlier file's verdict is re-verified and dropped: %+v", back)
	}
	if off := Offline(back, "head"); off.Records[0].State == StateConfirmed {
		t.Errorf("--offline does not take the verdict from the cache: %+v", off.Records[0])
	}
}

// Acceptance 11: the review route's results are what they were, apart from
// via: review; either route suffices.
// @verifies confirm:INV-1
// @verifies confirm:INV-2
func TestReviewRouteUnchanged(t *testing.T) {
	plain := sigtest.NewRepo(t)
	d := decision(ActionApprove)
	body, _ := d.Marshal()
	plain.Write("lattice/"+d.RelPath(), string(body))
	plain.Commit("decision")
	found, _, _ := Scan(Git{Dir: plain.Dir}, "main", "lattice")

	platforms := map[string]Platform{
		"approved":      &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}},
		"self-approval": &fake{pr: merged("dev", "h2"), reviews: []Review{approval("dev", "h2")}},
		"stale head":    &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h1")}},
		"no PR":         &fake{},
		"error":         &fake{err: errors.New("boom")},
		"no token":      nil,
	}
	for name, p := range platforms {
		in := Input{Commit: "head", Branch: "main", Found: found, Platform: p, Now: fixedNow}
		without := Compute(context.Background(), in)
		in.Signed = &signature.Repo{Dir: plain.Dir, LatticeRel: "lattice", Ref: "main", Repository: sigtest.Repository}
		with := Compute(context.Background(), in)
		got := with.Records[0]
		if got.Via != ViaReview {
			t.Errorf("%s: via %q", name, got.Via)
		}
		want := without.Records[0]
		got.Via, want.Via = "", ""
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the review route changed:\n got %+v\nwant %+v", name, got, want)
		}
	}

	// A signature that fails does not stop an approving review from counting.
	soft := newSigFixture(t, sigtest.Software, nil)
	host := hostListing("ana", soft.key)
	host.pr, host.reviews = merged("dev", "h2"), []Review{approval("bo", "h2")}
	if r := soft.record(soft.compute(host, nil), "brd.fees"); r.State != StateConfirmed || r.Via != ViaReview || r.ApproverLogin != "bo" {
		t.Errorf("either route suffices: %+v", r)
	}
}
