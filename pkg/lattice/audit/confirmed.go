package audit

// @module-feature audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Confirmed states.
const (
	ConfirmedHuman   = "human"   // a verified platform approval of this version, by a human other than the author
	ConfirmedPending = "pending" // prepared, not merged, not verified, or made by a local session
	ConfirmedAgent   = "agent"   // approved by a council or an agent; no human decision confirmed it
	ConfirmedDraft   = "draft"   // not approved, or returned or overturned
	ConfirmedDrifted = "drifted" // the BRD moved past the version confirmed, or the packet changed
	ConfirmedNone    = "none"    // no BRD criterion maps to the promise
	ConfirmedUnknown = "unknown" // an approval is claimed, and there is no confirm data that backs it
)

// confirmSchema is the contract of lattice/.cache/confirm.json. The audit
// reads that file as data, the way it reads breaks.json, and does not import
// the package that writes it.
const confirmSchema = "lattice.confirm/1"

// ConfirmRecord is one verdict from confirm.json: who the platform says
// decided what. Only this reaches the confirmed signal.
type ConfirmRecord struct {
	ID            string   `json:"id"`
	Version       int      `json:"version"`
	Action        string   `json:"action"`
	State         string   `json:"state"`
	ApproverLogin string   `json:"approver_login,omitempty"`
	PR            int      `json:"pr,omitempty"`
	Rows          []string `json:"rows,omitempty"`
}

// ConfirmData is lattice/.cache/confirm.json as the audit reads it.
type ConfirmData struct {
	Commit  string          `json:"commit"`
	Records []ConfirmRecord `json:"records"`
}

// LoadConfirm reads lattice/.cache/confirm.json. It returns nil when the file
// is absent, unreadable or another schema: no confirm data.
func LoadConfirm(latticeDir string) *ConfirmData {
	data, err := os.ReadFile(filepath.Join(latticeDir, ".cache", "confirm.json"))
	if err != nil {
		return nil
	}
	var f struct {
		Contract string `json:"contract"`
		ConfirmData
	}
	if json.Unmarshal(data, &f) != nil || f.Contract != confirmSchema {
		return nil
	}
	cd := f.ConfirmData
	return &cd
}

// criterionRef is a BRD criterion mapped to a promise, with its BRD.
type criterionRef struct {
	brd     schema.BRD
	id      string
	tier    int
	promise string
}

// criteriaByPromise indexes every BRD criterion by the invariants it maps to.
func criteriaByPromise(kg schema.KnowledgeGraph) map[string][]criterionRef {
	out := map[string][]criterionRef{}
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			for _, ref := range sc.MapsToInvariant {
				out[ref] = append(out[ref], criterionRef{brd: b, id: sc.ID, tier: sc.EffectiveTier(), promise: ref})
			}
		}
	}
	return out
}

