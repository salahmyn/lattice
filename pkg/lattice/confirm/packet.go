package confirm

// @module-feature confirm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// PromiseFacts are the structural signals of one promise in a packet: what
// it says, its tier, and which symbols enforce and verify it. Run-dependent
// signals (exercised, live, break-tested) are left out on purpose: a better
// test run must not void a confirmation. They are recorded on the decision's
// rows for the reader instead.
type PromiseFacts struct {
	Promise   string   `json:"promise"`
	Statement string   `json:"statement"`
	Tier      int      `json:"tier"`
	Enforcers []string `json:"enforcers"`
	Verifiers []string `json:"verifiers"`
}

// PacketInput is everything a review packet hashes.
type PacketInput struct {
	BRD      schema.BRD
	Promises []PromiseFacts
	// Diff is the structural change since the last confirmed version.
	Diff []string
	// Extra pins something outside the BRD, such as the council votes.
	Extra string
}

// PromisesOf lists the promises a BRD's criteria map to, sorted.
func PromisesOf(b schema.BRD) []string {
	seen := map[string]bool{}
	var out []string
	for _, sc := range b.SuccessCriteria {
		for _, ref := range sc.MapsToInvariant {
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	sort.Strings(out)
	return out
}

// FactsFor projects audit rows onto the promises a packet covers.
func FactsFor(rows []audit.Row, refs []string) []PromiseFacts {
	by := map[string]audit.Row{}
	for _, r := range rows {
		by[r.Promise] = r
	}
	out := make([]PromiseFacts, 0, len(refs))
	for _, ref := range refs {
		r, ok := by[ref]
		f := PromiseFacts{Promise: ref, Enforcers: []string{}, Verifiers: []string{}}
		if ok {
			f.Statement, f.Tier = r.Statement, r.Tier
			f.Enforcers = append(f.Enforcers, r.Signals.Enforced.Items...)
			f.Verifiers = append(f.Verifiers, r.Signals.Verified.Items...)
			sort.Strings(f.Enforcers)
			sort.Strings(f.Verifiers)
		}
		out = append(out, f)
	}
	return out
}

// PacketHash is a deterministic hash over the BRD version's content, its
// promises' structural signals and the structural diff since the last
// confirmed version. The BRD's status and approval block are left out: they
// are what a decision changes or displays, and hashing them would make every
// decision stale the moment it landed.
// @enforces confirm:INV-3
func PacketHash(in PacketInput) string {
	b := in.BRD
	b.Status, b.Approval, b.SourcePath, b.HumanReviewRequired = "", nil, "", false
	body, _ := schema.MarshalCanonical(b)
	h := sha256.New()
	fmt.Fprintf(h, "brd\n%s\n", body)
	for _, p := range in.Promises {
		fmt.Fprintf(h, "promise\t%s\t%s\t%d\t%s\t%s\n", p.Promise, p.Statement, p.Tier,
			strings.Join(p.Enforcers, ","), strings.Join(p.Verifiers, ","))
	}
	for _, d := range in.Diff {
		fmt.Fprintf(h, "diff\t%s\n", d)
	}
	fmt.Fprintf(h, "extra\t%s\n", in.Extra)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// WaiverHash pins one waiver entry.
func WaiverHash(w WaiverEntry) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{w.Code, w.Subject, w.Kind, w.Reason, w.ADR}, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// VotesHash pins the council votes behind an ack or overturn.
func VotesHash(votes []schema.BRDCouncilVote) string {
	vs := append([]schema.BRDCouncilVote(nil), votes...)
	sort.Slice(vs, func(i, j int) bool { return vs[i].Seat < vs[j].Seat })
	h := sha256.New()
	for _, v := range vs {
		fmt.Fprintf(h, "%s\t%s\t%s\t%s\n", v.Seat, v.Actor, v.Verdict, v.Reason)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// StructuralDiff lists what changed between the last confirmed version of a
// BRD and the current one: criteria added, removed, or edited in statement,
// tier or mapping, and constraints added or removed. With no previous
// version it reads "initial".
func StructuralDiff(prev *schema.BRD, cur schema.BRD) []string {
	if prev == nil {
		return []string{"initial"}
	}
	var out []string
	pc := map[string]schema.BRDCriterion{}
	for _, c := range prev.SuccessCriteria {
		pc[c.ID] = c
	}
	cc := map[string]bool{}
	for _, c := range cur.SuccessCriteria {
		cc[c.ID] = true
		p, ok := pc[c.ID]
		if !ok {
			out = append(out, "+criterion "+c.ID)
			continue
		}
		if p.Statement != c.Statement {
			out = append(out, "~statement "+c.ID)
		}
		if p.EffectiveTier() != c.EffectiveTier() {
			out = append(out, fmt.Sprintf("~tier %s %d->%d", c.ID, p.EffectiveTier(), c.EffectiveTier()))
		}
		if strings.Join(p.MapsToInvariant, ",") != strings.Join(c.MapsToInvariant, ",") {
			out = append(out, "~mapping "+c.ID)
		}
	}
	for id := range pc {
		if !cc[id] {
			out = append(out, "-criterion "+id)
		}
	}
	key := func(c schema.BRDConstraint) string { return string(c.Kind) + "|" + c.Ref + "|" + c.Note }
	pk := map[string]bool{}
	for _, c := range prev.Constraints {
		pk[key(c)] = true
	}
	ck := map[string]bool{}
	for _, c := range cur.Constraints {
		ck[key(c)] = true
		if !pk[key(c)] {
			out = append(out, "+constraint "+key(c))
		}
	}
	for k := range pk {
		if !ck[k] {
			out = append(out, "-constraint "+k)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}
