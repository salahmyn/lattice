package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/analyze"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

type searchHit struct {
	Kind       string  `json:"kind"`
	Ref        string  `json:"ref"`
	Text       string  `json:"text"`
	Similarity float64 `json:"similarity,omitempty"`
}

func newSearchCommand(io *IO) *cobra.Command {
	var semantic bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search features, capabilities, and invariants",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			var hits []searchHit
			if semantic {
				hits = semanticSearch(kg, args[0])
			} else {
				hits = lexicalSearch(kg, args[0])
			}
			if io.JSON {
				return io.printJSON(hits)
			}
			if len(hits) == 0 {
				io.printf("no matches\n")
				return nil
			}
			for _, h := range hits {
				if semantic {
					io.printf("  [%.2f] %-10s %s — %s\n", h.Similarity, h.Kind, h.Ref, h.Text)
				} else {
					io.printf("  %-10s %s — %s\n", h.Kind, h.Ref, h.Text)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&semantic, "semantic", false, "rank by embedding similarity")
	return cmd
}

// corpusEntries flattens the searchable text of the knowledge graph.
func corpusEntries(kg schema.KnowledgeGraph) []searchHit {
	var entries []searchHit
	for _, m := range kg.Features {
		entries = append(entries, searchHit{Kind: "feature", Ref: m.ID, Text: m.Purpose})
		for _, c := range m.Capabilities {
			entries = append(entries, searchHit{Kind: "capability", Ref: m.ID + ":" + c.ID, Text: c.Summary})
		}
		for _, inv := range m.Invariants {
			entries = append(entries, searchHit{Kind: "invariant", Ref: m.ID + ":" + inv.ID, Text: inv.Statement})
		}
	}
	return entries
}

func lexicalSearch(kg schema.KnowledgeGraph, query string) []searchHit {
	q := strings.ToLower(query)
	var hits []searchHit
	for _, e := range corpusEntries(kg) {
		if strings.Contains(strings.ToLower(e.Ref), q) || strings.Contains(strings.ToLower(e.Text), q) {
			hits = append(hits, e)
		}
	}
	return hits
}

func semanticSearch(kg schema.KnowledgeGraph, query string) []searchHit {
	emb := analyze.NewLexicalEmbedder()
	qv := emb.Embed(query)
	var hits []searchHit
	for _, e := range corpusEntries(kg) {
		e.Similarity = analyze.Cosine(qv, emb.Embed(e.Ref+" "+e.Text))
		if e.Similarity > 0 {
			hits = append(hits, e)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Similarity > hits[j].Similarity })
	if len(hits) > 10 {
		hits = hits[:10]
	}
	return hits
}
