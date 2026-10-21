package blocks

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestFeaturesBecomeBlocks(t *testing.T) {
	r := Build(Inputs{
		Features: []schema.Manifest{
			{ID: "refund", Purpose: "Refund orders.", ImplementsBRD: "brd.refunds", DependsOn: []string{"orders"},
				Invariants: []schema.Invariant{{ID: "INV-1", Statement: "Refund at most once."}}},
			{ID: "orders", Purpose: "Place orders."},
		},
		EntryPoints: []schema.EntryPoint{{ID: "post-refund", Kind: schema.EntryPointKindHTTP, Purpose: "Refund API",
			Trigger: schema.Trigger{Method: "POST", Path: "/refunds"}, Flow: []schema.FlowStep{{Feature: "refund", Capability: "issue"}}}},
	})
	b := blockMap(r)
	f := b["feat:refund"]
	if f.Kind != "composite" || f.BRD != "brd.refunds" || f.Description != "Refund orders." || f.Provenance.Kind != schema.ProvenanceHuman {
		t.Errorf("feature block = %+v", f)
	}
	if inv := b["inv:refund.INV-1"]; inv.Parent != "feat:refund" || inv.Kind != "invariant" || inv.Description != "Refund at most once." {
		t.Errorf("invariant block = %+v", inv)
	}
	ep := b["ep:post-refund"]
	if ep.Kind != "boundary-in" || ep.Title != "POST /refunds" || ep.Truth != schema.TruthWired || ep.Attrs["entry_kind"] != "http_route" {
		t.Errorf("entry point block = %+v", ep)
	}
	e := edgeSet(r)
	if l := e["feat:refund -uses-> feat:orders"]; len(l) != 1 || l[0] != "depends on" {
		t.Errorf("depends edge = %v (all %v)", l, e)
	}
	if l := e["ep:post-refund -flow-> feat:refund"]; len(l) != 1 || l[0] != "issue" {
		t.Errorf("flow edge = %v", l)
	}
}

func TestFeaturesWarnOnMissingTargets(t *testing.T) {
	r := Build(Inputs{
		Features:    []schema.Manifest{{ID: "refund", DependsOn: []string{"ordrs"}}},
		EntryPoints: []schema.EntryPoint{{ID: "cli", Kind: schema.EntryPointKindCLI, Trigger: schema.Trigger{Command: "refund"}, Flow: []schema.FlowStep{{Feature: "gone"}}}},
	})
	w := strings.Join(r.Warnings, "\n")
	for _, want := range []string{`depends_on "ordrs"`, `flow step "gone"`} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings missing %s: %v", want, r.Warnings)
		}
	}
	if blockMap(r)["ep:cli"].Title != "refund" {
		t.Errorf("cli trigger title = %q", blockMap(r)["ep:cli"].Title)
	}
}
