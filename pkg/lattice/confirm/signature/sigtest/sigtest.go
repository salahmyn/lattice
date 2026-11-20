// Package sigtest builds decision signatures in Go for tests: security-key
// (sk-ssh-ed25519, sk-ecdsa-sha2-nistp256) and software keys, signed the way
// ssh-keygen -Y sign and a FIDO authenticator do, so tests need neither a
// hardware key nor ssh-keygen. It is imported only by tests.
package sigtest

// @module-feature confirm

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
)

// Kind of test key.
const (
	SKEd25519 = "sk-ed25519"
	SKECDSA   = "sk-ecdsa"
	Software  = "ed25519"
)

// Key is a test signing key.
type Key struct {
	Pub  ssh.PublicKey
	kind string
	ed   ed25519.PrivateKey
	ec   *ecdsa.PrivateKey
	app  string
}

// NewKey makes a key of a kind, with application "ssh:" for sk keys.
func NewKey(t testing.TB, kind string) *Key {
	t.Helper()
	k := &Key{kind: kind, app: "ssh:"}
	var err error
	switch kind {
	case SKEd25519:
		var pub ed25519.PublicKey
		pub, k.ed, err = ed25519.GenerateKey(rand.Reader)
		if err == nil {
			k.Pub, err = ssh.ParsePublicKey(ssh.Marshal(struct {
				Name, Key, App string
			}{ssh.KeyAlgoSKED25519, string(pub), k.app}))
		}
	case SKECDSA:
		k.ec, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err == nil {
			//nolint:staticcheck // the wire format is the uncompressed point
			point := elliptic.Marshal(elliptic.P256(), k.ec.X, k.ec.Y)
			k.Pub, err = ssh.ParsePublicKey(ssh.Marshal(struct {
				Name, Curve, Key, App string
			}{ssh.KeyAlgoSKECDSA256, "nistp256", string(point), k.app}))
		}
	case Software:
		var pub ed25519.PublicKey
		pub, k.ed, err = ed25519.GenerateKey(rand.Reader)
		if err == nil {
			k.Pub, err = ssh.NewPublicKey(pub)
		}
	default:
		t.Fatalf("unknown key kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// AuthorizedKey is the key as one authorized_keys / allowed_signers field
// pair, "type base64".
func (k *Key) AuthorizedKey() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.Pub)))
}

// AllowedLine is an allowed_signers line granting the key to login in the
// lattice-decision namespace.
func (k *Key) AllowedLine(login string) string {
	return login + ` namespaces="` + signature.Namespace + `" ` + k.AuthorizedKey() + "\n"
}

// Fingerprint is the key's SHA256 fingerprint.
func (k *Key) Fingerprint() string { return ssh.FingerprintSHA256(k.Pub) }

// Options change how Sign signs, to build bad signatures.
type Options struct {
	Namespace string // default lattice-decision
	HashAlg   string // default sha512, as ssh-keygen
	// NoPresence clears the user-presence flag.
	NoPresence bool
}

// SignTuple signs a decision tuple's message.
func (k *Key) SignTuple(t testing.TB, tu signature.Tuple, o Options) []byte {
	t.Helper()
	msg, err := tu.Message()
	if err != nil {
		t.Fatal(err)
	}
	return k.Sign(t, msg, o)
}

