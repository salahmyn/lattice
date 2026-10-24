package reach

// @module-feature reachability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
)

// CacheFile is the resolved call graph, relative to the lattice/ directory.
// It is derived (like results.json), so it is not committed; lattice.json
// keeps only each symbol's class.
const CacheFile = ".cache/callgraph.json"

// cacheVersion is the callgraph.json format version.
const cacheVersion = 1

// Cache is the on-disk form of a classified call graph.
type Cache struct {
	Version    int            `json:"version"`
	Stats      Stats          `json:"stats"`
	Classes    map[string]int `json:"classes"`
	Roots      []Root         `json:"roots"`
	Edges      []Edge         `json:"edges"`
	Unresolved []Unresolved   `json:"unresolved,omitempty"`
}

// Save writes the result to lattice/.cache/callgraph.json.
func Save(latticeDir string, r *Result) error {
	if r == nil || r.Graph == nil {
		return nil
	}
	c := Cache{Version: cacheVersion, Stats: r.Graph.Stats, Classes: r.Classes,
		Roots: r.Roots, Edges: r.Graph.Edges, Unresolved: r.Graph.Unresolved}
	if c.Roots == nil {
		c.Roots = []Root{}
	}
	if c.Edges == nil {
		c.Edges = []Edge{}
	}
	data, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return err
	}
	p := filepath.Join(latticeDir, filepath.FromSlash(CacheFile))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// Input is everything Compute needs.
type Input struct {
	Modules     []ir.Module
	EntryPoints []schema.EntryPoint
	CodeRoots   []CodeRoot
	Declared    []config.CoverageRoot
	// SCIP maps a language to its index, when one exists.
	SCIP map[string]*scip.Corpus
}

// Compute builds, roots and classifies the call graph in one call.
func Compute(in Input) *Result {
	g := Build(in.Modules, Options{SCIP: in.SCIP})
	ri := RootInput{EntryPoints: in.EntryPoints, EntryFiles: EntryFiles(in.CodeRoots, in.Modules), Declared: in.Declared}
	roots := g.Roots(ri)
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].FQN < roots[j].FQN })
	return Classify(g, roots)
}
