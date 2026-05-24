package brd

import (
	"context"
	"errors"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// fakeLLM returns a canned JSON body for FromCode tests, so we can
// verify the deterministic-shape contract (provenance, status,
// implements_via, dropped fabricated invariants) without depending on
// a real provider.
type fakeLLM struct {
	body string
	err  error
}

func (f fakeLLM) Complete(_ context.Context, _ LLMRequest) (LLMResponse, error) {
	if f.err != nil {
		return LLMResponse{}, f.err
	}
	return LLMResponse{Text: f.body}, nil
}

func TestFromCodeSetsDeterministicFields(t *testing.T) {
	feat := schema.Manifest{
		ID: "checkout.refund", Version: 1, Status: schema.StatusProduction,
		Purpose: "Self-service refunds.",
		Owners:  schema.Owners{Business: "payments", Engineering: "checkout-eng"},
		Invariants: []schema.Invariant{
			{ID: "INV-1", Statement: "refund never exceeds charge"},
		},
	}
	// Model returns the prose fields the prompt asked for, plus a
	// fabricated invariant reference we expect to be dropped.
	body := `{
	  "title": "Customer self-service refunds",
	  "business_problem": "CS handles 1.2k refund requests/day...",
	  "business_goals": ["Reduce CS tickets by 30%"],
	  "stakeholders": {
	    "business_owner": "", "product_owner": "checkout-pm",
	    "engineering_owner": "", "legal": "", "compliance": ""
	  },
	  "user_scenarios": [
	    {"id": "US-1", "actor": "Customer", "narrative": "A customer refunds an order."}
	  ],
	  "success_criteria": [
	    {"id": "SC-1", "statement": "Refunds never exceed the charge",
	     "maps_to_invariant": "checkout.refund:INV-1"},
	    {"id": "SC-2", "statement": "Refund processed in under 5min",
	     "maps_to_invariant": "checkout.refund:INV-FAKE"}
	  ]
	}`
	b, _, err := FromCode(context.Background(), feat, nil, FromCodeOptions{
		Provider: fakeLLM{body: body},
		Model:    "test-model-1",
	})
	if err != nil {
		t.Fatalf("FromCode: %v", err)
	}
	if b.ID != "brd.checkout.refund" {
		t.Errorf("BRD id = %q, want brd.checkout.refund", b.ID)
	}
	if b.Status != schema.BRDDraft {
		t.Errorf("status = %q, want draft", b.Status)
	}
	if b.Provenance.Source != schema.BRDSourceLLMFromCode {
		t.Errorf("provenance.source = %q, want llm_from_code", b.Provenance.Source)
	}
	if b.Provenance.Model != "test-model-1" {
		t.Errorf("provenance.model = %q, want test-model-1", b.Provenance.Model)
	}
	if !b.HumanReviewRequired {
		t.Error("human_review_required must be true on llm_from_code")
	}
	if len(b.ImplementsVia) != 1 || b.ImplementsVia[0] != "checkout.refund" {
		t.Errorf("implements_via = %v, want [checkout.refund]", b.ImplementsVia)
	}
	// Owners fallback: LLM left business_owner blank; we should fall
	// back to feature.Owners.Business.
	if b.Stakeholders.BusinessOwner != "payments" {
		t.Errorf("business_owner = %q, want fallback to feature.owners.business (payments)", b.Stakeholders.BusinessOwner)
	}
	if b.Stakeholders.EngineeringOwner != "checkout-eng" {
		t.Errorf("engineering_owner = %q, want fallback (checkout-eng)", b.Stakeholders.EngineeringOwner)
	}
	// Fabricated invariant reference must be dropped (kept the
	// statement, cleared the maps_to_invariant).
	if len(b.SuccessCriteria) != 2 {
		t.Fatalf("expected 2 criteria, got %d", len(b.SuccessCriteria))
	}
	if b.SuccessCriteria[0].MapsToInvariant != "checkout.refund:INV-1" {
		t.Errorf("real invariant ref was dropped: %+v", b.SuccessCriteria[0])
	}
	if b.SuccessCriteria[1].MapsToInvariant != "" {
		t.Errorf("fabricated invariant ref survived: %+v", b.SuccessCriteria[1])
	}
	// Constraints must always be empty — model was forbidden from
	// inventing them, and we never accept them either way.
	if len(b.Constraints) != 0 {
		t.Errorf("constraints must be empty, got %+v", b.Constraints)
	}
}

func TestFromCodeNoProviderError(t *testing.T) {
	_, _, err := FromCode(context.Background(), schema.Manifest{ID: "x"}, nil, FromCodeOptions{})
	if !errors.Is(err, ErrNoLLM) {
		t.Errorf("expected ErrNoLLM, got %v", err)
	}
}

func TestFromCodeProviderError(t *testing.T) {
	want := errors.New("provider exploded")
	_, _, err := FromCode(context.Background(), schema.Manifest{ID: "x"}, nil, FromCodeOptions{
		Provider: fakeLLM{err: want},
	})
	if !errors.Is(err, want) {
		t.Errorf("expected provider error to pass through, got %v", err)
	}
}

func TestFromCodeUnparseableJSON(t *testing.T) {
	_, raw, err := FromCode(context.Background(), schema.Manifest{ID: "x"}, nil, FromCodeOptions{
		Provider: fakeLLM{body: "not json at all"},
	})
	if err == nil {
		t.Error("expected error for non-JSON response")
	}
	if raw != "not json at all" {
		t.Errorf("raw response should be returned for debugging, got %q", raw)
	}
}
