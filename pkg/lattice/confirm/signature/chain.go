package signature

// @module-feature confirm

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"path"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

// SignersSigFile is the endorsement of the latest change to allowed_signers,
// relative to lattice/: a detached signature, beside the file, by a key the
// previous version trusted.
const SignersSigFile = AllowedSignersFile + Ext

// SignersMessageVersion is the first line of an endorsement's message. It
// differs from a decision's, so neither kind of signature verifies as the
// other.
const SignersMessageVersion = "lattice-signers/v1"

// Codes of a change to allowed_signers, and of a decision signed by a key
// that was added without an endorsement.
const (
	CodeRootSigners    = "ROOT_SIGNERS"        // the first allowed_signers: the root of trust, accepted as is
	CodeEndorsed       = "SIGNERS_ENDORSED"    // signers added, endorsed by a key trusted before the change
	CodeRemoved        = "SIGNERS_REMOVED"     // signers removed and none added: needs no endorsement
	CodeEdited         = "SIGNERS_EDITED"      // no signer added or removed
	CodeNotEndorsed    = "SIGNER_NOT_ENDORSED" // signers added without a valid endorsement: they do not count
	CodeSignersHistory = "SIGNERS_NO_HISTORY"  // the history of allowed_signers cannot be read in full
)

// SignersChange is what an endorsement covers: a new allowed_signers, in a
// repository, after the commit that last changed the file on the default
// branch. Binding that commit, not only the content, keeps an old
// endorsement from re-adding a key that was removed since.
type SignersChange struct {
	// Repository is the code host's owner/name, lowercased.
	Repository string
	// After is the commit on the default branch's first-parent history that
	// last changed allowed_signers before this change, or "none".
	After string
	// Signers is the new allowed_signers.
	Signers []byte
}

