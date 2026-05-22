package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// LLMLabelOptions tunes grounded LLM labeling.
type LLMLabelOptions struct {
	MaxTokens int
	// CacheDir, when set, caches label results by candidate id so a re-run is
	// stable and cheap despite the LLM being non-deterministic.
	CacheDir string
}

const labelSystemPrompt = "You label a cluster of code as one software feature for the " +
	"Lattice knowledge graph. Name and describe only what the supplied evidence supports — " +
	"never invent symbols, capabilities, or behavior the evidence does not show. " +
	"Reply with JSON only."

// LabelWithLLM runs Stage 2 with an LLM: each candidate is labeled from a
// grounded prompt built only from its own evidence. Any candidate the LLM
// cannot label falls back to the deterministic skeleton, so the result is
// always complete. Labels are cached by candidate id.
func LabelWithLLM(ctx context.Context, cf CandidatesFile, provider agentic.Provider, opts LLMLabelOptions) []Draft {
	cache := loadLabelCache(opts.CacheDir)
	seen := map[string]bool{}
	drafts := make([]Draft, 0, len(cf.Candidates))
	for _, c := range cf.Candidates {
		m := llmLabelOne(ctx, c, provider, opts, cache)
		m.ID = uniqueID(sanitizeFeatureID(m.ID, c.Package), seen)
		drafts = append(drafts, Draft{CandidateID: c.ID, Manifest: m})
	}
	cache.save()
	return drafts
}

// labelResponse is the JSON shape the LLM is asked to return.
type labelResponse struct {
	ID           string `json:"id"`
	Purpose      string `json:"purpose"`
	Capabilities []struct {
		ID      string   `json:"id"`
		Summary string   `json:"summary"`
		Rules   []string `json:"rules"`
	} `json:"capabilities"`
}

// llmLabelOne labels a single candidate, falling back to the deterministic
// skeleton on any cache miss + LLM failure.
func llmLabelOne(ctx context.Context, c Candidate, provider agentic.Provider,
	opts LLMLabelOptions, cache *labelCache) schema.Manifest {

	if m, ok := cache.get(c.ID); ok {
		return m
	}
	fallback := draftManifest(deriveFeatureID(c.Package), c)

	resp, err := provider.Complete(ctx, agentic.CompletionRequest{
		SystemPrompt: labelSystemPrompt,
		UserMessage:  groundedLabelPrompt(c),
		MaxTokens:    opts.MaxTokens,
	})
	if err != nil {
		return fallback
	}
	var parsed labelResponse
	if jerr := json.Unmarshal([]byte(extractJSON(resp.Text)), &parsed); jerr != nil {
		return fallback
	}
	m := manifestFromLabel(parsed)
	if m.ID == "" || len(m.Capabilities) == 0 {
		return fallback
	}
	cache.put(c.ID, m)
	return m
}

// groundedLabelPrompt builds the per-candidate prompt from its evidence only.
func groundedLabelPrompt(c Candidate) string {
	var b strings.Builder
	b.WriteString("A static analysis grouped these code symbols into one feature candidate.\n\n")
	b.WriteString("Package: " + c.Package + "\n\nSymbols:\n")
	for _, s := range c.Symbols {
		b.WriteString("  - " + s + "\n")
	}
	b.WriteString("\nEvidence:\n")
	for _, e := range c.Evidence {
		b.WriteString("  - [" + e.Signal + "] " + e.Detail + "\n")
	}
	b.WriteString(`
Name this feature. Reply with JSON only:
{"id":"<short dotted lower_snake_case id>","purpose":"<one sentence>",` +
		`"capabilities":[{"id":"<lower_snake_case>","summary":"<one sentence>","rules":["<rule the capability must follow>"]}]}`)
	return b.String()
}

// manifestFromLabel turns the LLM's reply into a proposal manifest. It never
// drafts invariants: an unenforceable invariant would fail Stage-4
// verification, so invariant discovery is left to a human reviewer.
func manifestFromLabel(r labelResponse) schema.Manifest {
	m := schema.Manifest{
		ID:      r.ID,
		Version: 1,
		Status:  schema.StatusProposal,
		Purpose: schema.InlineText(r.Purpose),
		Owners:  schema.Owners{Business: "TODO-team", Engineering: "TODO-team"},
	}
	for _, c := range r.Capabilities {
		id := sanitizeSegment(c.ID)
		if id == "" {
			continue
		}
		rules := c.Rules
		if len(rules) == 0 {
			rules = []string{"TODO: state a rule this capability must follow."}
		}
		m.Capabilities = append(m.Capabilities, schema.Capability{
			ID: id, Summary: schema.InlineText(c.Summary), Rules: rules,
		})
	}
	return m
}

// sanitizeFeatureID cleans an LLM-proposed dotted id, falling back to a
// package-derived id when nothing usable remains.
func sanitizeFeatureID(id, pkg string) string {
	parts := make([]string, 0)
	for _, seg := range strings.Split(id, ".") {
		if clean := sanitizeSegment(seg); clean != "" {
			parts = append(parts, clean)
		}
	}
	if len(parts) == 0 {
		return deriveFeatureID(pkg)
	}
	return strings.Join(parts, ".")
}

// extractJSON returns the substring from the first '{' to the last '}'.
func extractJSON(s string) string {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j < 0 || j < i {
		return s
	}
	return s[i : j+1]
}

// labelCache is a candidate-id-keyed cache of LLM label results.
type labelCache struct {
	path    string
	entries map[string]json.RawMessage
	dirty   bool
}

func loadLabelCache(dir string) *labelCache {
	lc := &labelCache{entries: map[string]json.RawMessage{}}
	if dir == "" {
		return lc
	}
	lc.path = filepath.Join(dir, "import-label-cache.json")
	if data, err := os.ReadFile(lc.path); err == nil {
		_ = json.Unmarshal(data, &lc.entries)
	}
	return lc
}

func (lc *labelCache) get(key string) (schema.Manifest, bool) {
	raw, ok := lc.entries[key]
	if !ok {
		return schema.Manifest{}, false
	}
	var m schema.Manifest
	if json.Unmarshal(raw, &m) != nil {
		return schema.Manifest{}, false
	}
	return m, true
}

func (lc *labelCache) put(key string, m schema.Manifest) {
	if raw, err := json.Marshal(m); err == nil {
		lc.entries[key] = raw
		lc.dirty = true
	}
}

func (lc *labelCache) save() {
	if lc.path == "" || !lc.dirty {
		return
	}
	_ = os.MkdirAll(filepath.Dir(lc.path), 0o755)
	if data, err := json.MarshalIndent(lc.entries, "", "  "); err == nil {
		_ = os.WriteFile(lc.path, data, 0o644)
	}
}
