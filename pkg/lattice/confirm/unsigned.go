package confirm

// @module-feature confirm

import (
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// UnsignedSchema is the contract of `lattice confirm --unsigned`.
const UnsignedSchema = "lattice.confirm-unsigned/1"

// What an unsigned decision rests on now.
const (
	// RestsLocal is a typed name from `lattice brd approve` or `brd ack`.
	RestsLocal = "local"
	// RestsReview is an approving review on the merged pull request.
	RestsReview = "review"
	// RestsCouncil is a council's approval its delegating human has not
	// acknowledged yet: the acknowledgement itself is what to sign.
	RestsCouncil = "council"
)

// Unsigned is a decision on a BRD's current version that rests on a local
// note or a review, and that a hardware-key signature could re-confirm.
type Unsigned struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Action  string `json:"action"`
	// Rests is "local", "review" or "council".
	Rests string `json:"rests"`
	// By is the typed name of a local note, or the reviewer's login.
	By string `json:"by,omitempty"`
	PR int    `json:"pr,omitempty"`
	// DecisionPath is the decision file from the repository root; empty for
	// a local note, which has none until it is prepared.
	DecisionPath string `json:"decision_path,omitempty"`
	// Commands are what to run, in order, at your own terminal; the last is
	// the `lattice sign` command.
	Commands []string `json:"commands"`
	Text     string   `json:"text"`
}

// UnsignedList is lattice.confirm-unsigned/1.
type UnsignedList struct {
	Contract  string     `json:"contract"`
	Decisions []Unsigned `json:"decisions"`
	// Note says why the list may be short: no confirm data, or a file set
	// aside, hides the review route's records.
	Note string `json:"note,omitempty"`
}

// UnsignedInput is what ListUnsigned reads. Nothing in it is written.
type UnsignedInput struct {
	BRDs []schema.BRD
	// Found are the decision files Scan saw; only those on the default
	// branch are used.
	Found []Found
	// Cache is confirm.json as LoadCache read it, re-verified.
	Cache *File
	// Stem is the stem a decision prepared now would get (brd.approve or
	// brd.ack): its packet hashed as prepare hashes it. ok is false when it
	// cannot be computed.
	Stem func(b schema.BRD, action string) (stem string, ok bool)
}

