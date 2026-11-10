package audit

// @module-feature audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/stamp"
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
	// Stamp is what produced the file: commit, CI run, platform and digest.
	Stamp *stamp.Stamp `json:"stamp,omitempty"`
	// SetAside is the code the reader gave the file when it refused its
	// verdicts (CONFIRM_UNSTAMPED, CONFIRM_TAMPERED, CONFIRM_TRACKED, or
	// EVIDENCE_NOT_CI under --ci), with why in SetAsideText. A set-aside
	// file's records all read unknown.
	SetAside     string `json:"-"`
	SetAsideText string `json:"-"`
}

// Local reports whether the file was stamped by a run outside CI. Its human
// verdicts are shown as human (local, not CI).
func (cd *ConfirmData) Local() bool {
	return cd != nil && cd.Stamp != nil && cd.Stamp.Local()
}

// setAside returns a copy of cd whose records all read unknown, with code
// and text saying why.
func (cd *ConfirmData) setAside(code, text string) *ConfirmData {
	out := *cd
	out.Records = make([]ConfirmRecord, len(cd.Records))
	for i, r := range cd.Records {
		r.State = ConfirmedUnknown
		out.Records[i] = r
	}
	out.SetAside, out.SetAsideText = code, text
	return &out
}

// ForCI is the --ci rule for confirm.json, as for every other evidence file:
// only a file stamped by a CI run at the head commit counts. Any other file
// is set aside with EVIDENCE_NOT_CI, so its verdicts read unknown.
// @enforces confirm:INV-6
func (cd *ConfirmData) ForCI(head string) *ConfirmData {
	if cd == nil || cd.SetAside != "" {
		return cd
	}
	if code, text := stamp.ForCI(cd.Stamp, head); code != "" {
		return cd.setAside(code, text)
	}
	return cd
}

// LoadConfirm reads lattice/.cache/confirm.json. It returns nil when the file
// is absent, unreadable or another schema: no confirm data. A file with no
// stamp, a digest that does not match its records, or that git tracks is set
// aside (CONFIRM_UNSTAMPED, CONFIRM_TAMPERED, CONFIRM_TRACKED): it is
// returned so the reason can be shown, and none of its verdicts reads human.
// @enforces confirm:INV-6
func LoadConfirm(latticeDir string) *ConfirmData {
	p := filepath.Join(latticeDir, ".cache", "confirm.json")
	data, err := os.ReadFile(p)
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
	if res := stamp.Check(p, data); !res.OK() {
		return cd.setAside(res.Code, res.Text)
	}
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
	if cd != nil && cd.SetAside != "" {
		for _, r := range cd.Records {
			if r.ID == b.ID {
				return ConfirmedUnknown, "confirm.json set aside (" + cd.SetAside + "): " + cd.SetAsideText, ""
			}
		}
	}
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
		local := ""
		if cd.Local() {
			local = "human (local, not CI): "
		}
		return ConfirmedHuman, local + "confirmed at v" + v + " by " + human.ApproverLogin + " (PR #" + strconv.Itoa(human.PR) + ")", human.ApproverLogin
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
	switch {
	case cd != nil && cd.SetAside != "":
		why = "confirm.json set aside (" + cd.SetAside + ")"
	case cd != nil:
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

// BRDStanding is the standing of one BRD version as a whole, read from
// confirm.json the way a promise's confirmed signal is: a verified platform
// approval of the version (human), a confirmation of an earlier version or a
// changed packet (drifted), a council or agent approval awaiting a human
// (agent), a prepared decision not yet verified (pending), a typed approval
// with no platform record (unknown), or none (draft). A promise.confirm
// record does not confirm the BRD. detail says why, and approver is the
// platform login for a human standing.
func BRDStanding(b schema.BRD, cd *ConfirmData) (state, detail, approver string) {
	return approvalState(b, "", cd)
}
