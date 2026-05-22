package graph

import (
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func shardSampleGraph() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		SchemaVersion: "1.0",
		Features: []schema.Manifest{
			{ID: "checkout", Version: 1, Status: schema.StatusProduction},
			{ID: "checkout.refund", Version: 1, Status: schema.StatusProduction},
			{ID: "wallet", Version: 1, Status: schema.StatusProduction},
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "a", Feature: "checkout.refund"},
			{FQN: "b", Feature: "wallet"},
			{FQN: "c", Feature: ""}, // unassigned
		},
		Tests:       []schema.GraphSymbol{},
		Modules:     []schema.GraphModule{},
		Initiatives: []schema.Initiative{{ID: "init-1", Type: "initiative"}},
		Tasks:       []schema.Task{},
		Violations:  []schema.Violation{},
	}
}

func TestShardRoundTripByFeatureGroup(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "lattice.json")
	shardDir := filepath.Join(dir, "graph")

	orig := shardSampleGraph()
	if err := WriteSharded(index, shardDir, orig, ShardOptions{Strategy: "by_feature_group"}); err != nil {
		t.Fatalf("WriteSharded: %v", err)
	}

	got, err := Load(index)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Features) != 3 {
		t.Errorf("features: got %d want 3", len(got.Features))
	}
	if len(got.Symbols) != 3 {
		t.Errorf("symbols: got %d want 3", len(got.Symbols))
	}
	if len(got.Initiatives) != 1 {
		t.Errorf("initiatives: got %d want 1", len(got.Initiatives))
	}

	// checkout and checkout.refund share the "checkout" top-level shard.
	feats := map[string]bool{}
	for _, f := range got.Features {
		feats[f.ID] = true
	}
	for _, want := range []string{"checkout", "checkout.refund", "wallet"} {
		if !feats[want] {
			t.Errorf("missing feature %q after round-trip", want)
		}
	}
}

func TestLoadUnshardedGraph(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "lattice.json")
	orig := shardSampleGraph()
	if err := Write(index, orig); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Load(index)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Features) != 3 {
		t.Errorf("features: got %d want 3", len(got.Features))
	}
}