// ListUnsigned lists the decisions that could be confirmed by signature: a
// BRD's current version approved or acknowledged by a typed note (a local
// command, or a file written before the local mark existed) and not
// confirmed since; a council's approval its delegating human has not
// acknowledged; and each review-route record of a current version that no
// signature confirms: confirmed, or approved and not accepted because
// confirm.routes leaves the review route out (ROUTE_NOT_ACCEPTED). For each
// it gives the exact commands.
// It reads only; it signs, prepares and writes nothing.
// @enforces confirm:INV-19
func ListUnsigned(in UnsignedInput) UnsignedList {
	out := UnsignedList{Contract: UnsignedSchema, Decisions: []Unsigned{}}
	var recs []Record
	switch {
	case in.Cache == nil:
		out.Note = "no confirm data: decisions confirmed by a review are not listed until `lattice confirm` has run"
	case in.Cache.SetAside() != "":
		out.Note = "confirm.json is set aside (" + in.Cache.SetAside() + "): decisions confirmed by a review are not listed"
	default:
		recs = in.Cache.Records
	}
	current := map[string]int{}
	for _, b := range in.BRDs {
		current[b.ID] = b.Version
	}
	key := func(id string, v int, action string) string { return id + "|" + strconv.Itoa(v) + "|" + action }
	signed, human := map[string]bool{}, map[string]bool{}
	for _, r := range recs {
		if r.State != StateConfirmed && !reviewNotAccepted(r) {
			continue
		}
		if r.Action == ActionApprove || r.Action == ActionAck {
			human[r.ID+"|"+strconv.Itoa(r.Version)] = true
		}
		if r.Via == ViaSignature && r.State == StateConfirmed {
			signed[key(r.ID, r.Version, r.Action)] = true
		}
	}
	paths := map[string]string{}
	for _, f := range in.Found {
		if f.OnDefault {
			paths[key(f.Decision.ID, f.Decision.Version, f.Decision.Action)+"|"+f.AddedSHA] = f.Path
		}
	}

	for _, r := range recs {
		if (r.State != StateConfirmed && !reviewNotAccepted(r)) || r.Via == ViaSignature || signed[key(r.ID, r.Version, r.Action)] {
			continue
		}
		if v, ok := current[r.ID]; (!ok || v != r.Version) && r.Action != ActionWaiverAdd {
			continue
		}
		p := paths[key(r.ID, r.Version, r.Action)+"|"+r.DecisionSHA]
		if p == "" {
			continue
		}
		u := Unsigned{ID: r.ID, Version: r.Version, Action: r.Action, Rests: RestsReview, By: r.ApproverLogin, PR: r.PR, DecisionPath: p,
			Commands: []string{"lattice sign " + p}}
		u.Text = r.Method() + "; sign the decision file, then commit " + strings.TrimSuffix(p, ".yaml") + ".sig and merge it"
		if reviewNotAccepted(r) {
			u.Text = r.Method() + ", which confirm.routes does not accept; sign the decision file, then commit " +
				strings.TrimSuffix(p, ".yaml") + ".sig and merge it"
		}
		out.Decisions = append(out.Decisions, u)
	}

	for _, b := range in.BRDs {
		a := b.Approval
		// Any approval written in the BRD file that no confirmed record backs
		// is a typed note, whether or not it carries the local mark: files
		// approved or acknowledged before that mark existed have none.
		if a == nil || b.Status != schema.BRDApproved || (a.ApprovedVersion > 0 && a.ApprovedVersion != b.Version) ||
			human[b.ID+"|"+strconv.Itoa(b.Version)] {
			continue
		}
		council := a.Mandate != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.ApprovedBy)), "council")
		action, by, rests := ActionApprove, a.ApprovedBy, RestsLocal
		prep := "lattice prepare brd approve " + b.ID + " --version " + strconv.Itoa(b.Version)
		text := "a local note typed as " + by + ", not a confirmation; prepare the decision, sign it, then commit the .sig on the decision branch and merge it"
		switch {
		case a.HumanReview == schema.HumanReviewAcknowledged:
			action, by, prep = ActionAck, a.ReviewedBy, "lattice prepare brd ack "+b.ID
			text = "a local note typed as " + by + ", not a confirmation; prepare the decision, sign it, then commit the .sig on the decision branch and merge it"
		case council:
			// A council approved it and its delegating human has not acked
			// it: the ack is theirs to make, after reading the decision.
			action, by, rests, prep = ActionAck, a.DelegatedBy, RestsCouncil, "lattice prepare brd ack "+b.ID
			text = "approved by a council, not yet acknowledged by its delegating human; read the council's decision, then prepare the acknowledgement, sign it, commit the .sig on the decision branch and merge it"
		case schema.IsAgentActor(a.ApprovedBy):
			continue // an agent's approval is never a human's to re-sign
		}
		u := Unsigned{ID: b.ID, Version: b.Version, Action: action, Rests: rests, By: by}
		if in.Stem != nil {
			if stem, ok := in.Stem(b, action); ok {
				u.Commands = []string{prep, "git switch decision/" + stem, "lattice sign " + stem}
			}
		}
		if u.Commands == nil {
			u.Commands = []string{prep, "git switch <the branch prepare prints>", "lattice sign <that branch>"}
		}
		u.Text = text
		out.Decisions = append(out.Decisions, u)
	}
	sort.SliceStable(out.Decisions, func(i, j int) bool {
		x, y := out.Decisions[i], out.Decisions[j]
		if x.ID != y.ID {
			return x.ID < y.ID
		}
		if x.Version != y.Version {
			return x.Version < y.Version
		}
		return x.Action < y.Action
	})
	return out
}

// Text renders the list, one block per decision.
func (l UnsignedList) Text() string {
	var b strings.Builder
	for _, u := range l.Decisions {
		b.WriteString(u.Rests + "  " + u.Action + " " + u.ID + " v" + itoa(u.Version) + "  (" + u.Text + ")\n")
		for _, c := range u.Commands {
			b.WriteString("    " + c + "\n")
		}
	}
	if len(l.Decisions) == 0 {
		b.WriteString("no decision awaits a signature\n")
	}
	if l.Note != "" {
		b.WriteString("note: " + l.Note + "\n")
	}
	return b.String()
}

// reviewNotAccepted reports a review approval that confirm.routes does not
// accept as a human confirmation: a signature would confirm it.
func reviewNotAccepted(r Record) bool {
	return r.State == StatePending && r.Assurance.Code == CodeRouteNotAccepted && r.route() == ViaReview && r.ApproverLogin != ""
}
