package agentic

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ProposalResult is the output of the proposal-drafting capability.
type ProposalResult struct {
	Mode          Mode     `json:"mode"`
	ManifestYAML  string   `json:"manifest_yaml"`
	OpenQuestions []string `json:"open_questions"`
	SchemaValid   bool     `json:"schema_valid"`
	TokensUsed    int      `json:"tokens_used,omitempty"`
}

// DraftProposal turns a prose change description into a proposal manifest.
// Without an LLM it emits a schema skeleton with TODO placeholders.
func (c *Capabilities) DraftProposal(ctx context.Context, prose, targetFeature string) (ProposalResult, error) {
	kg, err := c.loadGraph(ctx)
	if err != nil {
		return ProposalResult{}, err
	}

	if !c.LLMEnabled() {
		return deterministicProposal(prose, targetFeature, kg), nil
	}

	related := relatedManifests(kg, prose, targetFeature)
	prompt := fmt.Sprintf(`The user wants to propose this change:

%s

Existing related features:
%s

Draft a proposal manifest. Status must be "proposal". Identify:
  - whether this extends an existing feature or creates a new one
  - 1-3 new or modified capabilities
  - 1-3 new invariants
  - 2-5 open questions needing human resolution

Output a valid Lattice manifest YAML, then a line "OPEN QUESTIONS:" followed by a bullet list.`,
		prose, related)

	resp, err := c.provider.Complete(ctx, CompletionRequest{
		SystemPrompt: "You draft Lattice feature manifests. Output YAML.",
		UserMessage:  prompt,
		MaxTokens:    c.cfg.Agentic.LLM.MaxTokens,
	})
	if err != nil {
		return deterministicProposal(prose, targetFeature, kg), nil
	}

	manifestYAML, questions := splitProposal(resp.Text)
	result := ProposalResult{
		Mode: ModeLLM, ManifestYAML: manifestYAML,
		OpenQuestions: questions, TokensUsed: resp.TokensUsed,
	}
	var m schema.Manifest
	result.SchemaValid = yaml.Unmarshal([]byte(manifestYAML), &m) == nil && m.ID != ""
	if !result.SchemaValid {
		// Fall back when the model produced unusable YAML.
		return deterministicProposal(prose, targetFeature, kg), nil
	}
	return result, nil
}

// deterministicProposal builds a manifest skeleton with TODO placeholders.
func deterministicProposal(prose, targetFeature string, kg schema.KnowledgeGraph) ProposalResult {
	id := targetFeature
	if id == "" {
		id = "TODO.feature_id"
	}
	version := 1
	for _, m := range kg.Features {
		if m.ID == targetFeature {
			version = m.Version + 1
		}
	}
	skeleton := schema.Manifest{
		ID:      id,
		Version: version,
		Status:  schema.StatusProposal,
		Purpose: "TODO: " + strings.TrimSpace(prose),
		Owners:  schema.Owners{Business: "TODO-team", Engineering: "TODO-team"},
		Capabilities: []schema.Capability{{
			ID: "TODO_capability", Summary: "TODO: describe the capability",
			Rules: []string{"TODO: state at least one rule"},
		}},
		Invariants: []schema.Invariant{{
			ID: "INV-1", Statement: "TODO: state the invariant this change must preserve",
		}},
	}
	out, _ := schema.MarshalCanonical(skeleton)
	return ProposalResult{
		Mode:         ModeDeterministic,
		ManifestYAML: string(out),
		OpenQuestions: []string{
			"Does this extend an existing feature or create a new one?",
			"What capabilities does this change add or modify?",
			"What invariants must the change preserve?",
			"Which teams own the business and engineering sides?",
		},
		SchemaValid: true,
	}
}

// relatedManifests returns a YAML summary of manifests lexically related to
// the prose or target feature.
func relatedManifests(kg schema.KnowledgeGraph, prose, targetFeature string) string {
	var b strings.Builder
	low := strings.ToLower(prose)
	for _, m := range kg.Features {
		if m.ID == targetFeature || sharesWord(low, strings.ToLower(m.Purpose)) {
			b.WriteString(fmt.Sprintf("- %s: %s\n", m.ID, m.Purpose))
		}
	}
	if b.Len() == 0 {
		return "(none)"
	}
	return b.String()
}

func sharesWord(a, b string) bool {
	fields := strings.Fields(b)
	for _, w := range fields {
		if len(w) > 4 && strings.Contains(a, w) {
			return true
		}
	}
	return false
}

// splitProposal separates the manifest YAML from a trailing open-questions list.
func splitProposal(text string) (string, []string) {
	idx := strings.Index(strings.ToUpper(text), "OPEN QUESTIONS")
	if idx < 0 {
		return extractYAML(text), nil
	}
	manifest := extractYAML(text[:idx])
	var questions []string
	for _, ln := range strings.Split(text[idx:], "\n") {
		ln = strings.TrimSpace(ln)
		ln = strings.TrimLeft(ln, "-*0123456789. ")
		if ln != "" && !strings.EqualFold(ln, "OPEN QUESTIONS:") && !strings.EqualFold(ln, "OPEN QUESTIONS") {
			questions = append(questions, ln)
		}
	}
	return manifest, questions
}
