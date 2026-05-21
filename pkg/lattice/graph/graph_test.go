package graph

import (
	"bytes"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func sampleInput() Input {
	return Input{
		Manifests: []schema.Manifest{
			{ID: "checkout", Version: 1, Status: schema.StatusProduction},
			{ID: "checkout.refund", Version: 1, Status: schema.StatusProduction},
		},
		Modules: []ir.Module{{
			File: "src/refund.py", Language: "python",
			Symbols: []ir.Symbol{{
				Name: "validate", FQN: "src.refund.validate", Kind: ir.KindFunction,
				File: "src/refund.py", Line: 3,
				Annotations: []ir.Annotation{
					{Kind: "feature", Args: []interface{}{"checkout.refund"}},
					{Kind: "enforces_invariant", Args: []interface{}{"INV-1"}},
				},
			}},
		}},
	}
}

func TestBuildResolvesChildrenAndImplementations(t *testing.T) {
	kg := Build(sampleInput(), Options{})

	var refund schema.Manifest
	for _, m := range kg.Features {
		if m.ID == "checkout.refund" {
			refund = m
		}
		if m.ID == "checkout" && (len(m.Children) != 1 || m.Children[0] != "checkout.refund") {
			t.Errorf("checkout children = %v", m.Children)
		}
	}
	if len(refund.Implementations) != 1 {
		t.Fatalf("checkout.refund implementations = %d, want 1", len(refund.Implementations))
	}
	if len(kg.Symbols) != 1 || kg.Symbols[0].Feature != "checkout.refund" {
		t.Errorf("symbol feature not resolved: %+v", kg.Symbols)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	opts := Options{}
	a, err := Marshal(Build(sampleInput(), opts))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(Build(sampleInput(), opts))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("graph build is not deterministic")
	}
}
