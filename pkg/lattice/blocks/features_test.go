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
				Owners:     schema.Owners{Business: "finance", Engineering: "payments-team"},
				Invariants: []schema.Invariant{{ID: "INV-1", Statement: "Refund at most once."}}},
			{ID: "orders", Purpose: "Place orders.", Owners: schema.Owners{Business: "sales"}},
			{ID: "audit", Purpose: "Audit log."},
		},
		EntryPoints: []schema.EntryPoint{{ID: "post-refund", Kind: schema.EntryPointKindHTTP, Purpose: "Refund API",
			Trigger: schema.Trigger{Method: "POST", Path: "/refunds"}, Flow: []schema.FlowStep{{Feature: "refund", Capability: "issue"}}}},
	})
	b := blockMap(r)
	f := b["feat:refund"]
	if f.Kind != "composite" || f.BRD != "brd.refunds" || f.Description != "Refund orders." || f.Provenance.Kind != schema.ProvenanceHuman {
		t.Errorf("feature block = %+v", f)
	}
	if b["feat:refund"].Attrs["owner"] != "payments-team" || b["feat:orders"].Attrs["owner"] != "sales" || b["feat:audit"].Attrs != nil {
		t.Errorf("owners: refund %v, orders %v, audit %v", b["feat:refund"].Attrs, b["feat:orders"].Attrs, b["feat:audit"].Attrs)
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

// Every feature and invariant becomes a block, and a dotted feature sits in
// the block of its id's prefix: the feature when one is declared, else a
// group block for the prefix. Ids stay what they were for flat features.
// @verifies blocks:INV-4
func TestDottedFeaturesNestUnderTheirParent(t *testing.T) {
	inv := func(ids ...string) []schema.Invariant {
		var out []schema.Invariant
		for _, id := range ids {
			out = append(out, schema.Invariant{ID: id, Statement: id})
		}
		return out
	}
	ms := []schema.Manifest{
		{ID: "orders"},
		{ID: "orders.refunds", Invariants: inv("INV-1", "INV-2"), SourcePath: "features/orders/refunds.yaml"},
		{ID: "orders.refunds.partial", Invariants: inv("INV-1")},
		{ID: "wallet.topup", Invariants: inv("INV-1")},
		{ID: "wallet.limits.daily", Invariants: inv("INV-1")},
		{ID: "fees", Invariants: inv("INV-1")},
	}
	b := blockMap(Build(Inputs{Features: ms}))
	for id, parent := range map[string]string{
		"feat:orders":                 "",
		"feat:fees":                   "",
		"feat:orders.refunds":         "feat:orders",
		"feat:orders.refunds.partial": "feat:orders.refunds",
		"feat:wallet.topup":           "feat-group:wallet",
		"feat:wallet.limits.daily":    "feat-group:wallet.limits",
		"feat-group:wallet.limits":    "feat-group:wallet",
		"feat-group:wallet":           "",
	} {
		got, ok := b[id]
		if !ok {
			t.Errorf("no block %s", id)
			continue
		}
		if got.Parent != parent {
			t.Errorf("%s parent = %q, want %q", id, got.Parent, parent)
		}
	}
	if g := b["feat-group:wallet"]; g.Kind != "group" || g.Title != "wallet" {
		t.Errorf("group block = %+v", g)
	}
	for _, m := range ms {
		for _, v := range m.Invariants {
			if got := b[InvariantBlockID(m.ID, v.ID)]; got.Parent != FeatureBlockID(m.ID) || got.Kind != "invariant" {
				t.Errorf("invariant %s:%s block = %+v", m.ID, v.ID, got)
			}
		}
	}
	if src := b["inv:orders.refunds.INV-1"].Provenance.Source; src != "features/orders/refunds.yaml" {
		t.Errorf("provenance = %q, want the manifest's path", src)
	}
	if n := len(b); n != 6+6+2 {
		t.Errorf("%d blocks, want 6 features, 6 invariants and 2 groups", n)
	}
}
