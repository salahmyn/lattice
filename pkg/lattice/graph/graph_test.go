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
				File: "src/refund.py", Line: 3, Exported: true,
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

func TestBuildSurfacesAndErrors(t *testing.T) {
	in := Input{
		Manifests: []schema.Manifest{{
			ID: "cart", Version: 1, Status: schema.StatusProduction,
			Surface: []schema.Surface{{Type: schema.SurfaceHTTP, Method: "GET", Path: "/carts/:id"}},
			Errors:  []schema.ErrorDecl{{Code: "not_found", Status: 404}},
		}},
		Modules: []ir.Module{{
			File: "src/index.ts", Language: "typescript",
			ModuleAnnotations: []ir.Annotation{
				{Kind: "module_feature", Args: []interface{}{"cart"}},
			},
			Surfaces: []ir.Surface{
				{Type: "http", Method: "GET", Path: "/carts/:id", Detected: true},
				{Type: "http", Method: "GET", Path: "/health", Detected: true},
			},
			Symbols: []ir.Symbol{{
				Name: "CartError", FQN: "src.index.CartError", Kind: ir.KindClass,
				File: "src/index.ts", Exported: true,
				Annotations: []ir.Annotation{
					{Kind: "error", Args: []interface{}{"not_found 404"}},
				},
			}},
		}},
	}
	kg := Build(in, Options{})

	if len(kg.Surfaces) != 2 {
		t.Fatalf("surfaces = %d, want 2", len(kg.Surfaces))
	}
	for _, s := range kg.Surfaces {
		switch s.Path {
		case "/carts/:id":
			if !s.Declared || !s.Implemented {
				t.Errorf("/carts/:id declared=%v implemented=%v, want both", s.Declared, s.Implemented)
			}
		case "/health":
			if s.Declared || !s.Implemented {
				t.Errorf("/health declared=%v implemented=%v, want implemented-only", s.Declared, s.Implemented)
			}
		}
	}

	if len(kg.Errors) != 1 {
		t.Fatalf("errors = %d, want 1", len(kg.Errors))
	}
	if e := kg.Errors[0]; !e.Declared || !e.Implemented || e.Status != 404 {
		t.Errorf("error %+v, want declared+implemented status 404", e)
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
