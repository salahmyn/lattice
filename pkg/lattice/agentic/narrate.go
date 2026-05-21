package agentic

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// NarrativeResult is the output of the narrative-generation capability.
type NarrativeResult struct {
	Mode       Mode   `json:"mode"`
	Markdown   string `json:"markdown"`
	TokensUsed int    `json:"tokens_used,omitempty"`
}

// Narrate generates a business-readable narrative of the system. Without an
// LLM it emits a deterministic templated narrative from the same data.
func (c *Capabilities) Narrate(ctx context.Context, scope string) (NarrativeResult, error) {
	kg, err := c.loadGraph(ctx)
	if err != nil {
		return NarrativeResult{}, err
	}
	groups := groupTopLevel(kg.Features, scope)

	if !c.LLMEnabled() {
		return NarrativeResult{Mode: ModeDeterministic, Markdown: templatedNarrative(groups)}, nil
	}

	var b strings.Builder
	for _, g := range groups {
		prompt := fmt.Sprintf(`Generate a business-readable narrative of what this system does for customers.

Audience: board members and non-technical executives.
Style: concrete, factual, no jargon. Two short paragraphs maximum.

FEATURE GROUP: %s
%s

Output: markdown narrative only.`, g.name, g.details())

		resp, err := c.provider.Complete(ctx, CompletionRequest{
			SystemPrompt: "You write executive-facing product narratives.",
			UserMessage:  prompt,
			MaxTokens:    c.cfg.Agentic.LLM.MaxTokens,
		})
		if err != nil {
			return NarrativeResult{Mode: ModeDeterministic, Markdown: templatedNarrative(groups)}, nil
		}
		b.WriteString("## " + titleCase(g.name) + "\n\n")
		b.WriteString(strings.TrimSpace(resp.Text) + "\n\n")
	}
	return NarrativeResult{Mode: ModeLLM, Markdown: strings.TrimSpace(b.String())}, nil
}

// featureGroup is a top-level feature and its descendants.
type featureGroup struct {
	name     string
	features []schema.Manifest
}

func (g featureGroup) details() string {
	var b strings.Builder
	for _, f := range g.features {
		b.WriteString(fmt.Sprintf("- %s: %s\n", f.ID, f.Purpose))
		if f.Value != nil && f.Value.Customer != "" {
			b.WriteString("  customer value: " + f.Value.Customer + "\n")
		}
		for _, cap := range f.Capabilities {
			b.WriteString("  capability " + cap.ID + ": " + cap.Summary + "\n")
		}
	}
	return b.String()
}

// groupTopLevel groups features by their top-level id segment.
func groupTopLevel(features []schema.Manifest, scope string) []featureGroup {
	byTop := map[string][]schema.Manifest{}
	for _, f := range features {
		top := f.ID
		if i := strings.Index(f.ID, "."); i > 0 {
			top = f.ID[:i]
		}
		if scope != "" && scope != "repo" && top != scope && f.ID != scope {
			continue
		}
		byTop[top] = append(byTop[top], f)
	}
	var names []string
	for t := range byTop {
		names = append(names, t)
	}
	sort.Strings(names)
	var groups []featureGroup
	for _, n := range names {
		fs := byTop[n]
		sort.Slice(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
		groups = append(groups, featureGroup{name: n, features: fs})
	}
	return groups
}

// templatedNarrative renders a deterministic, mechanical narrative.
func templatedNarrative(groups []featureGroup) string {
	var b strings.Builder
	b.WriteString("# System Narrative\n\n")
	for _, g := range groups {
		b.WriteString(fmt.Sprintf("## %s\n\n", titleCase(g.name)))
		b.WriteString(fmt.Sprintf("The %s feature group contains %d feature(s).\n\n",
			titleCase(g.name), len(g.features)))
		for _, f := range g.features {
			b.WriteString(fmt.Sprintf("**%s** — %s", f.ID, strings.TrimSpace(f.Purpose)))
			if f.Value != nil && f.Value.Customer != "" {
				b.WriteString(" " + strings.TrimSpace(f.Value.Customer))
			}
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
