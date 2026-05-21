package agentic

import (
	"context"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Mode records whether a capability result came from the LLM or its
// deterministic fallback.
type Mode string

const (
	ModeLLM           Mode = "llm"
	ModeDeterministic Mode = "deterministic"
)

// Capabilities is the entry point for the four built-in agentic capabilities.
// Each capability degrades to a deterministic result when no LLM is set up.
type Capabilities struct {
	repo     string
	cfg      config.Config
	provider Provider
}

// New builds the agentic capabilities for a repository.
func New(repo string, cfg config.Config) *Capabilities {
	return &Capabilities{repo: repo, cfg: cfg, provider: FromConfig(cfg.Agentic.LLM)}
}

// WithProvider overrides the LLM provider (used in tests).
func (c *Capabilities) WithProvider(p Provider) *Capabilities {
	c.provider = p
	return c
}

// LLMEnabled reports whether a real provider is configured.
func (c *Capabilities) LLMEnabled() bool { return Enabled(c.provider) }

// loadGraph extracts the repository into a knowledge graph.
func (c *Capabilities) loadGraph(ctx context.Context) (schema.KnowledgeGraph, error) {
	adCfg, err := config.LoadAdapters(c.repo)
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}
	reg := all.Registry(adCfg)
	res, err := extract.Extract(ctx, c.repo, reg, extract.Options{})
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}
	return graph.Build(graph.Input{
		Manifests:   res.Manifests,
		Modules:     res.Modules,
		Initiatives: res.Initiatives,
		Tasks:       res.Tasks,
	}, graph.Options{}), nil
}
