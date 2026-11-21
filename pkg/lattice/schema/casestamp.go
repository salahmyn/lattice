package schema

// @module-feature schema

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CaseStamp is one acceptance case as a decision recorded it: the case a
// human confirmed. Hash is CaseHash of its kind, statement and enforcer, so
// an edit to any of them reads as an edit after confirmation.
type CaseStamp struct {
	ID        string   `yaml:"id" json:"id"`
	Kind      CaseKind `yaml:"kind" json:"kind"`
	Statement string   `yaml:"statement" json:"statement"`
	Enforcer  string   `yaml:"enforcer,omitempty" json:"enforcer,omitempty"`
	Retired   bool     `yaml:"retired,omitempty" json:"retired,omitempty"`
	Hash      string   `yaml:"hash" json:"hash"`
}

// PromiseCases are the cases of one promise a decision recorded. An empty
// Cases says the promise had none when it was confirmed; a promise with no
// PromiseCases entry at all says the decision did not record its cases
// (decisions prepared before cases were recorded).
type PromiseCases struct {
	Promise string      `yaml:"promise" json:"promise"`
	Cases   []CaseStamp `yaml:"cases" json:"cases"`
}

// CaseHash hashes what a case claims: its kind, statement and enforcer.
func CaseHash(kind CaseKind, statement, enforcer string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{string(kind), statement, enforcer}, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NewCaseStamp stamps one case as it stands.
func NewCaseStamp(id string, kind CaseKind, statement, enforcer string, retired bool) CaseStamp {
	return CaseStamp{ID: id, Kind: kind, Statement: statement, Enforcer: enforcer, Retired: retired,
		Hash: CaseHash(kind, statement, enforcer)}
}

// CasesOf finds the cases recorded for a promise. ok is false when none were
// recorded for it.
func CasesOf(recorded []PromiseCases, promise string) (stamps []CaseStamp, ok bool) {
	for _, pc := range recorded {
		if pc.Promise == promise {
			return pc.Cases, true
		}
	}
	return nil, false
}

// Delta lists what changed from the stamp s to the case now, cur: "kind
// edge->error", "statement", "enforcer a->b", "retired" or "restored". It is
// empty when the case is as it was stamped. A stamp whose hash does not
// match its own fields reads as an edited statement, so a stamp that was
// tampered with never reads unchanged.
func (s CaseStamp) Delta(cur CaseStamp) []string {
	var out []string
	if s.Kind != cur.Kind {
		out = append(out, "kind "+arrow(string(s.Kind), string(cur.Kind)))
	}
	if s.Statement != cur.Statement || s.Hash != CaseHash(s.Kind, s.Statement, s.Enforcer) {
		out = append(out, "statement")
	}
	if s.Enforcer != cur.Enforcer {
		out = append(out, "enforcer "+arrow(s.Enforcer, cur.Enforcer))
	}
	switch {
	case !s.Retired && cur.Retired:
		out = append(out, "retired")
	case s.Retired && !cur.Retired:
		out = append(out, "restored")
	}
	return out
}

func arrow(from, to string) string {
	if from == "" {
		from = "(none)"
	}
	if to == "" {
		to = "(none)"
	}
	return from + "->" + to
}
