package cli

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Draft and proposed BRDs are ranked as awaiting a human approval; approved
// and superseded ones are not, and the action never tells an agent to approve.
// @verifies steering:INV-1
func TestApprovalActions(t *testing.T) {
	brds := []schema.BRD{
		{ID: "brd.b", Status: schema.BRDDraft, Provenance: schema.BRDProvenance{Source: schema.BRDSourceLLMFromCode}},
		{ID: "brd.a", Status: schema.BRDProposed},
		{ID: "brd.c", Status: schema.BRDApproved},
		{ID: "brd.d", Status: schema.BRDSuperseded},
	}
	got := approvalActions(brds, config.Autonomy{})
	if len(got) != 2 || got[0].Unit != "brd.a" || got[1].Unit != "brd.b" {
		t.Fatalf("approval actions = %+v, want brd.a then brd.b", got)
	}
	for _, a := range got {
		if a.Level != "awaiting-approval" || !strings.Contains(a.Action, "a human") || !strings.Contains(a.Action, "an agent never approves") {
			t.Errorf("action %+v", a)
		}
	}
	if !strings.HasPrefix(got[1].Action, "drafted from code by an LLM") {
		t.Errorf("llm_from_code BRD should say so: %q", got[1].Action)
	}

	agent := approvalActions([]schema.BRD{{ID: "brd.e", Status: schema.BRDDraft,
		Provenance: schema.BRDProvenance{Source: schema.BRDSourceHuman, AuthoredBy: "agent:drafter"}}}, config.Autonomy{})
	if len(agent) != 1 || !strings.HasPrefix(agent[0].Action, "written by agent:drafter: ") {
		t.Errorf("an agent-authored BRD should name its author: %+v", agent)
	}
}

// In council mode with a brd_approve mandate, next points at the council
// path, except for BRDs that stay human-only.
// @verifies steering:INV-2
func TestApprovalActionsCouncil(t *testing.T) {
	auto := config.Autonomy{DefaultMode: "council", Mandates: []config.Mandate{{ID: "m", Grants: config.GrantBRDApprove}}}
	brds := []schema.BRD{
		{ID: "brd.a", Status: schema.BRDProposed},
		{ID: "brd.b", Status: schema.BRDProposed, SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Tier: 3}}},
	}
	got := approvalActions(brds, auto)
	if len(got) != 2 || !strings.Contains(got[0].Action, "lattice brd vote brd.a") {
		t.Fatalf("brd.a should take the council path: %+v", got)
	}
	if !strings.HasPrefix(got[1].Action, "human-only (criterion SC-1 is tier 3") {
		t.Errorf("brd.b should stay human-only: %q", got[1].Action)
	}
}
