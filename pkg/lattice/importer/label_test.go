package importer

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestDeriveFeatureID(t *testing.T) {
	cases := map[string]string{
		"src/billing":         "billing",
		"src/checkout/refund": "checkout.refund",
		"app/Users":           "users",
		"tools":               "tools",
		".":                   "root",
		"src/Foo-Bar":         "foo_bar",
		"packages/api/v2":     "api.v2",
	}
	for in, want := range cases {
		if got := deriveFeatureID(in); got != want {
			t.Errorf("deriveFeatureID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLabelProducesValidSkeleton(t *testing.T) {
	cf := Discover(sampleModules(), Options{MinCandidateSymbols: 3})
	drafts := Label(cf)
	if len(drafts) != 1 {
		t.Fatalf("want 1 draft, got %d", len(drafts))
	}
	m := drafts[0].Manifest
	if m.ID != "billing" {
		t.Errorf("id = %q, want billing", m.ID)
	}
	if m.Status != schema.StatusProposal {
		t.Errorf("status = %q, want proposal", m.Status)
	}
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if len(m.Capabilities) != 1 {
		t.Errorf("want one capability skeleton, got %d", len(m.Capabilities))
	}
	if len(m.Invariants) != 0 {
		t.Errorf("deterministic labeler must draft no invariants, got %d", len(m.Invariants))
	}
}

func TestLabelDisambiguatesCollidingIDs(t *testing.T) {
	cf := CandidatesFile{Candidates: []Candidate{
		{ID: "cand_a", Package: "src/billing", Symbols: []string{"a"}},
		{ID: "cand_b", Package: "app/billing", Symbols: []string{"b"}},
	}}
	drafts := Label(cf)
	ids := map[string]bool{}
	for _, d := range drafts {
		if ids[d.Manifest.ID] {
			t.Errorf("duplicate feature id %q", d.Manifest.ID)
		}
		ids[d.Manifest.ID] = true
	}
	if !ids["billing"] || !ids["billing_2"] {
		t.Errorf("want billing and billing_2, got %v", ids)
	}
}
