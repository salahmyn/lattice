package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Journey is the aggregate-view shape: every entry point that
// touches any feature implementing the given BRD, plus the set of
// features in that BRD's surface. The CLI and UI render the same
// struct different ways — CLI as markdown, UI as a single mermaid
// graph.
type Journey struct {
	BRDID       string             `json:"brd_id"`
	BRDTitle    string             `json:"brd_title,omitempty"`
	Features    []string           `json:"features"`
	EntryPoints []schema.EntryPoint `json:"entry_points"`
	// Mermaid is the rendered diagram. Computed by RenderJourney so the
	// MCP / API surface gives an agent the same picture the UI shows.
	Mermaid string `json:"mermaid,omitempty"`
}

// BuildJourney aggregates every EP whose flow visits any feature that
// implements the given BRD. Returns nil when the BRD doesn't exist;
// returns an empty-but-non-nil Journey when the BRD has no features
// or no EPs reach them yet — the UI surfaces that as "no journey yet".
func BuildJourney(kg schema.KnowledgeGraph, brdID string) *Journey {
	var brd *schema.BRD
	for i := range kg.BRDs {
		if kg.BRDs[i].ID == brdID {
			brd = &kg.BRDs[i]
			break
		}
	}
	if brd == nil {
		return nil
	}

	// The "features in this BRD" set: union of brd.implements_via and
	// any feature carrying implements_brd: <this>. Same union the
	// graph builder uses, so the journey reads as it does on /brds/{id}.
	features := map[string]bool{}
	for _, f := range brd.ImplementsVia {
		features[f] = true
	}
	for _, f := range kg.Features {
		if f.ImplementsBRD == brdID {
			features[f.ID] = true
		}
	}

	j := &Journey{BRDID: brd.ID, BRDTitle: brd.Title}
	for f := range features {
		j.Features = append(j.Features, f)
	}
	sort.Strings(j.Features)

	// Aggregate EPs. An EP joins the journey if any flow step names a
	// feature in our set.
	for _, ep := range kg.EntryPoints {
		for _, step := range ep.Flow {
			if features[step.Feature] {
				j.EntryPoints = append(j.EntryPoints, ep)
				break
			}
		}
	}
	sort.Slice(j.EntryPoints, func(i, k int) bool { return j.EntryPoints[i].ID < j.EntryPoints[k].ID })

	j.Mermaid = renderJourneyMermaid(brd, features, j.EntryPoints)
	return j
}

// renderJourneyMermaid composes a single graph: trigger → handler →
// feature, for every EP that touches the BRD's surface. Features in
// the BRD set are styled distinctly so the operator sees the BRD's
// scope highlighted within the wider call graph.
func renderJourneyMermaid(brd *schema.BRD, features map[string]bool, eps []schema.EntryPoint) string {
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n")
	// BRD anchor node — lets the viewer trace every feature back to
	// the business intent that scoped this journey.
	brdNode := nodeID("B", brd.ID)
	b.WriteString(fmt.Sprintf("    %s((%s))\n", brdNode, mermaidEscape(brd.ID)))

	// Anchor each in-scope feature to the BRD node.
	for f := range features {
		fnode := nodeID("F", f)
		b.WriteString(fmt.Sprintf("    %s --> %s[\"%s\"]\n", brdNode, fnode, mermaidEscape(f)))
	}

	// Then thread each EP into the right feature node.
	seenEdge := map[string]bool{}
	for _, ep := range eps {
		trigNode := nodeID("T", ep.ID)
		b.WriteString(fmt.Sprintf("    %s{{\"%s\"}}\n", trigNode, mermaidEscape(triggerLabel(ep))))
		for _, step := range ep.Flow {
			if !features[step.Feature] {
				continue
			}
			fnode := nodeID("F", step.Feature)
			key := trigNode + "->" + fnode
			if seenEdge[key] {
				continue
			}
			seenEdge[key] = true
			b.WriteString(fmt.Sprintf("    %s --> %s\n", trigNode, fnode))
		}
	}
	b.WriteString("```\n")
	return b.String()
}
