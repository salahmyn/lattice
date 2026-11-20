package confirm

// @module-feature confirm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
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
	// Cases are the promise's acceptance cases, one "id|kind|statement|
	// enforcer|retired" each: part of what was confirmed, so adding, editing
	// or retiring one is a structural change.
	Cases []string `json:"cases,omitempty"`
}

// PacketInput is everything a review packet hashes.
type PacketInput struct {
	BRD      schema.BRD
	Promises []PromiseFacts
	// Diff is the structural change since the last confirmed version.
	Diff []string
	// Extra pins something outside the BRD, such as the council votes.
	Extra string
	// Declared lists the features whose implements_brd names the BRD
	// (DeclaredBy). With implements_via it makes up the BRD's scope.
	Declared []string
}

// DeclaredBy lists the features whose implements_brd names the BRD id.
func DeclaredBy(features []schema.Manifest, id string) []string {
	var out []string
	for _, f := range features {
		if f.ImplementsBRD == id {
			out = append(out, f.ID)
		}
	}
	return out
}

// Scope is a BRD's scope: the features on either half of a link, its
// implements_via and the features that declare implements_brd for it, sorted
// and once each. Completing a one-sided link leaves it as it was.
func Scope(via, declared []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range append(append([]string{}, via...), declared...) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
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
			for _, c := range r.Cases {
				f.Cases = append(f.Cases, strings.Join([]string{c.ID, c.Kind, c.Statement, c.Enforcer, strconv.FormatBool(c.Retired)}, "|"))
			}
		}
		out = append(out, f)
	}
	return out
}

// PacketHash is a deterministic hash over the BRD version's content, its
// promises' structural signals and the structural diff since the last
// confirmed version. The BRD's status and approval block are left out: they
// are what a decision changes or displays, and hashing them would make every
// decision stale the moment it landed. implements_via is hashed as the BRD's
// Scope: completing a link the feature already declares is bookkeeping and
// keeps the hash, while a feature entering or leaving the scope changes it.
// A source's checked_by and checked_at are left out as well: they record a
// later human check, not a change to the intent. A source's kind, ref, title
// and url stay in, so editing a source makes the decision stale as a
// text-only change (it is not in the structural diff).
// @enforces confirm:INV-3, confirm:INV-8, confirm:INV-9
func PacketHash(in PacketInput) string {
	b := in.BRD
	b.ImplementsVia = Scope(b.ImplementsVia, in.Declared)
	return packetHash(b, in)
}

// legacyPacketHash is PacketHash as versions before scope hashing computed
// it, over implements_via as written. A decision pinned then still matches a
// BRD left as it was.
func legacyPacketHash(in PacketInput) string { return packetHash(in.BRD, in) }

// PacketHashFor is the packet hash to compare with a decision's pinned one:
// the pin itself when the packet still matches it, as PacketHash computes it
// or as versions before scope hashing did, and PacketHash otherwise.
func PacketHashFor(in PacketInput, pinned string) string {
	if h := PacketHash(in); h != pinned && legacyPacketHash(in) != pinned {
		return h
	}
	return pinned
}

func packetHash(b schema.BRD, in PacketInput) string {
	b.Status, b.Approval, b.SourcePath, b.HumanReviewRequired = "", nil, "", false
	b.Sources = schema.WithoutChecks(b.Sources)
	body, _ := schema.MarshalCanonical(b)
	h := sha256.New()
	fmt.Fprintf(h, "brd\n%s\n", body)
	for _, p := range in.Promises {
		fmt.Fprintf(h, "promise\t%s\t%s\t%d\t%s\t%s\n", p.Promise, p.Statement, p.Tier,
			strings.Join(p.Enforcers, ","), strings.Join(p.Verifiers, ","))
		// A promise with no cases hashes as it did before cases existed.
		for _, c := range p.Cases {
			fmt.Fprintf(h, "case\t%s\t%s\n", p.Promise, c)
		}
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