// SignersDigest is the hash an endorsement pins for allowed_signers' content,
// with line endings normalised so a checkout's CRLF does not change it.
func SignersDigest(data []byte) string {
	sum := sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Message is the endorsement's signed message, computed by the signer and
// the verifier alike and never stored.
func (c SignersChange) Message() ([]byte, error) {
	repo := strings.ToLower(strings.TrimSpace(c.Repository))
	after := strings.TrimSpace(c.After)
	switch {
	case repo == "" || strings.ContainsAny(repo, "\n\r"):
		return nil, fail(CodeIncomplete, "the endorsement message needs a repository")
	case after == "" || strings.ContainsAny(after, "\n\r "):
		return nil, fail(CodeIncomplete, "the endorsement message needs the commit it follows")
	}
	return []byte(SignersMessageVersion + "\nrepository " + repo + "\nafter " + after + "\nsigners " + SignersDigest(c.Signers) + "\n"), nil
}

// VerifyChange checks an endorsement over a change to allowed_signers: the
// same format, key type, namespace and user-presence checks as a decision's
// signature. Whether the key was trusted before the change is Trust's
// question.
// @enforces confirm:INV-17
func VerifyChange(armoured []byte, c SignersChange) (Verified, *Failure) {
	msg, err := c.Message()
	if err != nil {
		f := asFailure(err)
		f.Unknown = true
		return Verified{}, f
	}
	return verifyMessage(armoured, msg, "this change to allowed_signers (repository, the commit it follows, its content)")
}

// grant is one login allowed_signers grants a key in the decision namespace.
type grant struct {
	Login string
	Key   ssh.PublicKey
}

func (g grant) id() string {
	return strings.ToLower(g.Login) + " " + base64.StdEncoding.EncodeToString(g.Key.Marshal())
}

func (g grant) String() string { return g.Login + " " + ssh.FingerprintSHA256(g.Key) }

// grants lists what an allowed_signers grants in the decision namespace.
func grants(data []byte) map[string]grant {
	out := map[string]grant{}
	signers, _ := ParseAllowedSigners(data)
	for _, s := range signers {
		if len(s.Namespaces) > 0 && !contains(s.Namespaces, Namespace) {
			continue
		}
		for _, p := range s.Principals {
			g := grant{Login: p, Key: s.Key}
			out[g.id()] = g
		}
	}
	return out
}

// Change is one commit on the default branch's first-parent history that
// changed allowed_signers, and what it did to trust.
type Change struct {
	Commit string `json:"commit"`
	// Code is ROOT_SIGNERS, SIGNERS_ENDORSED, SIGNERS_REMOVED, SIGNERS_EDITED
	// or SIGNER_NOT_ENDORSED.
	Code string `json:"code"`
	// Added and Removed are "<login> <fingerprint>" grants.
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	// EndorsedBy are the logins the endorsing key was trusted for before the
	// change, and Fingerprint is that key's.
	EndorsedBy  []string `json:"endorsed_by,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Text        string   `json:"text"`
}

// Trust is who allowed_signers trusts as of one commit, after following every
// change to it along that commit's first-parent history: the first version
// with a signer is the root, each later addition counts only when an
// endorsement by a key trusted before it sits beside it in the same commit,
// and a removal always counts.
type Trust struct {
	Rev string
	// Root is the commit whose allowed_signers is the root of trust, or ""
	// when no version has granted a signer.
	Root    string
	Changes []Change
	// Failure is set when the history cannot be followed (a shallow clone):
	// nothing is trusted and a signature reads unknown.
	Failure *Failure

	trusted map[string]grant // the grants that count at Rev
	listed  map[string]grant // what the file at Rev grants
}

// Trust follows allowed_signers along rev's first-parent history.
// @enforces confirm:INV-17
func (r Repo) Trust(rev string) Trust {
	t := Trust{Rev: rev, trusted: map[string]grant{}, listed: map[string]grant{}}
	file := path.Join(r.LatticeRel, AllowedSignersFile)
	sigFile := path.Join(r.LatticeRel, SignersSigFile)
	if shallow, _ := r.line("rev-parse", "--is-shallow-repository"); shallow == "true" {
		t.Failure = &Failure{Code: CodeSignersHistory, Unknown: true,
			Text: "this is a shallow clone: the history of " + file + " cannot be followed back to its root, so no signer can be shown to be endorsed (fetch the full history)"}
		return t
	}
	out, err := r.run("log", "--first-parent", "--reverse", "--format=%H", rev, "--", file)
	if err != nil {
		t.Failure = &Failure{Code: CodeSignersHistory, Unknown: true, Text: "the history of " + file + " on " + rev + " could not be read"}
		return t
	}
	after := "none"
	for _, c := range strings.Fields(string(out)) {
		next, _ := r.show(c, file)
		var prev []byte
		if parent, err := r.line("rev-parse", "--verify", "--quiet", c+"^1"); err == nil && parent != "" {
			prev, _ = r.show(parent, file)
		}
		nextG, prevG := grants(next), grants(prev)
		ch := Change{Commit: c}
		var added, removed []grant
		for id, g := range nextG {
			if _, ok := prevG[id]; !ok {
				added = append(added, g)
			}
		}
		for id, g := range prevG {
			if _, ok := nextG[id]; !ok {
				removed = append(removed, g)
			}
		}
		ch.Added, ch.Removed = names(added), names(removed)
		before := t.trusted

		switch {
		case t.Root == "" && len(nextG) == 0:
			ch.Code, ch.Text = CodeEdited, "no signer yet"
		case t.Root == "":
			t.Root = c
			t.trusted = map[string]grant{}
			for id, g := range nextG {
				t.trusted[id] = g
			}
			ch.Code = CodeRootSigners
			ch.Text = "the first " + file + " with a signer is the root of trust, accepted as is: " + strings.Join(ch.Added, ", ")
		default:
			t.trusted = map[string]grant{}
			for id, g := range before {
				if _, ok := nextG[id]; ok {
					t.trusted[id] = g
				}
			}
			switch {
			case len(added) == 0 && len(removed) > 0:
				ch.Code, ch.Text = CodeRemoved, "removed "+strings.Join(ch.Removed, ", ")+": a removal needs no endorsement"
			case len(added) == 0:
				ch.Code, ch.Text = CodeEdited, "no signer added or removed"
			default:
				ch.Code, ch.EndorsedBy, ch.Fingerprint, ch.Text = r.endorse(c, sigFile, after, next, before)
				if ch.Code == CodeEndorsed {
					for _, g := range added {
						t.trusted[g.id()] = g
					}
				}
			}
		}
		t.Changes = append(t.Changes, ch)
		after = c
	}
	listed, _ := r.show(rev, file)
	t.listed = grants(listed)
	return t
}

// endorse checks the endorsement beside allowed_signers in commit c: it must
// verify over the change and come from a key trusted before it.
func (r Repo) endorse(c, sigFile, after string, next []byte, before map[string]grant) (code string, by []string, fp, text string) {
	sig, err := r.show(c, sigFile)
	if err != nil {
		return CodeNotEndorsed, nil, "", "signers added with no endorsement (" + sigFile + ") in the same commit: they do not count"
	}
	v, f := VerifyChange(sig, SignersChange{Repository: r.Repository, After: after, Signers: next})
	if f != nil {
		return CodeNotEndorsed, nil, v.Fingerprint, "the endorsement does not hold (" + f.Code + ": " + f.Text + "): the signers added do not count"
	}
	by = loginsFor(before, v.Key)
	if len(by) == 0 {
		return CodeNotEndorsed, nil, v.Fingerprint, "the endorsement is by key " + Short(v.Fingerprint) + ", which allowed_signers did not trust before this change: the signers added do not count"
	}
	return CodeEndorsed, by, v.Fingerprint, "endorsed by " + strings.Join(by, ", ") + " with key " + Short(v.Fingerprint)
}

// Logins lists the logins trusted for key at Rev, and the logins the file
// lists for it whose grant was added without an endorsement.
func (t Trust) Logins(key ssh.PublicKey) (trusted, unendorsed []string) {
	if key == nil {
		return nil, nil
	}
	trusted = loginsFor(t.trusted, key)
	for _, l := range loginsFor(t.listed, key) {
		if !containsFold(trusted, l) {
			unendorsed = append(unendorsed, l)
		}
	}
	return trusted, unendorsed
}

// ChangeAdding is the last change that added key for login without an
// endorsement, for the text of a SIGNER_NOT_ENDORSED verdict.
func (t Trust) ChangeAdding(login string, key ssh.PublicKey) *Change {
	want := grant{Login: login, Key: key}.String()
	for i := len(t.Changes) - 1; i >= 0; i-- {
		c := t.Changes[i]
		if c.Code == CodeNotEndorsed {
			for _, a := range c.Added {
				if strings.EqualFold(a, want) {
					return &t.Changes[i]
				}
			}
		}
	}
	return nil
}

func loginsFor(gs map[string]grant, key ssh.PublicKey) []string {
	want := key.Marshal()
	seen := map[string]bool{}
	var out []string
	for _, g := range gs {
		if bytes.Equal(g.Key.Marshal(), want) && !seen[strings.ToLower(g.Login)] {
			seen[strings.ToLower(g.Login)] = true
			out = append(out, g.Login)
		}
	}
	sort.Strings(out)
	return out
}

func names(gs []grant) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.String())
	}
	sort.Strings(out)
	return out
}

// LastChange is the commit on rev's first-parent history that last changed
// allowed_signers, or "none": what an endorsement of the next change follows.
func (r Repo) LastChange(rev string) (string, error) {
	c, err := r.line("log", "--first-parent", "-n", "1", "--format=%H", rev, "--", path.Join(r.LatticeRel, AllowedSignersFile))
	if err != nil {
		return "", err
	}
	if c == "" {
		return "none", nil
	}
	return c, nil
}
