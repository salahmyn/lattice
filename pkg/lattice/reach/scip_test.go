package reach_test

import (
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
)

// When a SCIP index exists for a language it replaces the core's
// resolution there: its references give the edges, each credited to the
// innermost enclosing definition (bounded by enclosing_range), and the
// adapter's references for that language are not read.
// @verifies reachability:INV-2, scip:INV-1
func TestSCIPReplacesResolution(t *testing.T) {
	mods := parse(t, typescript.New(), map[string]string{
		// line 1: a, line 2: its body calls c(); line 4: b; line 6: c.
		"src/m.ts": "export function a() {\n  c();\n}\nexport function b() {\n}\nexport function c() {\n}\n",
	})
	def := func(line, end int32, sym string) *scippb.Occurrence {
		return &scippb.Occurrence{Range: []int32{line, 16, line, 17}, Symbol: sym,
			SymbolRoles: int32(scippb.SymbolRole_Definition), EnclosingRange: []int32{line, 0, end, 1}}
	}
	ref := func(line int32, sym string) *scippb.Occurrence {
		return &scippb.Occurrence{Range: []int32{line, 2, line, 3}, Symbol: sym}
	}
	// The index says b calls c (line 5, inside b) and nothing calls it from
	// a: the adapter's a -> c is not used.
	idx := &scippb.Index{Documents: []*scippb.Document{{RelativePath: "src/m.ts", Occurrences: []*scippb.Occurrence{
		def(0, 2, "ts . m/a()."),
		def(3, 4, "ts . m/b()."),
		ref(4, "ts . m/c()."),
		def(5, 6, "ts . m/c()."),
		ref(9, "ts . lib/external()."), // defined outside the corpus
	}}}}
	withSCIP := reach.Build(mods, reach.Options{SCIP: map[string]*scip.Corpus{"typescript": scip.NewCorpus(idx)}})
	if !hasEdge(withSCIP, "src.m.b", "src.m.c") || hasEdge(withSCIP, "src.m.a", "src.m.c") {
		t.Errorf("SCIP edges:%s", joined(edges(withSCIP, "scip", "call")))
	}
	if withSCIP.Stats.SCIPEdges != 1 || len(withSCIP.Stats.SCIP) != 1 {
		t.Errorf("stats = %+v", withSCIP.Stats)
	}
	without := reach.Build(mods, reach.Options{})
	if !hasEdge(without, "src.m.a", "src.m.c") {
		t.Errorf("adapter edges:%s", joined(edges(without)))
	}
}
