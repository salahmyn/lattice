package validate

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkCouncil covers BRDs a council approved in council mode:
//
//	BRD_COUNCIL_INVALID     (error) — the approval is not backed by a pinned
//	                                  brd_approve mandate, crosses the
//	                                  human-only floor, or lacks a unanimous
//	                                  recorded vote (e.g. a hand-written block)
//	BRD_COUNCIL_UNREVIEWED  (info)  — the human the council acted for has not
//	                                  acknowledged the decision yet
func (c *corpus) checkCouncil() []schema.Violation {
	var v []schema.Violation
	for i := range c.kg.BRDs {
		b := c.kg.BRDs[i]
		if b.Status != schema.BRDApproved || b.Approval == nil || b.Approval.Mandate == "" {
			continue
		}
		loc := &schema.Location{File: b.SourcePath}
		if why := brd.RecheckApproval(b, c.cfg.Autonomy); len(why) > 0 {
			v = append(v, schema.Violation{
				Code: schema.CodeBRDCouncilInvalid, Severity: schema.SeverityError,
				Subject:  b.ID,
				Message:  fmt.Sprintf("BRD %q council approval does not hold: %s", b.ID, strings.Join(why, "; ")),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "edit_brd",
					Field:  "approval",
					Detail: "have a human approve it (`lattice brd approve`) or re-run the council (`lattice brd vote`, `lattice brd decide`)",
				},
			})
			continue
		}
		if b.Approval.HumanReview != schema.HumanReviewAcknowledged {
			v = append(v, schema.Violation{
				Code: schema.CodeBRDCouncilUnreviewed, Severity: schema.SeverityInfo,
				Subject:  b.ID,
				Message:  fmt.Sprintf("BRD %q was approved by the council for %s, who has not reviewed the decision yet", b.ID, b.Approval.DelegatedBy),
				Location: loc,
				NextAction: &schema.NextAction{
					Kind:   "review_decision",
					Detail: "read " + brd.SummaryPath("lattice", b.ID, b.Version) + ", then `lattice brd ack " + b.ID + " --by <you>` or `lattice brd overturn`",
				},
			})
		}
	}
	return v
}
