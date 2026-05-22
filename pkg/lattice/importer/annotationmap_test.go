package importer

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func TestApplyAnnotationMap(t *testing.T) {
	modules := []ir.Module{{
		File: "a.py", Language: "python",
		Symbols: []ir.Symbol{
			{FQN: "billing.charge"},
			{FQN: "billing.refund"},
			{FQN: "unrelated.thing"},
		},
	}}
	ApplyAnnotationMap(modules, AnnotationMap{Features: []AnnotationMapFeature{
		{ID: "billing", Symbols: []string{"billing.charge", "billing.refund"}},
	}})

	feat := func(s ir.Symbol) string {
		for _, a := range s.Annotations {
			if a.Kind == "feature" {
				return a.Args[0].(string)
			}
		}
		return ""
	}
	if feat(modules[0].Symbols[0]) != "billing" || feat(modules[0].Symbols[1]) != "billing" {
		t.Errorf("sidecar symbols should gain a feature annotation")
	}
	if feat(modules[0].Symbols[2]) != "" {
		t.Errorf("unmapped symbol must not be annotated")
	}
}

func TestComputeVerification(t *testing.T) {
	features := []schema.Manifest{
		{ID: "billing", Invariants: []schema.Invariant{{ID: "INV-1"}, {ID: "INV-2"}}},
		{ID: "wallet", Invariants: []schema.Invariant{{ID: "INV-1"}}},
	}
	violations := []schema.Violation{
		{Code: schema.CodeUnenforcedInvariant, FeatureID: "billing", InvariantID: "INV-1"},
		{Code: schema.CodeUnverifiedInvariant, FeatureID: "billing", InvariantID: "INV-1"}, // same invariant
		{Code: schema.CodeManifestSchema}, // unrelated, ignored
	}
	vc := ComputeVerification(features, violations)
	if vc.TotalInvariants != 3 {
		t.Errorf("total = %d, want 3", vc.TotalInvariants)
	}
	// One distinct invariant is flagged, so 2 of 3 are verified.
	if vc.VerifiedInvariants != 2 {
		t.Errorf("verified = %d, want 2", vc.VerifiedInvariants)
	}
}
