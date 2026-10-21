package blocks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Walk is one simulated journey from a flows/v1 file: the catalogue nodes it
// visits, in order, and why it stopped.
type Walk struct {
	ID    string            `json:"id"`
	Title string            `json:"title"`
	Stop  string            `json:"stop"`
	Facts map[string]string `json:"facts"`
	Steps []WalkStep        `json:"steps"`
}

// WalkStep is one visited node and the link the walk took to reach it.
type WalkStep struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	When   []string `json:"when"`
	Chosen bool     `json:"chosen"`
}

// ParseWalks reads a flows/v1 file: a JSON array of walks with unique ids.
func ParseWalks(data []byte) ([]Walk, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var ws []Walk
	if err := dec.Decode(&ws); err != nil {
		return nil, fmt.Errorf("flows/v1: %w", err)
	}
	if ws == nil {
		return nil, fmt.Errorf("flows/v1: want a JSON array of walks")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("flows/v1: unexpected data after the walks")
	}
	seen := map[string]bool{}
	for i, w := range ws {
		switch {
		case w.ID == "":
			return nil, fmt.Errorf("flows/v1: walk %d has no id", i)
		case seen[w.ID]:
			return nil, fmt.Errorf("flows/v1: duplicate walk id %q", w.ID)
		}
		seen[w.ID] = true
		for j, s := range w.Steps {
			if s.ID == "" {
				return nil, fmt.Errorf("flows/v1: walk %s step %d has no id", w.ID, j)
			}
		}
	}
	return ws, nil
}

var pathParam = regexp.MustCompile(`\{[^}]*\}`)

// requestKey normalizes a request so every client block sending it compares equal.
func requestKey(h *CatHTTP) string {
	return strings.ToUpper(h.Method) + " " + strings.TrimRight(pathParam.ReplaceAllString(h.Path, "{}"), "/")
}

// entryRank orders the blocks answering a request: the entry point first.
var entryRank = map[string]int{"route": 0, "controller": 1, "service": 2}

// walkFlows turns a source's walks into flows over block ids: each walk is cut
// at its first repeated block (a retry loop), and after each step that sends a
// request, the round trip to the block answering it is inserted.
func (g *graph) walkFlows(im imported, walks []Walk) []schema.Flow {
	if len(walks) == 0 {
		return nil
	}
	catID := map[string]string{} // block id -> catalogue id, for blocks below an area
	for id, key := range im.keys {
		if im.nodes[id].Parent != "" {
			catID[key] = id
		}
	}
	far := map[string]map[string]bool{} // request key -> blocks answering it
	for _, e := range g.edges {
		n, ok := im.nodes[catID[e.From]]
		if e.Kind != "api" || !ok || n.API == nil || g.blocks[e.To] == nil {
			continue
		}
		k := requestKey(n.API)
		if far[k] == nil {
			far[k] = map[string]bool{}
		}
		far[k][e.To] = true
	}
	rank := func(id string) int {
		if r, ok := entryRank[g.blocks[id].Kind]; ok {
			return r
		}
		return len(entryRank)
	}
	answers := map[string][]string{}
	for k, set := range far {
		ids := sortedKeys(set)
		sort.SliceStable(ids, func(a, b int) bool { return rank(ids[a]) < rank(ids[b]) })
		answers[k] = ids
	}

	var out []schema.Flow
	for _, w := range walks {
		f := schema.Flow{ID: w.ID, Title: w.Title, Facts: w.Facts, Steps: []schema.FlowMove{}}
		seen, prev, loop := map[string]bool{}, "", false
		var lost []string
		for _, s := range w.Steps {
			key, ok := im.keys[s.ID]
			if !ok || g.blocks[key] == nil {
				lost = append(lost, s.ID)
				continue
			}
			if seen[key] {
				f.Steps = append(f.Steps, schema.FlowMove{From: prev, To: key, Label: s.Label, When: s.When, Kind: "loop", Chosen: s.Chosen})
				loop = true
				break
			}
			seen[key] = true
			kind := "flow"
			if prev == "" {
				kind = "start"
			}
			f.Steps = append(f.Steps, schema.FlowMove{From: prev, To: key, Label: s.Label, When: s.When, Kind: kind, Chosen: s.Chosen})
			prev = key
			if n := im.nodes[s.ID]; n.Kind == "api" && n.API != nil {
				if hits := answers[requestKey(n.API)]; len(hits) > 0 {
					f.Steps = append(f.Steps,
						schema.FlowMove{From: key, To: hits[0], Label: n.API.Method + " " + n.API.Path, Kind: "api"},
						schema.FlowMove{From: hits[0], To: key, Label: "response", Kind: "reply"})
				}
			}
		}
		switch {
		case loop:
			f.End = "loops back"
		case w.Stop == "end":
			f.End = "ends"
		default:
			f.End = w.Stop
		}
		if len(lost) > 0 { // usually walks simulated at another commit than the catalogue
			g.warnf("flow %s: %d step(s) name no block and were left out (first: %q)", w.ID, len(lost), lost[0])
		}
		out = append(out, f)
	}
	return out
}
