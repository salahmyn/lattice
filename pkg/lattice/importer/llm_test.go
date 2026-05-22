package importer

import (
	"context"
	"errors"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/agentic"
)

// fakeProvider returns a canned reply, or an error when reply is empty.
type fakeProvider struct{ reply string }

func (fakeProvider) Name() string { return "fake" }

func (p fakeProvider) Complete(context.Context, agentic.CompletionRequest) (agentic.CompletionResponse, error) {
	if p.reply == "" {
		return agentic.CompletionResponse{}, errors.New("llm unavailable")
	}
	return agentic.CompletionResponse{Text: p.reply}, nil
}

func TestExtractJSON(t *testing.T) {
	if got := extractJSON("noise {\"a\":1} trailing"); got != `{"a":1}` {
		t.Errorf("extractJSON = %q", got)
	}
}

func TestSanitizeFeatureID(t *testing.T) {
	if got := sanitizeFeatureID("Billing.Refund-Service", "src/x"); got != "billing.refund_service" {
		t.Errorf("got %q", got)
	}
	if got := sanitizeFeatureID("", "src/billing"); got != "billing" {
		t.Errorf("empty id should fall back to the package-derived id, got %q", got)
	}
}

func TestLabelWithLLMUsesTheReply(t *testing.T) {
	cf := Discover(sampleModules(), Options{MinCandidateSymbols: 3})
	reply := `{"id":"billing.core","purpose":"Handles billing.",` +
		`"capabilities":[{"id":"charge","summary":"Charge a card.","rules":["never double charge"]}]}`
	drafts := LabelWithLLM(context.Background(), cf, fakeProvider{reply: reply}, LLMLabelOptions{})
	if len(drafts) != 1 {
		t.Fatalf("want 1 draft, got %d", len(drafts))
	}
	m := drafts[0].Manifest
	if m.ID != "billing.core" || m.Purpose != "Handles billing." {
		t.Errorf("LLM label not applied: %+v", m)
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0].ID != "charge" {
		t.Errorf("capability not parsed: %+v", m.Capabilities)
	}
}

func TestLabelWithLLMFallsBackOnFailure(t *testing.T) {
	cf := Discover(sampleModules(), Options{MinCandidateSymbols: 3})
	drafts := LabelWithLLM(context.Background(), cf, fakeProvider{}, LLMLabelOptions{})
	if len(drafts) != 1 || drafts[0].Manifest.ID != "billing" {
		t.Fatalf("a failed LLM call must fall back to the deterministic label: %+v", drafts)
	}
}
