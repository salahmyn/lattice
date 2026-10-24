package cli

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Draft and proposed BRDs are ranked as awaiting a human approval; approved
// and superseded ones are not, and the action never tells an agent to approve.
func TestApprovalActions(t *testing.T) {
	brds := []schema.BRD{
		{ID: "brd.b", Status: schema.BRDDraft, Provenance: schema.BRDProvenance{Source: schema.BRDSourceLLMFromCode}},
		{ID: "brd.a", Status: schema.BRDProposed},
		{ID: "brd.c", Status: schema.BRDApproved},
		{ID: "brd.d", Status: schema.BRDSuperseded},
	}
	got := approvalActions(brds)
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
}
