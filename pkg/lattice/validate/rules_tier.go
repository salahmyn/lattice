package validate

// @module-feature validation

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkTierUnset reports BRDs with criteria whose tier is not 1, 2 or 3:
//
//	CRITERION_TIER_UNSET (info, always on) — one finding per BRD, listing every
//	                                         such criterion and quoting any
//	                                         out-of-range value
//
// Every tier rule reads an unpinned tier as tier 1, so a council mandate
// with a low tier_max can approve a BRD no one ever tiered. The finding
// makes that visible; it suggests no tier, because choosing one is a human
// judgment. An owner who wants the council to refuse such BRDs sets
// require_pinned_tiers on the mandate.
// @enforces validation:INV-18
func (c *corpus) checkTierUnset() []schema.Violation {
	var v []schema.Violation
	for i := range c.kg.BRDs {
		b := c.kg.BRDs[i]
		unpinned := brd.UnpinnedTiers(b)
		if len(unpinned) == 0 {
			continue
		}
		items := make([]string, len(unpinned))
		for j, cr := range unpinned {
			items[j] = cr.ID
			if cr.Tier != 0 {
				items[j] = fmt.Sprintf("%s (tier: %d)", cr.ID, cr.Tier)
			}
		}
		approval := "the BRD holds no council approval"
		if b.Approval != nil && b.Approval.Mandate != "" {
			approval = "the BRD holds a council approval"
		}
		v = append(v, schema.Violation{
			Code: schema.CodeCriterionTierUnset, Severity: schema.SeverityInfo,
			Subject: b.ID,
			Message: fmt.Sprintf("BRD %q: %d of %d criteria have no pinned tier (%s); they are treated as tier 1; %s",
				b.ID, len(unpinned), len(b.SuccessCriteria), strings.Join(items, ", "), approval),
			Location: &schema.Location{File: b.SourcePath},
			NextAction: &schema.NextAction{
				Kind:   "edit_brd",
				Field:  "success_criteria[].tier",
				Detail: "a human decides each listed criterion's tier, or accepts the finding in the allowlist with a reason",
			},
		})
	}
	return v
}
