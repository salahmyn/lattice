package audit

// @module-feature audit

import (
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Confirmed states.
const (
	ConfirmedHuman   = "human"   // approved, approver not an agent actor, no drift
	ConfirmedAgent   = "agent"   // approved by an agent actor, or by a council not yet acked
	ConfirmedDraft   = "draft"   // not approved
	ConfirmedDrifted = "drifted" // the BRD moved past the version approved
	ConfirmedNone    = "none"    // no BRD criterion maps to the promise
)

// criterionRef is a BRD criterion mapped to a promise, with its BRD.
type criterionRef struct {
	brd  schema.BRD
	id   string
	tier int
}

// criteriaByPromise indexes every BRD criterion by the invariants it maps to.
func criteriaByPromise(kg schema.KnowledgeGraph) map[string][]criterionRef {
	out := map[string][]criterionRef{}
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			for _, ref := range sc.MapsToInvariant {
				out[ref] = append(out[ref], criterionRef{brd: b, id: sc.ID, tier: sc.EffectiveTier()})
			}
		}
	}
	return out
}

// approvalState reads one BRD's approval record. It is a fact about who
// signed which version, never about whether the intent is right.
func approvalState(b schema.BRD) (state, detail string) {
	a := b.Approval
	if b.Status != schema.BRDApproved || a == nil || strings.TrimSpace(a.ApprovedBy) == "" {
		return ConfirmedDraft, "status " + statusName(b.Status) + ", no approval on record"
	}
	if a.ApprovedVersion > 0 && b.Version > a.ApprovedVersion {
		return ConfirmedDrifted, "approved at version " + strconv.Itoa(a.ApprovedVersion) + ", now version " + strconv.Itoa(b.Version)
	}
	council := a.Mandate != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.ApprovedBy)), "council")
	if council || schema.IsAgentActor(a.ApprovedBy) {
		reviewer := strings.TrimSpace(a.ReviewedBy)
		if a.HumanReview == schema.HumanReviewAcknowledged && reviewer != "" && !schema.IsAgentActor(reviewer) {
			return ConfirmedHuman, "approved by " + a.ApprovedBy + ", acknowledged by " + reviewer
		}
		return ConfirmedAgent, "approved by " + a.ApprovedBy + ", not acknowledged by a human"
	}
	return ConfirmedHuman, "approved by " + a.ApprovedBy
}

func statusName(s schema.BRDStatus) string {
	if s == "" {
		return string(schema.BRDDraft)
	}
	return string(s)
}

// confirmedRank orders states worst first: a promise is only as confirmed as
// its least-confirmed BRD.
var confirmedRank = map[string]int{ConfirmedDraft: 0, ConfirmedDrifted: 1, ConfirmedAgent: 2, ConfirmedHuman: 3}

// confirmed folds the approval records behind a promise's criteria.
// @enforces audit:INV-5
func confirmed(crit []criterionRef) (Signal, []Approval) {
	s := Signal{Honesty: Proxy}
	if len(crit) == 0 {
		s.Value, s.Detail = ConfirmedNone, "no BRD criterion maps to this promise"
		return s, nil
	}
	var out []Approval
	worst := ""
	for _, c := range crit {
		state, detail := approvalState(c.brd)
		ap := Approval{BRD: c.brd.ID, Criterion: c.id, Tier: c.tier, State: state, Status: statusName(c.brd.Status),
			Version: c.brd.Version, Detail: detail}
		if c.brd.Approval != nil {
			ap.ApprovedBy, ap.ApprovedVersion = c.brd.Approval.ApprovedBy, c.brd.Approval.ApprovedVersion
		}
		out = append(out, ap)
		if worst == "" || confirmedRank[state] < confirmedRank[worst] {
			worst = state
		}
	}
	s.Value = worst
	s.Detail = "a fact about the approval record, not proof the intent is right"
	for _, a := range out {
		if a.State == worst {
			s.Detail = a.BRD + ": " + a.Detail + "; " + s.Detail
			break
		}
	}
	return s, out
}
