package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// SubFeature is one proposed child of a decomposed feature.
type SubFeature struct {
	ID           string   `json:"id"`
	Purpose      string   `json:"purpose"`
	Capabilities []string `json:"capabilities"`
	Invariants   []string `json:"invariants"`
}

// DecompositionResult is the output of the decomposition capability.
type DecompositionResult struct {
	Mode        Mode         `json:"mode"`
	Feature     string       `json:"feature"`
	Triggered   bool         `json:"triggered"`
	Reason      string       `json:"reason"`
	SubFeatures []SubFeature `json:"sub_features"`
	TokensUsed  int          `json:"tokens_used,omitempty"`
}

// RecommendDecomposition proposes a sub-feature breakdown for an over-large
// feature. Deterministic mode clusters by shared surfaces and capabilities.
func (c *Capabilities) RecommendDecomposition(ctx context.Context, featureID string) (DecompositionResult, error) {
	kg, err := c.loadGraph(ctx)
	if err != nil {
		return DecompositionResult{}, err
	}
	var target *schema.Manifest
	for i := range kg.Features {
		if kg.Features[i].ID == featureID {
			target = &kg.Features[i]
		}
	}
	if target == nil {
		return DecompositionResult{}, fmt.Errorf("feature %q not found", featureID)
	}

	d := c.cfg.Decomposition
	over := len(target.Invariants) > d.MaxInvariants ||
		len(target.Capabilities) > d.MaxCapabilities ||
		len(target.Surface) > d.MaxSurfaces
	result := DecompositionResult{Feature: featureID, Triggered: over}
	if over {
		result.Reason = fmt.Sprintf("%d invariants, %d capabilities, %d surfaces",
			len(target.Invariants), len(target.Capabilities), len(target.Surface))
	} else {
		result.Reason = "feature is within complexity thresholds"
	}

	clusters := clusterFeature(*target)
	result.Mode = ModeDeterministic
	result.SubFeatures = clusters

	if !c.LLMEnabled() {
		return result, nil
	}

	prompt := fmt.Sprintf(`This Lattice manifest has grown large:

%s

Deterministic clustering suggests:
%s

Propose 3-8 sub-features that decompose %s cleanly. For each give a dot-nested
id, a one-line purpose, the capabilities it owns, and the invariants it owns.

Output JSON: {"sub_features":[{"id":str,"purpose":str,"capabilities":[str],"invariants":[str]}]}`,
		mustYAML(*target), mustJSON(clusters), featureID)

	resp, err := c.provider.Complete(ctx, CompletionRequest{
		SystemPrompt: "You decompose oversized Lattice features. Reply with JSON only.",
		UserMessage:  prompt,
		MaxTokens:    c.cfg.Agentic.LLM.MaxTokens,
	})
	if err != nil {
		return result, nil
	}
	var parsed struct {
		SubFeatures []SubFeature `json:"sub_features"`
	}
	if err := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); err != nil || len(parsed.SubFeatures) == 0 {
		return result, nil
	}
	result.Mode = ModeLLM
	result.SubFeatures = parsed.SubFeatures
	result.TokensUsed = resp.TokensUsed
	return result, nil
}

// clusterFeature groups invariants and capabilities into candidate sub-features
// by shared leading token of their ids.
func clusterFeature(m schema.Manifest) []SubFeature {
	groups := map[string]*SubFeature{}
	order := []string{}
	add := func(token string) *SubFeature {
		if g, ok := groups[token]; ok {
			return g
		}
		g := &SubFeature{
			ID:      m.ID + "." + token,
			Purpose: "Candidate group derived from id prefix " + token,
		}
		groups[token] = g
		order = append(order, token)
		return g
	}
	for _, cap := range m.Capabilities {
		g := add(leadingToken(cap.ID))
		g.Capabilities = append(g.Capabilities, cap.ID)
	}
	for _, inv := range m.Invariants {
		g := add("invariants")
		g.Invariants = append(g.Invariants, inv.ID)
	}
	sort.Strings(order)
	out := make([]SubFeature, 0, len(order))
	for i, token := range order {
		g := groups[token]
		if g.Purpose == "Candidate group derived from id prefix invariants" {
			g.ID = m.ID + ".candidate_group_" + itoa(i+1)
		}
		out = append(out, *g)
	}
	return out
}

func leadingToken(id string) string {
	if i := strings.IndexAny(id, "_."); i > 0 {
		return id[:i]
	}
	return id
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func mustYAML(v interface{}) string {
	b, _ := schema.MarshalCanonical(v)
	return string(b)
}
