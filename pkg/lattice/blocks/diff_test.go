package blocks

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestDiffGraphs(t *testing.T) {
	old := schema.KnowledgeGraph{
		Blocks:     []schema.Block{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Attrs: map[string]string{}}},
		BlockEdges: []schema.BlockEdge{{From: "a", To: "b", Kind: "flow"}},
		Sources:    []schema.SourceStamp{{ID: "web", Commit: "1111111aaaa"}, {ID: "api", Commit: "2222222bbbb"}},
		Flows:      []schema.Flow{{ID: "f", End: "ends"}},
	}
	cur := schema.KnowledgeGraph{
		Blocks:     []schema.Block{{ID: "a", Title: "A2"}, {ID: "c"}, {ID: "d"}},
		BlockEdges: []schema.BlockEdge{{From: "a", To: "b", Kind: "flow"}, {From: "a", To: "d", Kind: "api"}},
		Sources: []schema.SourceStamp{{ID: "api", Commit: "2222222bbbb", State: schema.FreshSnapshot},
			{ID: "web", Commit: "3333333cccc", State: schema.FreshAhead, Ahead: 2}},
		Flows: []schema.Flow{{ID: "f", End: "loops back"}},
	}
	d := DiffGraphs(old, cur)
	want := "api @ 2222222 (snapshot), web 1111111 → 3333333 (ahead 2); blocks +1 -1 ~1, edges +1 -0 ~0, flows +0 -0 ~1"
	if got := d.Summary(); got != want {
		t.Errorf("summary = %q\nwant      %q", got, want)
	}
	if len(d.BlocksAdded) != 1 || d.BlocksAdded[0] != "d" || d.BlocksRemoved[0] != "b" || d.BlocksChanged[0] != "a" {
		t.Errorf("diff = %+v", d)
	}
	moved := cur
	moved.Blocks = []schema.Block{{ID: "a", Title: "A2", Provenance: schema.Provenance{Source: "web@9999999"}}, {ID: "c"}, {ID: "d"}}
	if d := DiffGraphs(cur, moved); len(d.BlocksChanged) != 0 {
		t.Errorf("a provenance-only change counted: %v", d.BlocksChanged)
	}
	if got := DiffGraphs(cur, cur).Summary(); got != "api @ 2222222 (snapshot), web @ 3333333 (ahead 2); no changes" {
		t.Errorf("same graph = %q", got)
	}
}
