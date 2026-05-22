package views

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func TestRenderC4(t *testing.T) {
	ws := &workspace.Workspace{
		LatticeDir: "/proj/lattice",
		Mode:       workspace.ModeEmbedded,
		CodeRoots:  []workspace.CodeRoot{{Name: "default", Available: true}},
	}
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{
			{
				ID: "checkout.refund", DependsOn: []string{"wallet"},
				Implementations: []schema.Implementation{{Symbol: "x", Language: "python"}},
				Surface:         []schema.Surface{{Type: schema.SurfaceEventConsume, Name: "wallet.credited"}},
			},
			{
				ID:              "wallet",
				Implementations: []schema.Implementation{{Symbol: "y", Language: "typescript"}},
				Surface:         []schema.Surface{{Type: schema.SurfaceEventEmit, Name: "wallet.credited"}},
			},
		},
	}

	out := RenderC4(ws, kg)

	for _, want := range []string{
		"C4Container", "C4Component",
		"Component(cmp_checkout,", "Component(cmp_wallet,",
		`Rel(cmp_checkout, cmp_wallet, "depends on")`,
		`Rel(cmp_wallet, cmp_checkout, "wallet.credited")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("C4 output missing %q\n---\n%s", want, out)
		}
	}
}

func TestC4ContextWithActorsAndExternals(t *testing.T) {
	ws := &workspace.Workspace{
		LatticeDir: "/proj/lattice",
		CodeRoots:  []workspace.CodeRoot{{Name: "default", Available: true}},
	}
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{
			ID:              "checkout",
			Implementations: []schema.Implementation{{Symbol: "x", Language: "python"}},
		}},
	}
	ctx := schema.ArchitectureContext{
		System: "Shop",
		Actors: []schema.Actor{{ID: "customer", Name: "Customer", Description: "Buyer."}},
		ExternalSystems: []schema.ExternalSystem{
			{ID: "gw", Name: "Gateway", Description: "Cards.", UsedBy: []string{"checkout"}},
		},
	}
	m := buildC4Model(ws, kg, ctx)

	ctxDiagram := m.contextDiagram()
	for _, want := range []string{
		`Person(p_customer, "Customer"`,
		`System(sys, "Shop"`,
		`System_Ext(ext_gw, "Gateway"`,
		`Rel(p_customer, sys, "uses")`,
		`Rel(sys, ext_gw, "integrates with")`,
	} {
		if !strings.Contains(ctxDiagram, want) {
			t.Errorf("context diagram missing %q\n%s", want, ctxDiagram)
		}
	}

	dsl := m.structurizr()
	for _, want := range []string{
		`workspace "Shop"`,
		`p_customer = person "Customer"`,
		`sys = softwareSystem "Shop"`,
		`ext_gw = softwareSystem "Gateway"`,
		`systemContext sys "Context"`,
	} {
		if !strings.Contains(dsl, want) {
			t.Errorf("structurizr DSL missing %q\n%s", want, dsl)
		}
	}
}

func TestRenderC4ExternalEvent(t *testing.T) {
	ws := &workspace.Workspace{
		LatticeDir: "/proj/lattice",
		CodeRoots:  []workspace.CodeRoot{{Name: "default", Available: true}},
	}
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{
			ID:      "billing",
			Surface: []schema.Surface{{Type: schema.SurfaceEventConsume, Name: "payment.settled"}},
		}},
	}
	out := RenderC4(ws, kg)
	if !strings.Contains(out, "External: payment.settled") {
		t.Errorf("expected an external system for the unmatched consumed event\n%s", out)
	}
}
