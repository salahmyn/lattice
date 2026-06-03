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
			kg, _, err := graphFor(io, cmd, false)
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
//
// v0.6 extends the corpus to BRDs and EntryPoints so the question
// "find features about refunds" no longer fails on no keyword
// overlap — business prose lives in BRDs, intent prose lives in EP
// purposes, and both are now in the same semantic-similarity space.
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
	// BRDs: the business-intent prose layer. Title + business_problem
	// fold into one entry so a single hit answers "what's this BRD
	// about?"; goals/scenarios get their own entries so an agent can
	// land on the exact criterion that matters.
	for _, b := range kg.BRDs {
		bodyParts := []string{b.Title, b.BusinessProblem}
		entries = append(entries, searchHit{
			Kind: "brd", Ref: b.ID, Text: strings.TrimSpace(strings.Join(bodyParts, " — ")),
		})
		for i, goal := range b.BusinessGoals {
			entries = append(entries, searchHit{
				Kind: "brd_goal",
				Ref:  b.ID + ":G-" + itoa(i+1),
				Text: goal,
			})
		}
		for _, sc := range b.SuccessCriteria {
			entries = append(entries, searchHit{
				Kind: "brd_criterion",
				Ref:  b.ID + ":" + sc.ID,
				Text: sc.Statement,
			})
		}
		for _, us := range b.UserScenarios {
			ref := b.ID + ":" + us.ID
			text := us.Narrative
			if us.Actor != "" {
				text = us.Actor + " — " + text
			}
			entries = append(entries, searchHit{
				Kind: "user_scenario", Ref: ref, Text: text,
			})
		}
	}
	// Entry points: purpose carries the LLM-labelled intent (v0.3.1).
	// EPs without a purpose still get an entry keyed on the trigger so
	// lexical search can find them by route/path.
	for _, ep := range kg.EntryPoints {
		text := ep.Purpose
		if text == "" {
			text = epTriggerText(ep)
		}
		entries = append(entries, searchHit{
			Kind: "entry_point", Ref: ep.ID, Text: text,
		})
	}
	return entries
}

// epTriggerText returns the trigger spec as searchable prose — used
// when an EP carries no LLM-labelled purpose yet.
func epTriggerText(ep schema.EntryPoint) string {
	switch ep.Kind {
	case schema.EntryPointKindHTTP:
		return ep.Trigger.Method + " " + ep.Trigger.Path
	case schema.EntryPointKindCLI:
		return "cli command " + ep.Trigger.Command
	case schema.EntryPointKindCron:
		return "scheduled " + ep.Trigger.Schedule
	case schema.EntryPointKindQueue:
		return "queue " + ep.Trigger.Queue
	case schema.EntryPointKindEventConsumer:
		return "event " + ep.Trigger.Event
	}
	return ep.ID
}

// itoa keeps the index stringification local — avoids pulling strconv
// just for the goal-numbering footnote.
func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	// Two digits is plenty for goal numbering; BRDs with 100+ goals
	// will get truncated ids, which is the lesser evil over an extra import.
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
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
