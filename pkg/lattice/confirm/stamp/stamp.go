// Package stamp is the provenance of lattice/.cache/confirm.json: what
// produced the file (the commit, the CI run, the platform) and a digest of
// its verdicts. It is honest provenance, not a signature: there is no secret,
// so a careful forger can recompute the digest. What the stamp does is make a
// hand-edited or hand-written file read as such, and tell a CI run from a
// local one, so that `lattice check --ci` can refuse anything CI did not
// produce at the head commit.
//
// The writer (package confirm) and the readers (package audit, and confirm's
// own loader) share this package because audit cannot import confirm.
package stamp

// @module-feature confirm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalRun is the ci_run of a file produced outside CI.
const LocalRun = "local"

// Codes a reader gives a confirm.json it sets aside.
const (
	// CodeUnstamped: the file carries no stamp or no digest, so nothing says
	// `lattice confirm` produced it.
	CodeUnstamped = "CONFIRM_UNSTAMPED"
	// CodeTampered: the digest does not match the records, the commit or the
	// CI run the file carries: it was edited after it was written.
	CodeTampered = "CONFIRM_TAMPERED"
	// CodeTracked: the file is tracked by git. It is derived and never
	// committed; a committed copy is someone's claim, not a run's result.
	CodeTracked = "CONFIRM_TRACKED"
	// CodeNotCI: under --ci, the file was produced outside CI or at another
	// commit than the head.
	CodeNotCI = "EVIDENCE_NOT_CI"
)

// Stamp says what produced a confirm.json. Digest covers every other field
// and the file's records.
type Stamp struct {
	// Commit is the commit `lattice confirm` ran at.
	Commit string `json:"commit"`
	// CIRun is the CI run id (GITHUB_RUN_ID, CI_PIPELINE_ID, ...) or "local".
	CIRun string `json:"ci_run"`
	// Platform is the code host the approvals were read from ("github").
	Platform string `json:"platform,omitempty"`
	// Repository is the platform's owner/name, when known.
	Repository string `json:"repository,omitempty"`
	// Digest is "sha256:<hex>" of the canonical JSON of the fields above and
	// the records.
	Digest string `json:"digest"`
}

// Local reports whether the stamp is of a run outside CI.
func (s Stamp) Local() bool {
	return strings.TrimSpace(s.CIRun) == "" || s.CIRun == LocalRun
}

// Digest is the digest of a stamp's fields and the records, as raw JSON. The
// records are re-encoded canonically (sorted keys, no spaces, numbers as
// written), so the digest does not depend on indentation or on which Go
// struct decoded them.
func Digest(s Stamp, records json.RawMessage) (string, error) {
	var recs interface{}
	if len(bytes.TrimSpace(records)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(records))
		dec.UseNumber()
		if err := dec.Decode(&recs); err != nil {
			return "", err
		}
	}
	if recs == nil {
		recs = []interface{}{}
	}
	canon, err := json.Marshal(map[string]interface{}{
		"commit": s.Commit, "ci_run": s.CIRun, "platform": s.Platform, "repository": s.Repository, "records": recs,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Seal returns the stamp with its digest set for records (any value that
// encodes to the records array).
func Seal(s Stamp, records interface{}) (Stamp, error) {
	raw, err := json.Marshal(records)
	if err != nil {
		return s, err
	}
	s.Digest, err = Digest(s, raw)
	return s, err
}

// Result is a reader's verdict on a confirm.json. Code is empty when the
// stamp is intact.
type Result struct {
	Stamp *Stamp
	Code  string
	Text  string
}

// OK reports whether the file may be read as the record of a run.
func (r Result) OK() bool { return r.Code == "" }

// Check reads the stamp of confirm.json's bytes, found at path, and decides
// whether its verdicts may be read: the file is not tracked by git, it
// carries a stamp with a digest, and the digest matches its records, its
// commit and its CI run.
// @enforces confirm:INV-6
func Check(path string, data []byte) Result {
	var f struct {
		Commit  string          `json:"commit"`
		Stamp   *Stamp          `json:"stamp"`
		Records json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return Result{Code: CodeTampered, Text: "confirm.json does not parse: " + err.Error()}
	}
	if path != "" && Tracked(path) {
		return Result{Stamp: f.Stamp, Code: CodeTracked,
			Text: "confirm.json is tracked by git: it is derived by `lattice confirm` and never committed, so a committed copy is not read"}
	}
	if f.Stamp == nil || strings.TrimSpace(f.Stamp.Digest) == "" {
		return Result{Stamp: f.Stamp, Code: CodeUnstamped,
			Text: "confirm.json carries no stamp: nothing says `lattice confirm` wrote it, so its verdicts are not read"}
	}
	want, err := Digest(*f.Stamp, f.Records)
	if err != nil || want != f.Stamp.Digest || f.Stamp.Commit != f.Commit {
		return Result{Stamp: f.Stamp, Code: CodeTampered,
			Text: "confirm.json's digest does not match its records: it was edited after `lattice confirm` wrote it, so its verdicts are not read"}
	}
	return Result{Stamp: f.Stamp}
}

// ForCI is the --ci rule: only a stamp of a CI run at the head commit counts,
// as for every other evidence file. It returns CodeNotCI and why otherwise.
func ForCI(s *Stamp, head string) (code, text string) {
	switch {
	case s == nil:
		return CodeNotCI, "confirm.json carries no stamp: it reads unknown under --ci"
	case s.Local():
		return CodeNotCI, "confirm.json was produced outside CI (ci_run " + orNone(s.CIRun) + "): it reads unknown under --ci"
	case head != "" && s.Commit != head:
		return CodeNotCI, "confirm.json is for commit " + short(s.Commit) + ", not head " + short(head) + ": it reads unknown under --ci"
	}
	return "", ""
}

// Tracked reports whether the file at path is tracked by git. Outside a
// repository, or without git, it is not.
func Tracked(path string) bool {
	cmd := exec.Command("git", "-C", filepath.Dir(path), "ls-files", "--error-unmatch", "--", filepath.Base(path))
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd.Run() == nil
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

func short(sha string) string {
	if sha == "" {
		return "(none)"
	}
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}
