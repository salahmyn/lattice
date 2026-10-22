package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestCarryBlocksKeepsRefreshOutput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lattice.json")
	old := schema.KnowledgeGraph{
		Blocks:  []schema.Block{{ID: "a"}},
		Sources: []schema.SourceStamp{{ID: "s"}},
	}
	if err := Write(p, old); err != nil {
		t.Fatal(err)
	}
	var kg schema.KnowledgeGraph
	CarryBlocks(p, &kg)
	if len(kg.Blocks) != 1 || len(kg.Sources) != 1 {
		t.Fatalf("not carried: %+v", kg)
	}
	var none schema.KnowledgeGraph
	CarryBlocks(filepath.Join(t.TempDir(), "absent.json"), &none)
	os.WriteFile(p, []byte("not json"), 0o644)
	CarryBlocks(p, &none)
	if len(none.Blocks) != 0 {
		t.Fatal("absent/unparsable must not carry")
	}
}

func TestCarryBlocksKeepsFlows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lattice.json")
	if err := os.WriteFile(p, []byte(`{"flows":[{"id":"f","end":"ends","steps":[{"to":"a","kind":"start"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var kg schema.KnowledgeGraph
	CarryBlocks(p, &kg)
	if len(kg.Flows) != 1 || kg.Flows[0].ID != "f" {
		t.Errorf("flows = %+v", kg.Flows)
	}
}
