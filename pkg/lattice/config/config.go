// Package config loads and represents Lattice's per-repo configuration from
// the .lattice/ directory.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Dir is the per-repo config directory name.
const Dir = ".lattice"

// File names within Dir.
const (
	ConfigFile   = "config.yaml"
	AdaptersFile = "adapters.yaml"
	MCPFile      = "mcp.yaml"
)

// Config is the top-level .lattice/config.yaml model.
type Config struct {
	Agentic         Agentic         `yaml:"agentic"`
	MutationTesting MutationTesting `yaml:"mutation_testing"`
	Analysis        Analysis        `yaml:"analysis"`
	Subprocess      Subprocess      `yaml:"subprocess"`
	SCIP            SCIP            `yaml:"scip"`
	Decomposition   Decomposition   `yaml:"decomposition"`
}

// Agentic configures the optional LLM-backed capabilities.
type Agentic struct {
	LLM LLM `yaml:"llm"`
}

// LLM configures the provider for agentic capabilities.
type LLM struct {
	Enabled   bool   `yaml:"enabled"`
	Provider  string `yaml:"provider"` // anthropic | openai | ollama
	Model     string `yaml:"model"`
	APIKeyEnv string `yaml:"api_key_env"`
	BaseURL   string `yaml:"base_url"`
	Timeout   string `yaml:"timeout"`
	MaxTokens int    `yaml:"max_tokens"`
}

// TimeoutDuration parses Timeout, falling back to 30s.
func (l LLM) TimeoutDuration() time.Duration {
	if d, err := time.ParseDuration(l.Timeout); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// MutationTesting configures mutation-test orchestration.
type MutationTesting struct {
	Enabled          bool               `yaml:"enabled"`
	Scope            string             `yaml:"scope"` // invariant_enforcers
	Thresholds       MutationThresholds `yaml:"thresholds"`
	TimeoutPerMutant string             `yaml:"timeout_per_mutant"`
}

// MutationThresholds holds the default mutation score and per-invariant
// overrides keyed "feature:INV-N".
type MutationThresholds struct {
	Default   float64            `yaml:"default"`
	Overrides map[string]float64 `yaml:"overrides,omitempty"`
}

// ThresholdFor returns the mutation threshold for a "feature:INV-N" key.
func (t MutationThresholds) ThresholdFor(key string) float64 {
	if v, ok := t.Overrides[key]; ok {
		return v
	}
	return t.Default
}

// Analysis configures the conflict/impact analyzer.
type Analysis struct {
	SimilarityWarnThreshold      float64 `yaml:"similarity_warn_threshold"`
	SimilarityDuplicateThreshold float64 `yaml:"similarity_duplicate_threshold"`
}

// Subprocess governs spawned structural checks, indexers, and runners.
type Subprocess struct {
	DefaultTimeout string `yaml:"default_timeout"`
}

// DefaultTimeoutDuration parses DefaultTimeout, falling back to 60s.
func (s Subprocess) DefaultTimeoutDuration() time.Duration {
	if d, err := time.ParseDuration(s.DefaultTimeout); err == nil && d > 0 {
		return d
	}
	return 60 * time.Second
}

// SCIP configures code-graph indexing.
type SCIP struct {
	CommitIndexes bool `yaml:"commit_indexes"`
}

// Decomposition holds the complexity thresholds that trigger a decomposition
// recommendation.
type Decomposition struct {
	MaxInvariants   int `yaml:"max_invariants"`
	MaxCapabilities int `yaml:"max_capabilities"`
	MaxSurfaces     int `yaml:"max_surfaces"`
}

// Default returns the built-in configuration used when no config file or
// individual field is present.
func Default() Config {
	return Config{
		Agentic: Agentic{LLM: LLM{
			Enabled:   false,
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-7",
			APIKeyEnv: "ANTHROPIC_API_KEY",
			Timeout:   "30s",
			MaxTokens: 2000,
		}},
		MutationTesting: MutationTesting{
			Enabled:          false,
			Scope:            "invariant_enforcers",
			Thresholds:       MutationThresholds{Default: 80},
			TimeoutPerMutant: "60s",
		},
		Analysis: Analysis{
			SimilarityWarnThreshold:      0.75,
			SimilarityDuplicateThreshold: 0.9,
		},
		Subprocess:    Subprocess{DefaultTimeout: "60s"},
		SCIP:          SCIP{CommitIndexes: false},
		Decomposition: Decomposition{MaxInvariants: 20, MaxCapabilities: 15, MaxSurfaces: 8},
	}
}

// Load reads .lattice/config.yaml from repoPath, layering it over Default().
// A missing file is not an error: the defaults are returned.
func Load(repoPath string) (Config, error) {
	cfg := Default()
	path := filepath.Join(repoPath, Dir, ConfigFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
