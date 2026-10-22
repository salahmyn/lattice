package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBlockWireNames(t *testing.T) {
	kg := KnowledgeGraph{
		Blocks: []Block{{ID: "fe:cart", Kind: "area", Title: "Cart", Truth: TruthDeclared,
			Provenance: Provenance{Kind: ProvenanceAdapter, Source: "shop@abc1234:cart.json"}}},
		BlockEdges: []BlockEdge{{From: "fe:a", To: "fe:b", Kind: "flow", Provenance: Provenance{Kind: ProvenanceAdapter}}},
		Sources:    []SourceStamp{{ID: "shop", Commit: "abc", CommitDate: "2026-10-21T00:00:00Z"}},
	}
	data, err := json.Marshal(kg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`"blocks":`, `"block_edges":`, `"sources":`, `"commit_date":`, `"provenance":{"kind":"adapter"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	empty, _ := json.Marshal(KnowledgeGraph{})
	for _, absent := range []string{"blocks", "block_edges", `"sources"`} {
		if strings.Contains(string(empty), absent) {
			t.Errorf("empty graph should omit %s: %s", absent, empty)
		}
	}
}
