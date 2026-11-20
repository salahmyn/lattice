// Package signature is the second route to a human confirmation: a detached
// OpenSSH signature over a decision, made with a hardware-backed key.
//
// The signer runs `lattice sign <decision>` on their own machine, which
// writes `<stem>.sig` beside the decision file. The verifier computes the
// same message from the decision file and the repository, never from the
// signature file, and accepts the signature only when all of these hold:
//
//   - it is an OpenSSH SSHSIG signature in the `lattice-decision` namespace
//     over exactly that message;
//   - the key is a security-key type (sk-ssh-ed25519@openssh.com or
//     sk-ecdsa-sha2-nistp256@openssh.com) and the signature carries the
//     user-presence flag;
//   - the key is listed for the signer's login in lattice/allowed_signers at
//     the base commit (the first parent of the default-branch commit that
//     introduced the signature) and still at the tip of the default branch,
//     and trusted there: listed in the first allowed_signers (the root of
//     trust), or added by a change endorsed by a key trusted before it;
//   - the key is among the login's SSH signing keys on the code host. That
//     check needs the network and lives in package confirm.
//
// Verification is native Go (golang.org/x/crypto/ssh); nothing here runs
// ssh-keygen. The package depends on no other lattice package, so both the
// writer of confirm.json (package confirm) and its readers (package audit)
// re-verify with the same code.
package signature

// @module-feature confirm

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Namespace is the SSHSIG namespace of every decision signature.
const Namespace = "lattice-decision"

// MessageVersion is the first line of the signed message.
const MessageVersion = "lattice-decision/v1"

// Ext is the extension of a signature file, written beside the decision file.
const Ext = ".sig"

const (
	armourBegin = "-----BEGIN SSH SIGNATURE-----"
	armourEnd   = "-----END SSH SIGNATURE-----"
	magic       = "SSHSIG"
	sigVersion  = 1

	// flagUserPresence is the FIDO user-presence bit of an sk signature.
	flagUserPresence = 0x01
)

// Codes a signature verdict carries.
const (
	CodeSigned        = "SIGNED"                   // every check held
	CodeMalformed     = "SIGNATURE_MALFORMED"      // not an SSHSIG signature
	CodeSoftwareKey   = "SOFTWARE_KEY"             // the key is not a security-key type
	CodeNoPresence    = "SIGNATURE_NO_PRESENCE"    // the user-presence flag is missing
	CodeNamespace     = "SIGNATURE_NAMESPACE"      // signed in another namespace
	CodeInvalid       = "SIGNATURE_INVALID"        // does not verify over this decision's message
	CodeIncomplete    = "SIGNATURE_INCOMPLETE"     // the message would miss a field
	CodeNoBase        = "SIGNATURE_NO_BASE"        // no base commit to read allowed_signers from
	CodeNotAllowed    = "SIGNER_NOT_ALLOWED"       // not in allowed_signers at the base commit
	CodeRevoked       = "SIGNER_REVOKED"           // no longer in allowed_signers at the tip
	CodeNotOnHost     = "SIGNER_NOT_ON_GITHUB"     // not among the login's GitHub signing keys
	CodeAnchorUnknown = "GITHUB_ANCHOR_UNKNOWN"    // the signing keys could not be read
	CodeNotReverified = "SIGNATURE_NOT_REVERIFIED" // a reader could not re-verify a claimed verdict
	CodeNotHuman      = "SIGNER_NOT_HUMAN"         // allowed_signers names a bot or agent login for the key
)

// MachineLogin reports whether a login names a bot ("...[bot]") or an agent
// ("agent..."), the same logins the review route refuses as approvers. Such
// a login never signs a decision, whoever holds the key.
func MachineLogin(login string) bool {
	l := strings.ToLower(strings.TrimSpace(login))
	return strings.HasSuffix(l, "[bot]") || strings.HasPrefix(l, "agent")
}

// Failure is why a signature does not count. Unknown is true when the
// verdict cannot be reached (no base commit, no repository name): such a
// decision reads unknown, never human, and never a definite rejection.
type Failure struct {
	Code    string
	Text    string
	Unknown bool
}

