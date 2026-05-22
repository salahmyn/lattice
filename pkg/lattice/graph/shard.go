package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ShardOptions controls how the knowledge graph is split into shard files.
type ShardOptions struct {
	// Strategy is "by_feature_group" (one shard per top-level feature id) or
	// "by_size" (fixed-size chunks of features).
	Strategy string
	// MaxFeaturesPerShard bounds a shard under the by_size strategy.
	MaxFeaturesPerShard int
}

// shardIndex is the lattice.json written when sharding is enabled: it holds
// the non-sharded parts of the graph plus pointers to the shard files.
type shardIndex struct {
	SchemaVersion       string              `json:"schema_version"`
	GeneratedAt         string              `json:"generated_at"`
	GeneratedFromCommit string              `json:"generated_from_commit"`
	Sharded             bool                `json:"sharded"`
	Strategy            string              `json:"shard_strategy"`
	Shards              []string            `json:"shards"`
	Initiatives         []schema.Initiative `json:"initiatives"`
	Tasks               []schema.Task       `json:"tasks"`
	CodeGraph           schema.CodeGraph    `json:"code_graph"`
	Violations          []schema.Violation  `json:"violations"`
	Review              bool                `json:"review,omitempty"`
}

// shardFile is one shard: a partial knowledge graph holding the feature-scoped
// slices for a group.
type shardFile struct {
	SchemaVersion    string                        `json:"schema_version"`
	Group            string                        `json:"group"`
	Features         []schema.Manifest             `json:"features"`
	Symbols          []schema.GraphSymbol          `json:"symbols"`
	Tests            []schema.GraphSymbol          `json:"tests"`
	Modules          []schema.GraphModule          `json:"modules"`
	StructuralChecks []schema.GraphStructuralCheck `json:"structural_checks"`
}

// topLevel returns the first dot-separated segment of a feature id.
func topLevel(featureID string) string {
	if featureID == "" {
		return "_unassigned"
	}
	if i := strings.Index(featureID, "."); i > 0 {
		return featureID[:i]
	}
	return featureID
}

// WriteSharded emits the knowledge graph as a shard index at indexPath plus
// one shard file per group under shardDir.
func WriteSharded(indexPath, shardDir string, kg schema.KnowledgeGraph, opts ShardOptions) error {
	if err := os.RemoveAll(shardDir); err != nil {
		return err
	}
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		return err
	}

	groups := assignGroups(kg, opts)
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	var shardRefs []string
	for _, name := range names {
		shard := groups[name]
		shard.SchemaVersion = kg.SchemaVersion
		shard.Group = name
		data, err := marshalJSON(shard)
		if err != nil {
			return err
		}
		rel := filepath.Join(filepath.Base(shardDir), name+".json")
		if err := os.WriteFile(filepath.Join(shardDir, name+".json"), data, 0o644); err != nil {
			return err
		}
		shardRefs = append(shardRefs, filepath.ToSlash(rel))
	}

	idx := shardIndex{
		SchemaVersion: kg.SchemaVersion, GeneratedAt: kg.GeneratedAt,
		GeneratedFromCommit: kg.GeneratedFromCommit, Sharded: true,
		Strategy: opts.Strategy, Shards: shardRefs,
		Initiatives: kg.Initiatives, Tasks: kg.Tasks,
		CodeGraph: kg.CodeGraph, Violations: kg.Violations, Review: kg.Review,
	}
	data, err := marshalJSON(idx)
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath, data, 0o644)
}

// assignGroups partitions the graph's feature-scoped data into named shards.
func assignGroups(kg schema.KnowledgeGraph, opts ShardOptions) map[string]*shardFile {
	// featureGroup maps a feature id to its shard name.
	featureGroup := map[string]string{}

	if opts.Strategy == "by_size" {
		size := opts.MaxFeaturesPerShard
		if size < 1 {
			size = 200
		}
		for i, f := range kg.Features {
			featureGroup[f.ID] = fmt.Sprintf("shard-%03d", i/size)
		}
	} else {
		for _, f := range kg.Features {
			featureGroup[f.ID] = topLevel(f.ID)
		}
	}

	groups := map[string]*shardFile{}
	get := func(name string) *shardFile {
		if g, ok := groups[name]; ok {
			return g
		}
		g := &shardFile{}
		groups[name] = g
		return g
	}
	groupOf := func(feature string) string {
		if g, ok := featureGroup[feature]; ok {
			return g
		}
		if opts.Strategy == "by_size" {
			return "shard-000"
		}
		return topLevel(feature)
	}

	for _, f := range kg.Features {
		g := get(featureGroup[f.ID])
		g.Features = append(g.Features, f)
	}
	for _, s := range kg.Symbols {
		g := get(groupOf(s.Feature))
		g.Symbols = append(g.Symbols, s)
	}
	for _, t := range kg.Tests {
		g := get(groupOf(t.Feature))
		g.Tests = append(g.Tests, t)
	}
	for _, m := range kg.Modules {
		g := get(groupOf(m.Feature))
		g.Modules = append(g.Modules, m)
	}
	for _, sc := range kg.StructuralChecks {
		g := get(groupOf(sc.Feature))
		g.StructuralChecks = append(g.StructuralChecks, sc)
	}
	return groups
}

// Load reads a knowledge graph from indexPath, transparently reassembling it
// from shard files when the graph was written sharded.
func Load(indexPath string) (schema.KnowledgeGraph, error) {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}

	var probe struct {
		Sharded bool `json:"sharded"`
	}
	_ = json.Unmarshal(data, &probe)

	if !probe.Sharded {
		var kg schema.KnowledgeGraph
		if err := json.Unmarshal(data, &kg); err != nil {
			return schema.KnowledgeGraph{}, err
		}
		return kg, nil
	}

	var idx shardIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return schema.KnowledgeGraph{}, err
	}
	kg := schema.KnowledgeGraph{
		SchemaVersion: idx.SchemaVersion, GeneratedAt: idx.GeneratedAt,
		GeneratedFromCommit: idx.GeneratedFromCommit,
		Initiatives:         idx.Initiatives, Tasks: idx.Tasks,
		CodeGraph: idx.CodeGraph, Violations: idx.Violations, Review: idx.Review,
		Features: []schema.Manifest{}, Symbols: []schema.GraphSymbol{},
		Tests: []schema.GraphSymbol{}, Modules: []schema.GraphModule{},
		StructuralChecks: []schema.GraphStructuralCheck{},
	}
	base := filepath.Dir(indexPath)
	for _, ref := range idx.Shards {
		sdata, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(ref)))
		if err != nil {
			return kg, err
		}
		var sf shardFile
		if err := json.Unmarshal(sdata, &sf); err != nil {
			return kg, err
		}
		kg.Features = append(kg.Features, sf.Features...)
		kg.Symbols = append(kg.Symbols, sf.Symbols...)
		kg.Tests = append(kg.Tests, sf.Tests...)
		kg.Modules = append(kg.Modules, sf.Modules...)
		kg.StructuralChecks = append(kg.StructuralChecks, sf.StructuralChecks...)
	}
	return kg, nil
}

// marshalJSON renders v as deterministic indented JSON.
func marshalJSON(v interface{}) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
