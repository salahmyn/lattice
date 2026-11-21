package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func tierFindings(kg schema.KnowledgeGraph) []schema.Violation {
	return byCode(Validate(kg, councilCfg(), Options{}), schema.CodeCriterionTierUnset)
}

// An absent tier and an out-of-range one are both unpinned; the finding
// lists each, quotes the out-of-range value and suggests no number.
// @verifies validation:INV-18
func TestCriterionTierUnset(t *testing.T) {
	kg := councilApprovedKG(schema.HumanReviewAcknowledged)
	kg.BRDs[0].SuccessCriteria = []schema.BRDCriterion{
		{ID: "SC-1", Statement: "a"},
		{ID: "SC-2", Statement: "b", Tier: 4},
		{ID: "SC-3", Statement: "c", Tier: -1},
	}
	got := tierFindings(kg)
	if len(got) != 1 {
		t.Fatalf("want one finding per BRD, got %+v", got)
	}
	f := got[0]
	for _, want := range []string{"3 of 3 criteria", "SC-1", "SC-2 (tier: 4)", "SC-3 (tier: -1)", "treated as tier 1", "holds a council approval"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q lacks %q", f.Message, want)
		}
	}
	if strings.Contains(f.Message, "SC-1 (") {
		t.Errorf("an absent tier is not quoted: %q", f.Message)
	}
	if f.Subject != "brd.x" || f.Severity != schema.SeverityInfo {
		t.Errorf("want info on brd.x, got %+v", f)
	}
	if d := f.NextAction.Detail; strings.ContainsAny(d, "123") {
		t.Errorf("next action must not suggest a number: %q", d)
	}
}

// A BRD with pinned and unpinned criteria lists only the unpinned ones; a
// fully pinned BRD raises nothing; a BRD with no approval says so.
// @verifies validation:INV-18
func TestCriterionTierUnsetMixedBRD(t *testing.T) {
	kg := councilApprovedKG(schema.HumanReviewAcknowledged)
	kg.BRDs[0].Approval = nil
	kg.BRDs[0].Status = schema.BRDProposed
	kg.BRDs[0].SuccessCriteria = []schema.BRDCriterion{
		{ID: "SC-1", Statement: "a", Tier: 2},
		{ID: "SC-2", Statement: "b"},
		{ID: "SC-3", Statement: "c", Tier: 1},
	}
	got := tierFindings(kg)
	if len(got) != 1 || !strings.Contains(got[0].Message, "1 of 3 criteria") ||
		!strings.Contains(got[0].Message, "(SC-2)") || !strings.Contains(got[0].Message, "holds no council approval") {
		t.Fatalf("want 1 of 3 (SC-2), no approval, got %+v", got)
	}

	kg.BRDs[0].SuccessCriteria[1].Tier = 3
	if got := tierFindings(kg); len(got) != 0 {
		t.Errorf("a fully pinned BRD raises nothing, got %+v", got)
	}
}

// The finding is info and always on, so a run that fails on warnings stays
// green with it open; an allowlist entry with a reason accepts it.
// @verifies validation:INV-18
func TestCriterionTierUnsetStaysGreen(t *testing.T) {
	kg := councilApprovedKG(schema.HumanReviewAcknowledged)
	got := tierFindings(kg)
	if len(got) != 1 {
		t.Fatalf("want the finding, got %+v", got)
	}
	if Fails(got[0], schema.SeverityWarning) {
		t.Error("fail_on warning must stay green with CRITERION_TIER_UNSET open")
	}
	if !Fails(got[0], schema.SeverityInfo) {
		t.Error("fail_on info does fail on it")
	}
	al := Allowlist{Entries: []AllowEntry{{Code: schema.CodeCriterionTierUnset, Subject: "brd.x", Reason: "tiers decided later"}}}
	open, accepted := al.Apply(got)
	if len(open) != 0 || len(accepted) != 1 {
		t.Errorf("allowlist: open %v accepted %v", open, accepted)
	}
}