func (f *Failure) Error() string { return f.Code + ": " + f.Text }

func fail(code, format string, args ...interface{}) *Failure {
	return &Failure{Code: code, Text: fmt.Sprintf(format, args...)}
}

// Tuple is what a decision signature covers. Every field is required.
type Tuple struct {
	// Repository is the code host's owner/name, lowercased.
	Repository string
	ID         string
	Version    int
	Action     string
	PacketHash string
	// Rows are the promises the decision names. A promise.confirm packet
	// hash does not pin which promises the batch covers, so the message
	// does; a decision without rows signs "none".
	Rows []string
}

// Message is the signed message. It is computed by the signer and the
// verifier alike and never stored. A tuple missing any field has no message.
// @enforces confirm:INV-10
func (t Tuple) Message() ([]byte, error) {
	fields := []struct{ name, value string }{
		{"repository", strings.ToLower(strings.TrimSpace(t.Repository))},
		{"id", t.ID},
		{"version", ""},
		{"action", t.Action},
		{"packet", t.PacketHash},
	}
	if t.Version > 0 {
		fields[2].value = strconv.Itoa(t.Version)
	}
	var b strings.Builder
	b.WriteString(MessageVersion + "\n")
	for _, f := range fields {
		if strings.TrimSpace(f.value) == "" {
			return nil, fail(CodeIncomplete, "the signed message needs a %s", f.name)
		}
		if strings.ContainsAny(f.value, "\n\r") {
			return nil, fail(CodeIncomplete, "the %s holds a line break", f.name)
		}
		b.WriteString(f.name + " " + f.value + "\n")
	}
	rows := append([]string(nil), t.Rows...)
	sort.Strings(rows)
	out := make([]string, 0, len(rows))
	for i, r := range rows {
		if strings.ContainsAny(r, ",\n\r ") || r == "" {
			return nil, fail(CodeIncomplete, "row %q cannot be signed", r)
		}
		if i == 0 || r != rows[i-1] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		b.WriteString("rows none\n")
	} else {
		b.WriteString("rows " + strings.Join(out, ",") + "\n")
	}
	return []byte(b.String()), nil
}

// Signature is a parsed SSHSIG signature.
type Signature struct {
	PublicKey ssh.PublicKey
	Namespace string
	Reserved  []byte
	HashAlg   string
	Sig       *ssh.Signature
}

// Parse reads an armoured SSHSIG signature (PROTOCOL.sshsig).
func Parse(armoured []byte) (*Signature, error) {
	text := strings.TrimSpace(strings.ReplaceAll(string(armoured), "\r\n", "\n"))
	if !strings.HasPrefix(text, armourBegin) || !strings.HasSuffix(text, armourEnd) {
		return nil, fail(CodeMalformed, "not an armoured SSH signature")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, armourBegin), armourEnd)
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return nil, fail(CodeMalformed, "the signature is not base64: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte(magic)) {
		return nil, fail(CodeMalformed, "the signature does not start with %s", magic)
	}
	r := reader{b: raw[len(magic):]}
	version := r.uint32()
	pub, ns, reserved, alg, sig := r.str(), r.str(), r.str(), r.str(), r.str()
	if r.err != nil || len(r.b) != 0 {
		return nil, fail(CodeMalformed, "the signature blob is truncated or has trailing bytes")
	}
	if version != sigVersion {
		return nil, fail(CodeMalformed, "SSHSIG version %d is not supported", version)
	}
	key, err := ssh.ParsePublicKey(pub)
	if err != nil {
		return nil, fail(CodeMalformed, "the public key does not parse: %v", err)
	}
	var s ssh.Signature
	if err := ssh.Unmarshal(sig, &s); err != nil {
		return nil, fail(CodeMalformed, "the signature does not parse: %v", err)
	}
	return &Signature{PublicKey: key, Namespace: string(ns), Reserved: reserved, HashAlg: string(alg), Sig: &s}, nil
}