// approvalState reads one BRD's standing for one promise. It is a fact about
// who the platform says confirmed which version, never about whether the
// intent is right. The BRD's own approval block is display only: it never
// makes a state human.
func approvalState(b schema.BRD, promise string, cd *ConfirmData) (state, detail string, approver string) {
	var human, stale, pending, voided *ConfirmRecord
	var olderHuman *ConfirmRecord
	if cd != nil {
		for i := range cd.Records {
			r := &cd.Records[i]
			if r.ID != b.ID {
				continue
			}
			covers := r.Action == "brd.approve" || r.Action == "brd.ack" || (r.Action == "promise.confirm" && hasString(r.Rows, promise))
			voids := r.Action == "brd.return" || r.Action == "brd.overturn"
			switch {
			case r.Version == b.Version && r.State == "confirmed" && covers:
				human = r
			case r.Version == b.Version && r.State == "confirmed" && voids:
				voided = r
			case r.Version == b.Version && r.State == "stale" && covers:
				stale = r
			case r.Version == b.Version && r.State == "pending" && covers:
				pending = r
			case r.Version < b.Version && r.State == "confirmed" && covers:
				olderHuman = r
			}
		}
	}
	v := strconv.Itoa(b.Version)
	switch {
	case voided != nil:
		return ConfirmedDraft, "v" + v + " was " + strings.TrimPrefix(voided.Action, "brd.") + "ed by " + voided.ApproverLogin + " (PR #" + strconv.Itoa(voided.PR) + ")", ""
	case human != nil:
		return ConfirmedHuman, "confirmed at v" + v + " by " + human.ApproverLogin + " (PR #" + strconv.Itoa(human.PR) + ")", human.ApproverLogin
	case stale != nil:
		return ConfirmedDrifted, "confirmed by " + stale.ApproverLogin + ", but the packet changed since: confirm again", ""
	case olderHuman != nil:
		return ConfirmedDrifted, "confirmed at version " + strconv.Itoa(olderHuman.Version) + ", now version " + v, ""
	case pending != nil:
		return ConfirmedPending, "prepared, not merged or not approved by a human on the merged head", ""
	}

	a := b.Approval
	if b.Status != schema.BRDApproved || a == nil || strings.TrimSpace(a.ApprovedBy) == "" {
		return ConfirmedDraft, "status " + statusName(b.Status) + ", no decision on record", ""
	}
	if a.ApprovedVersion > 0 && b.Version > a.ApprovedVersion {
		return ConfirmedDrifted, "approved at version " + strconv.Itoa(a.ApprovedVersion) + ", now version " + v, ""
	}
	council := a.Mandate != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.ApprovedBy)), "council")
	switch {
	case council || schema.IsAgentActor(a.ApprovedBy):
		return ConfirmedAgent, "reviewed by " + a.ApprovedBy + ", awaiting a human decision", ""
	case a.Local:
		return ConfirmedPending, "prepared by a local session, not confirmed: " + a.ApprovedBy + " is a typed name", ""
	}
	why := "no confirm data (run `lattice confirm` in CI)"
	if cd != nil {
		why = "no platform approval on record for it"
	}
	return ConfirmedUnknown, "the approval block names " + a.ApprovedBy + " (display only); " + why, ""
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func statusName(s schema.BRDStatus) string {
	if s == "" {
		return string(schema.BRDDraft)
	}
	return string(s)
}

// confirmedRank orders states worst first: a promise is only as confirmed as
// its least-confirmed BRD.
var confirmedRank = map[string]int{ConfirmedDraft: 0, ConfirmedDrifted: 1, ConfirmedUnknown: 2, ConfirmedPending: 3, ConfirmedAgent: 4, ConfirmedHuman: 5}

// confirmed folds the standing of the BRDs behind a promise's criteria. It
// reads confirm.json only, through cd; nil cd is "no confirm data".
// @enforces audit:INV-5
func confirmed(crit []criterionRef, cd *ConfirmData) (Signal, []Approval) {
	s := Signal{Honesty: Proxy}
	if len(crit) == 0 {
		s.Value, s.Detail = ConfirmedNone, "no BRD criterion maps to this promise"
		return s, nil
	}
	var out []Approval
	worst := ""
	for _, c := range crit {
		state, detail, approver := approvalState(c.brd, c.promise, cd)
		ap := Approval{BRD: c.brd.ID, Criterion: c.id, Tier: c.tier, State: state, Status: statusName(c.brd.Status),
			Version: c.brd.Version, Detail: detail, ApprovedBy: approver}
		if c.brd.Approval != nil {
			ap.ApprovedVersion = c.brd.Approval.ApprovedVersion
		}
		out = append(out, ap)
		if worst == "" || confirmedRank[state] < confirmedRank[worst] {
			worst = state
		}
	}
	s.Value = worst
	s.Detail = "a fact about the platform's record of who confirmed, not proof the intent is right"
	for _, a := range out {
		if a.State == worst {
			s.Detail = a.BRD + ": " + a.Detail + "; " + s.Detail
			break
		}
	}
	return s, out
}