// Sign makes an armoured SSHSIG signature over message.
func (k *Key) Sign(t testing.TB, message []byte, o Options) []byte {
	t.Helper()
	if o.Namespace == "" {
		o.Namespace = signature.Namespace
	}
	if o.HashAlg == "" {
		o.HashAlg = "sha512"
	}
	data, err := signature.SignedData(o.Namespace, nil, o.HashAlg, message)
	if err != nil {
		t.Fatal(err)
	}
	flags := byte(0x01)
	if o.NoPresence {
		flags = 0
	}
	var sig *ssh.Signature
	switch k.kind {
	case Software:
		s, err := ssh.NewSignerFromKey(k.ed)
		if err != nil {
			t.Fatal(err)
		}
		if sig, err = s.Sign(rand.Reader, data); err != nil {
			t.Fatal(err)
		}
	case SKEd25519:
		sig = &ssh.Signature{Format: ssh.KeyAlgoSKED25519, Blob: ed25519.Sign(k.ed, k.skBlob(flags, data)), Rest: skRest(flags)}
	case SKECDSA:
		digest := sha256.Sum256(k.skBlob(flags, data))
		r, s, err := ecdsa.Sign(rand.Reader, k.ec, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		sig = &ssh.Signature{Format: ssh.KeyAlgoSKECDSA256, Blob: ssh.Marshal(struct{ R, S *big.Int }{r, s}), Rest: skRest(flags)}
	}
	return armour(blob(k.Pub, o.Namespace, nil, o.HashAlg, sig))
}

// skBlob is what a FIDO authenticator signs (PROTOCOL.u2f):
// SHA256(application) || flags || counter || SHA256(data).
func (k *Key) skBlob(flags byte, data []byte) []byte {
	app := sha256.Sum256([]byte(k.app))
	msg := sha256.Sum256(data)
	out := append([]byte{}, app[:]...)
	out = append(out, skRest(flags)...)
	return append(out, msg[:]...)
}

func skRest(flags byte) []byte {
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], 7)
	return append([]byte{flags}, c[:]...)
}

// Repo is a throwaway git repository for signature tests, with an origin
// remote naming the GitHub repository acme/shop.
type Repo struct {
	t   testing.TB
	Dir string
}

// Repository is the owner/name the test repository's origin names.
const Repository = "acme/shop"

// NewRepo makes a repository on branch main with one commit.
func NewRepo(t testing.TB) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GITHUB_REPOSITORY", "")
	r := &Repo{t: t, Dir: t.TempDir()}
	r.Git("init", "-q", "-b", "main")
	r.Git("remote", "add", "origin", "https://github.com/"+Repository+".git")
	r.Write("lattice/config.yaml", "x: 1\n")
	r.Write(".gitignore", "lattice/.cache/\n")
	r.Commit("base")
	return r
}

// Git runs git in the repository and returns its trimmed output.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write writes a file, creating its directories.
func (r *Repo) Write(rel string, body string) {
	r.t.Helper()
	p := filepath.Join(r.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Commit commits everything and returns the commit.
func (r *Repo) Commit(msg string) string {
	r.t.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

// Merge merges a branch into the current one with a merge commit, as a
// pull request's merge button does.
func (r *Repo) Merge(branch string) string {
	r.t.Helper()
	r.Git("merge", "-q", "--no-ff", "-m", "merge "+branch, branch)
	return r.Git("rev-parse", "HEAD")
}

// T is the test the repository belongs to.
func (r *Repo) T() testing.TB { return r.t }

// The SSHSIG wire format (PROTOCOL.sshsig), as ssh-keygen -Y sign writes it.
const (
	armourBegin = "-----BEGIN SSH SIGNATURE-----"
	armourEnd   = "-----END SSH SIGNATURE-----"
	magic       = "SSHSIG"
	sigVersion  = 1
)

// armour wraps a raw SSHSIG blob the way ssh-keygen writes it.
func armour(raw []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(raw)
	var b strings.Builder
	b.WriteString(armourBegin + "\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n" + armourEnd + "\n")
	return []byte(b.String())
}

// blob assembles a raw SSHSIG blob from its parts.
func blob(pub ssh.PublicKey, namespace string, reserved []byte, hashAlg string, sig *ssh.Signature) []byte {
	var b bytes.Buffer
	b.WriteString(magic)
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], sigVersion)
	b.Write(v[:])
	for _, f := range [][]byte{pub.Marshal(), []byte(namespace), reserved, []byte(hashAlg), ssh.Marshal(sig)} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(f)))
		b.Write(n[:])
		b.Write(f)
	}
	return b.Bytes()
}
