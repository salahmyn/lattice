package signature

// @module-feature confirm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// ApprovalsDir is where decision files and their signatures live, relative
// to lattice/.
const ApprovalsDir = "decisions/approvals"

// ActionPromiseConfirm is the one action whose rows say what it confirms.
const ActionPromiseConfirm = "promise.confirm"

// Decision is what a signature covers, read from the decision file.
type Decision struct {
	Action     string `yaml:"action"`
	ID         string `yaml:"id"`
	Version    int    `yaml:"version"`
	PacketHash string `yaml:"packet_hash"`
	Rows       []struct {
		Promise string `yaml:"promise"`
	} `yaml:"rows"`
}

// Promises lists the decision's rows.
func (d Decision) Promises() []string {
	out := make([]string, 0, len(d.Rows))
	for _, r := range d.Rows {
		out = append(out, r.Promise)
	}
	return out
}

// Tuple is the decision's signed tuple in a repository.
func (d Decision) Tuple(repository string) Tuple {
	return Tuple{Repository: repository, ID: d.ID, Version: d.Version, Action: d.Action, PacketHash: d.PacketHash, Rows: d.Promises()}
}

// SigPath is the signature file beside a decision file.
func SigPath(decisionPath string) string {
	return strings.TrimSuffix(decisionPath, path.Ext(decisionPath)) + Ext
}

// Repo reads decision signatures from one repository with git plumbing, by
// revision only: never the index or the working tree.
type Repo struct {
	// Dir is the repository's top-level directory.
	Dir string
	// LatticeRel is lattice/ relative to Dir, with forward slashes.
	LatticeRel string
	// Ref is the default branch: where decisions, signatures and the tip's
	// allowed_signers are read, and whose first-parent history says which
	// commit introduced a signature.
	Ref string
	// Repository is the code host's owner/name.
	Repository string
}

// Evidence is what Evaluate found for a signature that verifies.
type Evidence struct {
	// Path is the signature file, from the repository root.
	Path        string
	Key         ssh.PublicKey
	Fingerprint string
	// Introduced is the commit on Ref's first-parent history that added or
	// last changed the signature file: the merge or squash commit that
	// landed it on the default branch.
	Introduced string
	// Base is Introduced's first parent: the default branch before that
	// change. allowed_signers is read here.
	Base string
	// Logins are the principals allowed_signers grants the key, at Base and
	// still at the tip of Ref.
	Logins []string
}

// Evaluate checks the signature beside a decision file, as of r.Ref. present
// is false when there is no signature file. On a failure the evidence still
// carries the fingerprint when the signature parsed.
// @enforces confirm:INV-11
func (r Repo) Evaluate(decisionPath string, d Decision) (ev Evidence, f *Failure, present bool) {
	ev.Path = SigPath(decisionPath)
	data, err := r.show(r.Ref, ev.Path)
	if err != nil {
		return ev, nil, false
	}
	v, f := Verify(data, d.Tuple(r.Repository))
	ev.Key, ev.Fingerprint = v.Key, v.Fingerprint
	if f != nil {
		return ev, f, true
	}
	ev.Introduced, _ = r.line("log", "--first-parent", "--no-renames", "--diff-filter=AM", "--format=%H", "-n", "1", r.Ref, "--", ev.Path)
	if ev.Introduced == "" {
		return ev, &Failure{Code: CodeNoBase, Unknown: true,
			Text: "the commit that introduced " + ev.Path + " on " + r.Ref + " was not found"}, true
	}
	ev.Base, err = r.line("rev-parse", "--verify", "--quiet", ev.Introduced+"^1")
	if err != nil || ev.Base == "" {
		return ev, &Failure{Code: CodeNoBase, Unknown: true,
			Text: "commit " + short(ev.Introduced) + " that introduced the signature has no parent here (a root commit, or a shallow clone): allowed_signers has no base to be read at"}, true
	}
	allowed := path.Join(r.LatticeRel, AllowedSignersFile)
	atBase := r.principals(ev.Base, allowed, v.Key)
	if len(atBase) == 0 {
		return ev, fail(CodeNotAllowed, "key %s is not in %s at the base commit %s, the default branch before %s introduced the signature: a key enrolled in the same change does not count",
			Short(ev.Fingerprint), allowed, short(ev.Base), short(ev.Introduced)), true
	}
	atTip := r.principals(r.Ref, allowed, v.Key)
	for _, l := range atBase {
		if containsFold(atTip, l) {
			ev.Logins = append(ev.Logins, l)
		}
	}
	if len(ev.Logins) == 0 {
		return ev, fail(CodeRevoked, "key %s was in %s at %s but is no longer at the tip of %s: removing a key un-confirms what it signed",
			Short(ev.Fingerprint), allowed, short(ev.Base), r.Ref), true
	}
	return ev, nil, true
}

