package blocks

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// An edge a human record or a judgment states is declared. An edge derived
// from an entry point's flow is not stated by a person and is not the join of
// two extracted ends, so no rule applies: its truth is empty, which is never
// read as a pass.
// @verifies blocks:INV-5
func TestEdgeTruth(t *testing.T) {
	in := Inputs{
		Features: []schema.Manifest{{ID: "refund", DependsOn: []string{"orders"}}, {ID: "orders"}},
		EntryPoints: []schema.EntryPoint{{ID: "post-refund", Kind: schema.EntryPointKindHTTP,
			Trigger: schema.Trigger{Method: "POST", Path: "/refunds"}, Flow: []schema.FlowStep{{Feature: "refund"}}}},
		Judgments: []Judgment{{ID: "calls", Type: "edge", Entries: []JudgmentEntry{
			{From: "feat:refund", To: "feat:orders", Kind: "server_api", Label: "charge"}}}},
	}
	got := map[string]string{}
	for _, e := range Build(in).Edges {
		got[e.From+" -"+e.Kind+"-> "+e.To] = e.Truth
	}
	want := map[string]string{
		"feat:refund -uses-> feat:orders":       schema.EdgeTruthDeclared, // depends_on, a human record
		"feat:refund -server_api-> feat:orders": schema.EdgeTruthDeclared, // a judgment
		"ep:post-refund -flow-> feat:refund":    "",                       // derived from a flow: no rule applies
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s: truth %q (present %v), want %q", k, g, ok, w)
		}
	}
}
