package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Suggestion is one proposed annotation with a confidence and rationale.
type Suggestion struct {
	Annotation string   `json:"annotation"`
	Args       []string `json:"args"`
	Rationale  string   `json:"rationale"`
	Confidence float64  `json:"confidence"`
}

// AnnotationResult is the output of the annotation-suggestion capability.
type AnnotationResult struct {
	Mode        Mode         `json:"mode"`
	File        string       `json:"file"`
	Line        int          `json:"line"`
	Suggestions []Suggestion `json:"suggestions"`
	TokensUsed  int          `json:"tokens_used,omitempty"`
}

// SuggestAnnotation proposes annotations for the symbol at file:line. The
// deterministic pass examines the module's feature and the invariants its
// neighboring symbols enforce; the LLM pass (when configured) refines them.
func (c *Capabilities) SuggestAnnotation(ctx context.Context, file string, line int) (AnnotationResult, error) {
	kg, err := c.loadGraph(ctx)
	if err != nil {
		return AnnotationResult{}, err
	}
	det := deterministicAnnotations(kg, file, line)
	result := AnnotationResult{Mode: ModeDeterministic, File: file, Line: line, Suggestions: det}

	if !c.LLMEnabled() {
		return result, nil
	}

	prompt := fmt.Sprintf(`Given a symbol at %s:%d, suggest appropriate Lattice annotations.

Deterministic candidates already found:
%s

Output JSON: {"suggestions":[{"annotation":str,"args":[str],"rationale":str,"confidence":float}]}`,
		file, line, mustJSON(det))

	resp, err := c.provider.Complete(ctx, CompletionRequest{
		SystemPrompt: ToneContract(c.cfg.Agentic.Tone) +
			"You suggest Lattice code annotations. Reply with JSON only.",
		UserMessage: prompt,
		MaxTokens:   c.cfg.Agentic.LLM.MaxTokens,
	})
	if err != nil {
		return result, nil // deterministic fallback on any LLM failure
	}
	var parsed struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); err != nil || len(parsed.Suggestions) == 0 {
		return result, nil
	}
	result.Mode = ModeLLM
	result.Suggestions = topN(parsed.Suggestions, 3)
	result.TokensUsed = resp.TokensUsed
	return result, nil
}

// deterministicAnnotations builds candidate annotations from the file's
// module feature and the invariants its symbols already enforce.
func deterministicAnnotations(kg schema.KnowledgeGraph, file string, line int) []Suggestion {
	var moduleFeature string
	invFreq := map[string]int{}
	capFreq := map[string]int{}
	var nearest schema.GraphSymbol
	nearestDist := -1

	for _, mod := range kg.Modules {
		if mod.File != file {
			continue
		}
		moduleFeature = mod.Feature
	}
	for _, s := range append(kg.Symbols, kg.Tests...) {
		if s.File != file {
			continue
		}
		for _, inv := range s.EnforcesInvariants {
			invFreq[inv]++
		}
		for _, cap := range s.Capabilities {
			capFreq[cap]++
		}
		if d := abs(s.Line - line); nearestDist < 0 || d < nearestDist {
			nearestDist = d
			nearest = s
		}
	}

	var out []Suggestion
	feature := moduleFeature
	if nearest.Feature != "" {
		feature = nearest.Feature
	}
	if feature != "" {
		out = append(out, Suggestion{
			Annotation: "feature", Args: []string{feature}, Confidence: 0.6,
			Rationale: "feature of the surrounding module and neighboring symbols",
		})
	}
	for inv, n := range invFreq {
		out = append(out, Suggestion{
			Annotation: "enforces_invariant", Args: []string{inv},
			Confidence: 0.3 + 0.1*float64(n),
			Rationale:  fmt.Sprintf("%d neighboring symbol(s) enforce this invariant", n),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	return topN(out, 3)
}

func topN(s []Suggestion, n int) []Suggestion {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func mustJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
