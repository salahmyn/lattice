package store

import (
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// words splits text into lower-case runs of letters and digits. Both backends
// search these, so they return the same blocks.
func words(s string) []string {
	return strings.Fields(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

func limitOf(f Filter) int {
	if f.Limit > 0 {
		return f.Limit
	}
	return 50
}

func clampDepth(d int) int {
	switch {
	case d < 1:
		return 1
	case d > 10:
		return 10
	}
	return d
}

// searchBlocks returns the blocks where every term of text is a prefix of a
// word of the id, title or description, by id.
func searchBlocks(bs []schema.Block, text string, f Filter) []schema.Block {
	terms := words(text)
	if len(terms) == 0 {
		return nil
	}
	var out []schema.Block
	for _, b := range bs {
		if (f.Kind != "" && b.Kind != f.Kind) || (f.System != "" && b.System != f.System) || (f.Tier != "" && b.Tier != f.Tier) {
			continue
		}
		ws := words(b.ID + " " + b.Title + " " + b.Description)
		all := true
		for _, t := range terms {
			hit := false
			for _, w := range ws {
				if strings.HasPrefix(w, t) {
					hit = true
					break
				}
			}
			all = all && hit
		}
		if all {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if n := limitOf(f); len(out) > n {
		out = out[:n]
	}
	return out
}

// relations returns the edges touching every node reached from id in fewer
// than depth hops, following dir and only the given kinds (all when empty).
func relations(es []schema.BlockEdge, id string, dir Direction, kinds []string, depth int) []schema.BlockEdge {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	frontier, seen := map[string]bool{id: true}, map[string]bool{id: true}
	got := map[[3]string]schema.BlockEdge{}
	for d := 0; d < clampDepth(depth) && len(frontier) > 0; d++ {
		next := map[string]bool{}
		visit := func(e schema.BlockEdge, far string) {
			got[[3]string{e.From, e.Kind, e.To}] = e
			if !seen[far] {
				seen[far] = true
				next[far] = true
			}
		}
		for _, e := range es {
			if len(want) > 0 && !want[e.Kind] {
				continue
			}
			if dir != In && frontier[e.From] {
				visit(e, e.To)
			}
			if dir != Out && frontier[e.To] {
				visit(e, e.From)
			}
		}
		frontier = next
	}
	out := make([]schema.BlockEdge, 0, len(got))
	for _, e := range got {
		out = append(out, e)
	}
	sortEdges(out)
	return out
}

func sortEdges(es []schema.BlockEdge) {
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.To < b.To
	})
}
