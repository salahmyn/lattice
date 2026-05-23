// Package config loads and represents Lattice's per-repo configuration from
// the lattice/ directory.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// File names within the lattice/ directory.
const (
	ConfigFile   = "config.yaml"
	AdaptersFile = "adapters.yaml"
	MCPFile      = "mcp.yaml"
)

// Config is the top-level lattice/config.yaml model.
type Config struct {
	Agentic         Agentic         `yaml:"agentic"`
	MutationTesting MutationTesting `yaml:"mutation_testing"`
	Analysis        Analysis        `yaml:"analysis"`
	Subprocess      Subprocess      `yaml:"subprocess"`
	SCIP            SCIP            `yaml:"scip"`
	Decomposition   Decomposition   `yaml:"decomposition"`
	Knowledge       Knowledge       `yaml:"knowledge"`
	Import          Import          `yaml:"import"`
}

// Import configures brownfield adoption (`lattice import`).
type Import struct {
	// MinCandidateSymbols is the fewest production symbols a source directory
	// must hold to be discovered as a feature candidate.
	MinCandidateSymbols int            `yaml:"min_candidate_symbols"`
	Coverage            ImportCoverage `yaml:"coverage"`
}

// ImportCoverage configures the adoption coverage report.
type ImportCoverage struct {
	// Exclude is a set of slash-path globs dropped from the symbol universe
	// before coverage is measured (generated code, fixtures).
	Exclude []string `yaml:"exclude"`
}

// Knowledge configures how the knowledge graph is emitted.
type Knowledge struct {
	Sharding Sharding `yaml:"sharding"`
}

// Sharding controls splitting lattice.json into per-group shard files.
type Sharding struct {
	Enabled  bool   `yaml:"enabled"`
	Strategy string `yaml:"strategy"` // by_feature_group | by_size
	// MaxFeaturesPerShard bounds a shard under the by_size strategy.
	MaxFeaturesPerShard int `yaml:"max_features_per_shard"`
}

// Agentic configures the optional LLM-backed capabilities.
type Agentic struct {
	LLM  LLM  `yaml:"llm"`
	Tone Tone `yaml:"tone"`
}

// Tone shapes the voice of every LLM-generated prose field — feature
// purposes, capability summaries, business narratives, annotation
// rationales. The labeler renders this into a contract that prepends
// the system prompt so a single setting steers all agentic capabilities.
// All fields are optional; the zero value yields the historical
// engineering-leaning voice.
type Tone struct {
	// Audience drives vocabulary: business | product | engineering | mixed.
	// "business" leans plain language, "engineering" allows jargon.
	Audience string `yaml:"audience"`
	// ReadingLevel: simple | intermediate | expert.
	ReadingLevel string `yaml:"reading_level"`
	// AvoidJargon turns on a stronger anti-jargon clause.
	AvoidJargon bool `yaml:"avoid_jargon"`
	// ExtraInstructions is appended verbatim to the tone contract so a
	// team can pin domain-specific words or examples.
	ExtraInstructions string `yaml:"extra_instructions"`
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
		Knowledge: Knowledge{Sharding: Sharding{
			Enabled: false, Strategy: "by_feature_group", MaxFeaturesPerShard: 200,
		}},
		Import: Import{MinCandidateSymbols: 3},
	}
}

// Load reads config.yaml from the lattice/ directory, layering it over
// Default(). A missing file is not an error: the defaults are returned.
func Load(latticeDir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(latticeDir, ConfigFile)
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