// SignedData is the blob an SSHSIG signature signs: the magic, the
// namespace, the reserved field, the hash algorithm and the hash of the
// message.
func SignedData(namespace string, reserved []byte, hashAlg string, message []byte) ([]byte, error) {
	var digest []byte
	switch hashAlg {
	case "sha256":
		s := sha256.Sum256(message)
		digest = s[:]
	case "sha512":
		s := sha512.Sum512(message)
		digest = s[:]
	default:
		return nil, fail(CodeMalformed, "hash algorithm %q is not sha256 or sha512", hashAlg)
	}
	var b bytes.Buffer
	b.WriteString(magic)
	for _, f := range [][]byte{[]byte(namespace), reserved, []byte(hashAlg), digest} {
		writeString(&b, f)
	}
	return b.Bytes(), nil
}

// HardwareKey reports whether a key type is a security-key type this route
// accepts.
// @enforces confirm:INV-9
func HardwareKey(keyType string) bool {
	return keyType == ssh.KeyAlgoSKED25519 || keyType == ssh.KeyAlgoSKECDSA256
}

// Verified is what a good signature says about who signed.
type Verified struct {
	Key         ssh.PublicKey
	Fingerprint string
}

// Verify checks an armoured signature over the tuple's message: the format,
// the key type, the namespace, the user-presence flag and the signature
// itself. It says nothing about whether the key is trusted; Repo.Evaluate
// does that.
// @enforces confirm:INV-9, confirm:INV-10
func Verify(armoured []byte, t Tuple) (Verified, *Failure) {
	msg, err := t.Message()
	if err != nil {
		var f *Failure
		errors.As(err, &f)
		f.Unknown = true
		return Verified{}, f
	}
	return verifyMessage(armoured, msg, "this decision (repository, id, version, action, packet, rows)")
}

// verifyMessage checks an armoured signature over msg: the format, the key
// type, the namespace, the user-presence flag and the signature itself.
// what names the message in the failure text.
func verifyMessage(armoured, msg []byte, what string) (Verified, *Failure) {
	s, err := Parse(armoured)
	if err != nil {
		return Verified{}, asFailure(err)
	}
	v := Verified{Key: s.PublicKey, Fingerprint: ssh.FingerprintSHA256(s.PublicKey)}
	if !HardwareKey(s.PublicKey.Type()) {
		return v, fail(CodeSoftwareKey, "the key %s is a %s key: only a hardware-backed security key (sk-ssh-ed25519 or sk-ecdsa-sha2-nistp256) counts", Short(v.Fingerprint), s.PublicKey.Type())
	}
	if s.Namespace != Namespace {
		return v, fail(CodeNamespace, "signed in namespace %q, not %q", s.Namespace, Namespace)
	}
	if s.Sig.Format != s.PublicKey.Type() {
		return v, fail(CodeInvalid, "a %s signature from a %s key", s.Sig.Format, s.PublicKey.Type())
	}
	if len(s.Sig.Rest) < 5 || s.Sig.Rest[0]&flagUserPresence == 0 {
		return v, fail(CodeNoPresence, "the security key did not report user presence (no touch)")
	}
	data, err := SignedData(Namespace, s.Reserved, s.HashAlg, msg)
	if err != nil {
		return v, asFailure(err)
	}
	if err := s.PublicKey.Verify(data, s.Sig); err != nil {
		return v, fail(CodeInvalid, "the signature does not verify over %s: %v", what, err)
	}
	return v, nil
}

// Short is the head of a fingerprint, for display.
func Short(fp string) string {
	const n = len("SHA256:") + 12
	if len(fp) > n {
		return fp[:n]
	}
	return fp
}

func asFailure(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	return fail(CodeMalformed, "%v", err)
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) uint32() uint32 {
	if r.err != nil || len(r.b) < 4 {
		r.err = errors.New("short")
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *reader) str() []byte {
	n := r.uint32()
	if r.err != nil || uint64(len(r.b)) < uint64(n) {
		r.err = errors.New("short")
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}

func writeString(b *bytes.Buffer, s []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	b.Write(n[:])
	b.Write(s)
}
