package cli

import (
	"context"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// probeLLMProvider sends a tiny round-trip through the configured
// provider and reports the verbatim outcome. v0.2.1 #7 — surfaces
// upgrade_required, unsupported_model, DNS, and missing-key errors at
// setup time rather than once per candidate during a 50-minute draft.
func probeLLMProvider(ctx context.Context, io *IO) *llmProbe {
	ws, err := openWorkspace(io)
	if err != nil {
		return &llmProbe{Error: "no workspace — run `lattice init` first"}
	}
	cfg, _ := config.Load(ws.LatticeDir)
	out := &llmProbe{
		Provider: cfg.Agentic.LLM.Provider,
		Model:    cfg.Agentic.LLM.Model,
		BaseURL:  cfg.Agentic.LLM.BaseURL,
	}
	if !cfg.Agentic.LLM.Enabled {
		out.Error = "agentic.llm.enabled is false; nothing to probe"
		out.Suggestion = "set agentic.llm.enabled: true in lattice/config.yaml"
		return out
	}
	provider := agentic.FromConfig(cfg.Agentic.LLM)
	if !agentic.Enabled(provider) {
		out.Error = "provider falls back to NoOp — check provider name and that $" + cfg.Agentic.LLM.APIKeyEnv + " is set"
		out.Suggestion = "export " + cfg.Agentic.LLM.APIKeyEnv + "=<your-key> and ensure provider is one of: anthropic, openai, ollama"
		return out
	}
	// Tiny prompt — never asks the model to think, just round-trip.
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t0 := time.Now()
	resp, err := provider.Complete(probeCtx, agentic.CompletionRequest{
		SystemPrompt: "You are a connectivity probe. Reply with exactly: ok",
		UserMessage:  "probe",
		MaxTokens:    16,
	})
	out.ElapsedMS = time.Since(t0).Milliseconds()
	if err != nil {
		out.Error = err.Error()
		out.Suggestion = probeSuggestion(out.Error, cfg.Agentic.LLM)
		return out
	}
	out.OK = true
	out.Reply = strings.TrimSpace(resp.Text)
	out.Tokens = resp.TokensUsed
	return out
}

// probeSuggestion maps a known error fragment to an actionable next
// step. The pattern set is intentionally narrow — only the failures we
// have actually seen from real providers are heuristically named.
func probeSuggestion(errText string, llm config.LLM) string {
	lower := strings.ToLower(errText)
	switch {
	case strings.Contains(lower, "upgrade_required") || strings.Contains(lower, "plan"):
		return "the API key is valid but the account plan doesn't include API access — upgrade or switch providers"
	case strings.Contains(lower, "unsupported_model") || strings.Contains(lower, "model"):
		return "model name not recognised by the provider — check the supported list and the provider/model prefix"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "dns") || strings.Contains(lower, "lookup"):
		return "DNS could not resolve " + llm.BaseURL + " — check the base_url for typos or VPN"
	case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized"):
		return "key was rejected — verify $" + llm.APIKeyEnv + " is set to a valid key for provider " + llm.Provider
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timeout"):
		return "provider didn't respond within the configured timeout — raise agentic.llm.timeout or check network"
	}
	return ""
}
