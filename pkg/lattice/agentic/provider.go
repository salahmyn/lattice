// Package agentic implements Lattice's optional LLM-backed capabilities and
// the provider abstraction behind them. Every capability has a deterministic
// fallback, so the framework works fully with no LLM configured.
package agentic

import (
	"context"
	"errors"
	"os"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// ErrNoProvider is returned by the NoOp provider; callers fall back to
// deterministic behavior when they see it.
var ErrNoProvider = errors.New("no LLM provider configured")

// CompletionRequest is one prompt to an LLM provider.
type CompletionRequest struct {
	SystemPrompt string
	UserMessage  string
	JSONSchema   *string // optional, requests structured output
	MaxTokens    int
	Temperature  float64
}

// CompletionResponse is an LLM provider's reply.
type CompletionResponse struct {
	Text       string
	TokensUsed int
}

// Provider is a backend for agentic capabilities.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

// NoOpProvider always errors; it stands in when no LLM is configured.
type NoOpProvider struct{}

// Name returns the provider name.
func (NoOpProvider) Name() string { return "none" }

// Complete always returns ErrNoProvider.
func (NoOpProvider) Complete(context.Context, CompletionRequest) (CompletionResponse, error) {
	return CompletionResponse{}, ErrNoProvider
}

// FromConfig builds a provider from the agentic LLM configuration. When the
// LLM is disabled or its API key is absent, a NoOpProvider is returned.
func FromConfig(cfg config.LLM) Provider {
	if !cfg.Enabled {
		return NoOpProvider{}
	}
	apiKey := os.Getenv(cfg.APIKeyEnv)
	switch cfg.Provider {
	case "anthropic":
		if apiKey == "" {
			return NoOpProvider{}
		}
		return &AnthropicProvider{APIKey: apiKey, Model: cfg.Model, BaseURL: cfg.BaseURL,
			Timeout: cfg.TimeoutDuration(), MaxTokens: cfg.MaxTokens}
	case "openai":
		if apiKey == "" {
			return NoOpProvider{}
		}
		return &OpenAIProvider{APIKey: apiKey, Model: cfg.Model, BaseURL: cfg.BaseURL,
			Timeout: cfg.TimeoutDuration(), MaxTokens: cfg.MaxTokens}
	case "ollama":
		return &OllamaProvider{Model: cfg.Model, BaseURL: cfg.BaseURL, Timeout: cfg.TimeoutDuration()}
	default:
		return NoOpProvider{}
	}
}

// Enabled reports whether p is a real provider.
func Enabled(p Provider) bool {
	_, isNoOp := p.(NoOpProvider)
	return !isNoOp
}
