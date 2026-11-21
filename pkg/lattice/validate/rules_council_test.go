package validate

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func councilCfg() config.Config {
	cfg := config.Default()
	cfg.Autonomy = config.Autonomy{
		DefaultMode: "council",
		Mandates: []config.Mandate{{
			ID: "m-council", Grants: config.GrantBRDApprove, TierMax: 2,
			DelegatedBy: "owner@example.com", Expires: "2027-01-01",
			Council: &config.Council{Seats: []string{"intent", "evidence", "risk"}},
		}},
	}
	return cfg
}

func councilApprovedKG(review schema.HumanReviewState) schema.KnowledgeGraph {
	votes := []schema.BRDCouncilVote{
		{Seat: "intent", Actor: "agent-a", Verdict: schema.VerdictApprove, Reason: "r"},
		{Seat: "evidence", Actor: "agent-b", Verdict: schema.VerdictApprove, Reason: "r"},
		{Seat: "risk", Actor: "agent-c", Verdict: schema.VerdictApprove, Reason: "r"},
	}
	return schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.x", Version: 1, Status: schema.BRDApproved, Title: "x", BusinessProblem: "p",
			ImplementsVia:   []string{"checkout"},
			SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "x"}},
			Approval: &schema.BRDApproval{
				ApprovedBy: "council:m-council", ApprovedAt: "2026-10-24T09:00:00Z", ApprovedVersion: 1,
				Mandate: "m-council", DelegatedBy: "owner@example.com",
				Decision:    &schema.BRDCouncilDecision{Outcome: "approved", Summary: "s", Votes: votes},
				HumanReview: review,
			},
		}},
		Features: []schema.Manifest{brdManifest("checkout")},
	}
}

func TestCouncilApprovalAwaitingReview(t *testing.T) {
	got := codes(Validate(councilApprovedKG(schema.HumanReviewPending), councilCfg(), Options{}))
	if !got[schema.CodeBRDCouncilUnreviewed] || got[schema.CodeBRDCouncilInvalid] {
		t.Errorf("want BRD_COUNCIL_UNREVIEWED only, got %v", got)
	}
	got = codes(Validate(councilApprovedKG(schema.HumanReviewAcknowledged), councilCfg(), Options{}))
	if got[schema.CodeBRDCouncilUnreviewed] || got[schema.CodeBRDCouncilInvalid] {
		t.Errorf("an acknowledged council approval raises nothing, got %v", got)
	}
}

// @verifies brds.council:INV-6
func TestCouncilApprovalInvalid(t *testing.T) {
	for name, edit := range map[string]func(*schema.KnowledgeGraph, *config.Config){
		"mandate not pinned": func(_ *schema.KnowledgeGraph, cfg *config.Config) { cfg.Autonomy.Mandates = nil },
		"tier 3 criterion": func(kg *schema.KnowledgeGraph, _ *config.Config) {
			kg.BRDs[0].SuccessCriteria[0].Tier = 3
		},
		"a changes vote": func(kg *schema.KnowledgeGraph, _ *config.Config) {
			kg.BRDs[0].Approval.Decision.Votes[1].Verdict = schema.VerdictChanges
		},
		"no decision": func(kg *schema.KnowledgeGraph, _ *config.Config) { kg.BRDs[0].Approval.Decision = nil },
		"approved after expiry": func(kg *schema.KnowledgeGraph, _ *config.Config) {
			kg.BRDs[0].Approval.ApprovedAt = "2027-02-01T00:00:00Z"
		},
		"other human": func(kg *schema.KnowledgeGraph, _ *config.Config) {
			kg.BRDs[0].Approval.DelegatedBy = "someone@else.com"
		},
	} {
		kg, cfg := councilApprovedKG(schema.HumanReviewAcknowledged), councilCfg()
		edit(&kg, &cfg)
		if !codes(Validate(kg, cfg, Options{}))[schema.CodeBRDCouncilInvalid] {
			t.Errorf("%s: want BRD_COUNCIL_INVALID", name)
		}
	}
}

// Turning council mode off later does not void the approvals it gave.
func TestCouncilApprovalSurvivesModeChange(t *testing.T) {
	cfg := councilCfg()
	cfg.Autonomy.DefaultMode = "gated"
	if codes(Validate(councilApprovedKG(schema.HumanReviewAcknowledged), cfg, Options{}))[schema.CodeBRDCouncilInvalid] {
		t.Error("a later mode change should not invalidate a council approval")
	}
}

// An approval of a BRD with an unpinned tier stays valid while the mandate
// leaves require_pinned_tiers unset; the owner setting it re-checks the
// approval and voids it.
// @verifies brds.council:INV-7
func TestCouncilApprovalUnpinnedTiers(t *testing.T) {
	kg, cfg := councilApprovedKG(schema.HumanReviewAcknowledged), councilCfg()
	if kg.BRDs[0].SuccessCriteria[0].TierPinned() {
		t.Fatal("fixture should have an unpinned tier")
	}
	if got := codes(Validate(kg, cfg, Options{})); got[schema.CodeBRDCouncilInvalid] {
		t.Errorf("field unset: an existing approval stays valid, got %v", got)
	}
	cfg.Autonomy.Mandates[0].RequirePinnedTiers = true
	if !codes(Validate(kg, cfg, Options{}))[schema.CodeBRDCouncilInvalid] {
		t.Error("field set: the approval of an unpinned BRD must stop holding")
	}
	kg.BRDs[0].SuccessCriteria[0].Tier = 1
	if codes(Validate(kg, cfg, Options{}))[schema.CodeBRDCouncilInvalid] {
		t.Error("field set: a fully pinned BRD still holds")
	}
}
