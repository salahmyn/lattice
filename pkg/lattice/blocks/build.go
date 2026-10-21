package blocks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// SourceInput is one source's parsed catalogues and the stamp of what was read.
type SourceInput struct {
	Info       SourceInfo
	Catalogues []*Catalogue
	Stamp      schema.SourceStamp
}

// Inputs is everything one refresh builds the block graph from.
type Inputs struct {
	Sources     []SourceInput
	Judgments   []Judgment
	Features    []schema.Manifest
	EntryPoints []schema.EntryPoint
}

// Result is the block graph plus warnings worth showing a person.
type Result struct {
	Blocks   []schema.Block       `json:"blocks"`
	Edges    []schema.BlockEdge   `json:"edges"`
	Sources  []schema.SourceStamp `json:"sources"`
	Warnings []string             `json:"warnings"`
}

// Build computes the block graph. It is a pure function of its inputs: the same
// inputs give the same result in the same order.
func Build(in Inputs) Result {
	g := &graph{blocks: map[string]*schema.Block{}, edges: map[[3]string]*schema.BlockEdge{}}
	byType := map[string][]Judgment{}
	for _, j := range in.Judgments {
		byType[j.Type] = append(byType[j.Type], j)
	}
	for _, j := range byType["system"] {
		g.applyNodes(j, "system")
	}
	for _, j := range byType["external"] {
		g.applyNodes(j, "external")
	}

	keys, nodes, provs := map[string]string{}, map[string]CatNode{}, map[string]schema.Provenance{}
	var stamps []schema.SourceStamp
	for _, s := range in.Sources {
		if s.Info.System != "" && g.blocks[s.Info.System] == nil {
			g.warnf("source %s: system %q is not declared by a system judgment", s.Info.ID, s.Info.System)
		}
		im := importSource(s.Catalogues, s.Info)
		if s.Info.System != "" && g.blocks[s.Info.System] == nil {
			for _, b := range im.blocks {
				if b.Parent == s.Info.System {
					b.Parent = ""
				}
			}
		}
		for k, b := range im.blocks {
			g.blocks[k] = b
		}
		for _, id := range sortedKeys(im.nodes) {
			keys[id], nodes[id], provs[id] = im.keys[id], im.nodes[id], im.prov[id]
			for _, x := range im.nodes[id].Next {
				to, ok := im.keys[x.To]
				if !ok {
					g.warnf("%s: next %q is not a block in source %s", id, x.To, s.Info.ID)
					continue
				}
				label := x.Label
				if x.Loop {
					label = strings.TrimSpace("↺ " + label)
				}
				g.edge(im.keys[id], to, "flow", label, x.When, im.prov[id])
			}
		}
		for _, c := range im.calls {
			g.edge(im.keys[c[0]], im.keys[c[1]], "call", "calls", nil, im.prov[c[1]])
		}
		stamps = append(stamps, s.Stamp)
	}

	// a server block that fulfills a client block is the far end of that client's request
	missing := 0
	for _, id := range sortedKeys(nodes) {
		for _, t := range nodes[id].Fulfills {
			from, ok := keys[t]
			if !ok {
				missing++
				continue
			}
			label := g.blocks[keys[id]].Attrs["http"]
			if label == "" {
				label = g.blocks[from].Attrs["api"]
			}
			g.edge(from, keys[id], "api", label, nil, provs[id])
		}
	}
	if missing > 0 {
		g.warnf("%d fulfills references name no block", missing)
	}

	g.addFeatures(in.Features, in.EntryPoints)
	for _, j := range byType["group"] {
		g.applyGroup(j)
	}
	for _, j := range byType["edge"] {
		g.applyEdge(j)
	}
	for _, j := range byType["label"] {
		g.applyLabel(j)
	}
	return g.result(stamps)
}

type graph struct {
	blocks map[string]*schema.Block
	edges  map[[3]string]*schema.BlockEdge
	warns  []string
}

func (g *graph) warnf(format string, args ...any) {
	g.warns = append(g.warns, fmt.Sprintf(format, args...))
}

// edge adds or merges a typed edge; endpoints are checked when the result is assembled.
func (g *graph) edge(from, to, kind, label string, when []string, prov schema.Provenance) {
	if from == "" || to == "" || from == to {
		return
	}
	k := [3]string{from, to, kind}
	e := g.edges[k]
	if e == nil {
		e = &schema.BlockEdge{From: from, To: to, Kind: kind, Provenance: prov}
		g.edges[k] = e
	}
	if label != "" && !contains(e.Labels, label) {
		e.Labels = append(e.Labels, label)
	}
	for _, w := range when {
		if !contains(e.When, w) {
			e.When = append(e.When, w)
		}
	}
}

func (g *graph) isAncestor(a, b string) bool {
	seen := map[string]bool{}
	for p := g.blocks[b].Parent; p != "" && !seen[p]; {
		if p == a {
			return true
		}
		seen[p] = true
		pb := g.blocks[p]
		if pb == nil {
			return false
		}
		p = pb.Parent
	}
	return false
}

func (g *graph) result(stamps []schema.SourceStamp) Result {
	r := Result{Blocks: []schema.Block{}, Edges: []schema.BlockEdge{}}
	for _, k := range sortedKeys(g.blocks) {
		r.Blocks = append(r.Blocks, *g.blocks[k])
	}
	for _, e := range g.edges {
		if g.blocks[e.From] == nil || g.blocks[e.To] == nil || g.isAncestor(e.From, e.To) || g.isAncestor(e.To, e.From) {
			continue
		}
		sort.Strings(e.Labels)
		sort.Strings(e.When)
		r.Edges = append(r.Edges, *e)
	}
	sort.Slice(r.Edges, func(i, j int) bool {
		a, b := r.Edges[i], r.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].ID < stamps[j].ID })
	r.Sources = stamps
	sort.Strings(g.warns)
	r.Warnings = g.warns
	return r
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// stubs replaced in Tasks 6 and 7
type Judgment struct {
	ID, Type string
	Entries  []JudgmentEntry
}
type JudgmentEntry struct{ ID, Title string }

func (g *graph) applyNodes(j Judgment, defaultKind string) {
	for _, e := range j.Entries {
		g.blocks[e.ID] = &schema.Block{ID: e.ID, Kind: defaultKind, Title: e.Title, Truth: schema.TruthDeclared}
	}
}
func (g *graph) applyGroup(Judgment)                                {}
func (g *graph) applyEdge(Judgment)                                 {}
func (g *graph) applyLabel(Judgment)                                {}
func (g *graph) addFeatures([]schema.Manifest, []schema.EntryPoint) {}
