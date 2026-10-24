package blocks

// @module-feature blocks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Diff is what changed between two block graphs: added, removed and changed
// block ids, edges ("from -kind-> to") and flow ids, and where each source moved.
type Diff struct {
	BlocksAdded   []string `json:"blocks_added,omitempty"`
	BlocksRemoved []string `json:"blocks_removed,omitempty"`
	BlocksChanged []string `json:"blocks_changed,omitempty"`
	EdgesAdded    []string `json:"edges_added,omitempty"`
	EdgesRemoved  []string `json:"edges_removed,omitempty"`
	EdgesChanged  []string `json:"edges_changed,omitempty"`
	FlowsAdded    []string `json:"flows_added,omitempty"`
	FlowsRemoved  []string `json:"flows_removed,omitempty"`
	FlowsChanged  []string `json:"flows_changed,omitempty"`
	Sources       []string `json:"sources,omitempty"`
}

// DiffGraphs compares the block graph of old with that of cur.
func DiffGraphs(old, cur schema.KnowledgeGraph) Diff {
	var d Diff
	// provenance names the commit read, so it is left out: a new commit alone is not a change
	old.Blocks, old.BlockEdges = withoutProvenance(old.Blocks, old.BlockEdges)
	cur.Blocks, cur.BlockEdges = withoutProvenance(cur.Blocks, cur.BlockEdges)
	blockID := func(b schema.Block) string { return b.ID }
	d.BlocksAdded, d.BlocksRemoved, d.BlocksChanged = compare(index(old.Blocks, blockID), index(cur.Blocks, blockID))
	edgeKey := func(e schema.BlockEdge) string { return e.From + " -" + e.Kind + "-> " + e.To }
	d.EdgesAdded, d.EdgesRemoved, d.EdgesChanged = compare(index(old.BlockEdges, edgeKey), index(cur.BlockEdges, edgeKey))
	flowID := func(f schema.Flow) string { return f.ID }
	d.FlowsAdded, d.FlowsRemoved, d.FlowsChanged = compare(index(old.Flows, flowID), index(cur.Flows, flowID))
	prev := index(old.Sources, func(s schema.SourceStamp) string { return s.ID })
	for _, s := range cur.Sources {
		if s.Commit == "" {
			continue
		}
		line := s.ID + " @ " + short(s.Commit)
		if o, ok := prev[s.ID]; ok && o.Commit != "" && o.Commit != s.Commit {
			line = s.ID + " " + short(o.Commit) + " → " + short(s.Commit)
		}
		if st := freshnessText(s); st != "" {
			line += " (" + st + ")"
		}
		d.Sources = append(d.Sources, line)
	}
	return d
}

// Summary is the one-line changelog: where each source moved, then the counts.
func (d Diff) Summary() string {
	var parts []string
	for _, p := range []struct {
		name    string
		a, r, c []string
	}{{"blocks", d.BlocksAdded, d.BlocksRemoved, d.BlocksChanged},
		{"edges", d.EdgesAdded, d.EdgesRemoved, d.EdgesChanged},
		{"flows", d.FlowsAdded, d.FlowsRemoved, d.FlowsChanged}} {
		if len(p.a)+len(p.r)+len(p.c) > 0 {
			parts = append(parts, fmt.Sprintf("%s +%d -%d ~%d", p.name, len(p.a), len(p.r), len(p.c)))
		}
	}
	changes := "no changes"
	if len(parts) > 0 {
		changes = strings.Join(parts, ", ")
	}
	if len(d.Sources) == 0 {
		return changes
	}
	return strings.Join(d.Sources, ", ") + "; " + changes
}

func freshnessText(s schema.SourceStamp) string {
	switch s.State {
	case schema.FreshBehind:
		return fmt.Sprintf("behind %d", s.Behind)
	case schema.FreshAhead:
		return fmt.Sprintf("ahead %d", s.Ahead)
	}
	return s.State
}

func short(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func index[T any](xs []T, key func(T) string) map[string]T {
	m := make(map[string]T, len(xs))
	for _, x := range xs {
		m[key(x)] = x
	}
	return m
}

// compare returns the keys only in cur, only in old, and in both with a
// different JSON encoding.
func compare[T any](old, cur map[string]T) (added, removed, changed []string) {
	for _, k := range sortedKeys(cur) {
		o, ok := old[k]
		if !ok {
			added = append(added, k)
			continue
		}
		a, _ := json.Marshal(o)
		b, _ := json.Marshal(cur[k])
		if !bytes.Equal(a, b) {
			changed = append(changed, k)
		}
	}
	for _, k := range sortedKeys(old) {
		if _, ok := cur[k]; !ok {
			removed = append(removed, k)
		}
	}
	return added, removed, changed
}

func withoutProvenance(bs []schema.Block, es []schema.BlockEdge) ([]schema.Block, []schema.BlockEdge) {
	bs, es = append([]schema.Block(nil), bs...), append([]schema.BlockEdge(nil), es...)
	for i := range bs {
		bs[i].Provenance = schema.Provenance{}
	}
	for i := range es {
		es[i].Provenance = schema.Provenance{}
	}
	return bs, es
}