func (r Repo) principals(rev, file string, key ssh.PublicKey) []string {
	data, err := r.show(rev, file)
	if err != nil {
		return nil
	}
	signers, _ := ParseAllowedSigners(data)
	return Principals(signers, key, Namespace)
}

func (r Repo) show(rev, p string) ([]byte, error) { return r.run("show", rev+":"+p) }

func (r Repo) line(args ...string) (string, error) {
	out, err := r.run(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), nil
}

func (r Repo) run(args ...string) ([]byte, error) { return runGit(r.Dir, args...) }

func runGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.quotePath=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

var githubRemote = regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/]+?)(?:\.git)?/?$`)

// ParseGitHubRemote reads owner and repo from a remote URL:
// https://github.com/o/r(.git) or git@github.com:o/r(.git).
func ParseGitHubRemote(remote string) (owner, repo string, ok bool) {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// RepositoryOf names the repository at dir as the code host knows it:
// GITHUB_REPOSITORY when set, else the origin remote's owner/name,
// lowercased. It is "" when neither is known, and a message needs it.
func RepositoryOf(dir string) string {
	if slug := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY")); strings.Count(slug, "/") == 1 {
		return strings.ToLower(slug)
	}
	out, err := runGit(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	owner, repo, ok := ParseGitHubRemote(string(out))
	if !ok {
		return ""
	}
	return strings.ToLower(owner + "/" + repo)
}

// Claim is a confirm.json record's claim that a signature confirms a
// decision: what to look for, and the login and fingerprint the code host
// vouched for when `lattice confirm` ran online.
type Claim struct {
	ID           string
	Version      int
	Action       string
	Rows         []string
	DecisionPath string
	Login        string
	Fingerprint  string
}

// Recheck re-verifies signature claims from the repository holding
// latticeDir, at ref (the branch `lattice confirm` read). The signature, the
// message, the key type and both allowed_signers reads are done again here,
// from git objects; only the code host's word that the key is the login's
// signing key is taken from the claim. It returns one entry per claim, nil
// when the claim holds.
// @enforces confirm:INV-13
func Recheck(latticeDir, ref string, claims []Claim) []*Failure {
	out := make([]*Failure, len(claims))
	if len(claims) == 0 {
		return out
	}
	all := func(f *Failure) []*Failure {
		for i := range out {
			out[i] = f
		}
		return out
	}
	topOut, err := runGit(latticeDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return all(notReverified("the repository could not be read to re-verify the signature"))
	}
	top := strings.TrimSpace(string(topOut))
	rel, err := filepath.Rel(evalLinks(top), evalLinks(latticeDir))
	if err != nil || strings.HasPrefix(rel, "..") {
		return all(notReverified("lattice/ is outside the repository"))
	}
	if strings.TrimSpace(ref) == "" {
		return all(notReverified("confirm.json names no branch to re-verify the signature on"))
	}
	if _, err := runGit(top, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return all(notReverified("branch " + ref + " that confirm.json names is not here"))
	}
	r := Repo{Dir: top, LatticeRel: filepath.ToSlash(rel), Ref: ref, Repository: RepositoryOf(top)}
	for i, c := range claims {
		out[i] = r.recheck(c)
	}
	return out
}

func (r Repo) recheck(c Claim) *Failure {
	p := path.Clean(c.DecisionPath)
	dir := path.Join(r.LatticeRel, ApprovalsDir) + "/"
	if p != c.DecisionPath || !strings.HasPrefix(p, dir) || path.Ext(p) != ".yaml" {
		return notReverified("the claimed decision file " + c.DecisionPath + " is not a decision file")
	}
	data, err := r.show(r.Ref, p)
	if err != nil {
		return notReverified("the decision file " + p + " is not on " + r.Ref)
	}
	var d Decision
	if yaml.Unmarshal(data, &d) != nil || d.ID != c.ID || d.Version != c.Version || d.Action != c.Action {
		return notReverified("the decision file " + p + " does not hold this decision")
	}
	if c.Action == ActionPromiseConfirm && !sameSet(d.Promises(), c.Rows) {
		return notReverified("the decision file " + p + " names other promises than the claim")
	}
	ev, f, present := r.Evaluate(p, d)
	switch {
	case !present:
		return notReverified("there is no signature " + SigPath(p) + " on " + r.Ref)
	case f != nil:
		return f
	case ev.Fingerprint != c.Fingerprint || !containsFold(ev.Logins, c.Login):
		return notReverified(fmt.Sprintf("the signature is by key %s for %s, not the key %s of %s the code host vouched for",
			Short(ev.Fingerprint), strings.Join(ev.Logins, ","), Short(c.Fingerprint), c.Login))
	}
	return nil
}

func notReverified(text string) *Failure {
	return &Failure{Code: CodeNotReverified, Unknown: true, Text: text}
}

func sameSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\n") == strings.Join(y, "\n")
}

func evalLinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
