package signature_test

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
)

func tuple() signature.Tuple {
	return signature.Tuple{Repository: "acme/shop", ID: "brd.fees", Version: 3, Action: "brd.approve",
		PacketHash: "sha256:9e1f04ab", Rows: []string{"fees:INV-2", "fees:INV-1"}}
}

// The message carries every field of the tuple, and a tuple missing any of
// them has no message: nothing signs or accepts it.
// @verifies confirm:INV-9
func TestMessageNeedsEveryField(t *testing.T) {
	msg, err := tuple().Message()
	if err != nil {
		t.Fatal(err)
	}
	want := "lattice-decision/v1\nrepository acme/shop\nid brd.fees\nversion 3\naction brd.approve\npacket sha256:9e1f04ab\nrows fees:INV-1,fees:INV-2\n"
	if string(msg) != want {
		t.Fatalf("message:\n%s\nwant:\n%s", msg, want)
	}
	upper := tuple()
	upper.Repository = "Acme/Shop"
	if m, _ := upper.Message(); string(m) != want {
		t.Error("the repository is compared without case, as the code host does")
	}
	for name, mut := range map[string]func(*signature.Tuple){
		"repository": func(t *signature.Tuple) { t.Repository = "" },
		"id":         func(t *signature.Tuple) { t.ID = "" },
		"version":    func(t *signature.Tuple) { t.Version = 0 },
		"action":     func(t *signature.Tuple) { t.Action = "" },
		"packet":     func(t *signature.Tuple) { t.PacketHash = " " },
		"line break": func(t *signature.Tuple) { t.ID = "brd.fees\nid brd.other" },
		"bad row":    func(t *signature.Tuple) { t.Rows = []string{"a,b"} },
	} {
		tu := tuple()
		mut(&tu)
		if _, err := tu.Message(); err == nil {
			t.Errorf("%s: a message missing a field must not exist", name)
		}
		k := sigtest.NewKey(t, sigtest.SKEd25519)
		good := k.SignTuple(t, tuple(), sigtest.Options{})
		if _, f := signature.Verify(good, tu); f == nil || f.Code != signature.CodeIncomplete || !f.Unknown {
			t.Errorf("%s: verifying against an incomplete tuple must read unknown, got %+v", name, f)
		}
	}
	none := tuple()
	none.Rows = nil
	if m, _ := none.Message(); !strings.HasSuffix(string(m), "rows none\n") {
		t.Errorf("a decision with no rows signs rows none: %s", m)
	}
}

// Security-key signatures verify; a software key is refused with
// SOFTWARE_KEY, and a signature without user presence is refused.
// @verifies confirm:INV-8
func TestVerifyKeyTypes(t *testing.T) {
	for _, kind := range []string{sigtest.SKEd25519, sigtest.SKECDSA} {
		k := sigtest.NewKey(t, kind)
		for _, alg := range []string{"sha512", "sha256"} {
			v, f := signature.Verify(k.SignTuple(t, tuple(), sigtest.Options{HashAlg: alg}), tuple())
			if f != nil || v.Fingerprint != k.Fingerprint() {
				t.Errorf("%s/%s: a good signature must verify: %+v", kind, alg, f)
			}
		}
		if _, f := signature.Verify(k.SignTuple(t, tuple(), sigtest.Options{NoPresence: true}), tuple()); f == nil || f.Code != signature.CodeNoPresence {
			t.Errorf("%s: no user presence must be refused, got %+v", kind, f)
		}
	}
	soft := sigtest.NewKey(t, sigtest.Software)
	v, f := signature.Verify(soft.SignTuple(t, tuple(), sigtest.Options{}), tuple())
	if f == nil || f.Code != signature.CodeSoftwareKey || f.Unknown {
		t.Fatalf("a software key must be refused with SOFTWARE_KEY, got %+v", f)
	}
	if v.Fingerprint != soft.Fingerprint() {
		t.Error("a refused signature still names its key")
	}
}

// A signature replayed onto another repository, decision, version, action,
// packet or set of promises does not verify; neither does one made in
// another namespace.
// @verifies confirm:INV-9
func TestVerifyBindsTheTuple(t *testing.T) {
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	sig := k.SignTuple(t, tuple(), sigtest.Options{})
	for name, mut := range map[string]func(*signature.Tuple){
		"repository": func(t *signature.Tuple) { t.Repository = "acme/other" },
		"decision":   func(t *signature.Tuple) { t.ID = "brd.wallet" },
		"version":    func(t *signature.Tuple) { t.Version = 4 },
		"action":     func(t *signature.Tuple) { t.Action = "brd.ack" },
		"packet":     func(t *signature.Tuple) { t.PacketHash = "sha256:00000000" },
		"rows":       func(t *signature.Tuple) { t.Rows = append(t.Rows, "fees:INV-3") },
	} {
		tu := tuple()
		mut(&tu)
		if _, f := signature.Verify(sig, tu); f == nil || f.Code != signature.CodeInvalid {
			t.Errorf("replayed onto another %s: got %+v", name, f)
		}
	}
	other := k.SignTuple(t, tuple(), sigtest.Options{Namespace: "git"})
	if _, f := signature.Verify(other, tuple()); f == nil || f.Code != signature.CodeNamespace {
		t.Errorf("another namespace: got %+v", f)
	}
	for name, bad := range map[string][]byte{
		"empty":      nil,
		"not armour": []byte("hello"),
		"truncated":  []byte(strings.Replace(string(sig), "\n-----END", "AAAA\n-----END", 1)),
	} {
		if _, f := signature.Verify(bad, tuple()); f == nil || f.Code != signature.CodeMalformed {
			t.Errorf("%s: got %+v", name, f)
		}
	}
}

// allowed_signers grants a key only what a plain line says: exact
// principals, the lattice-decision namespace if namespaces is set, and no
// line with cert-authority, validity or a pattern.
// @verifies confirm:INV-10
func TestAllowedSigners(t *testing.T) {
	k := sigtest.NewKey(t, sigtest.SKEd25519)
	key := k.AuthorizedKey()
	data := strings.Join([]string{
		"# trusted signers",
		"",
		"ana " + key + " ana's key",
		`bo,cy namespaces="git,lattice-decision" ` + key,
		`dee namespaces="git" ` + key,
		`*@example.com ` + key,
		`eve cert-authority ` + key,
		`fay valid-before="20300101" ` + key,
		`gus not-a-key`,
	}, "\n")
	signers, skipped := signature.ParseAllowedSigners([]byte(data))
	got := signature.Principals(signers, k.Pub, signature.Namespace)
	if strings.Join(got, ",") != "ana,bo,cy" {
		t.Errorf("principals %v", got)
	}
	if len(skipped) != 4 {
		t.Errorf("pattern, cert-authority, valid-before and a bad line are skipped: %v", skipped)
	}
	if other := sigtest.NewKey(t, sigtest.SKEd25519); len(signature.Principals(signers, other.Pub, signature.Namespace)) != 0 {
		t.Error("another key is granted nothing")
	}
}
