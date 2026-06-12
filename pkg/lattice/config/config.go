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
	Architecture    Architecture    `yaml:"architecture,omitempty"` // v0.7 — AMA support
	Autonomy        Autonomy        `yaml:"autonomy,omitempty"`     // v0.8 — agent steering
	Runtime         Runtime         `yaml:"runtime,omitempty"`      // v0.8 — V0 runs-clean gate
}

// Runtime (v0.8) describes how to install, build, boot, and probe the
// application for the V0 runs-clean gate ("gate zero"): nothing in a
// workspace is demonstrated while the app fails to install, build,
// boot, and answer its smoke probes from a clean state. The whole block
// is opt-in; `lattice runs-clean` errors when it is absent.
type Runtime struct {
	// CleanInstall restores dependencies from the lockfile in a fresh
	// state, e.g. "npm ci" or "go mod download". Catches dependencies
	// used in code but never declared/locked.
	CleanInstall string `yaml:"clean_install,omitempty"`
	// Build compiles the application, e.g. "npm run build" or "go build ./...".
	Build string `yaml:"build,omitempty"`
	// Boot starts the application and is expected to stay up, e.g. "npm start".
	// Empty means the project has no long-running process — the boot and
	// probe steps are skipped.
	Boot string `yaml:"boot,omitempty"`
	// BootWaitMS is how long to wait after Boot before probing (default 5000).
	BootWaitMS int `yaml:"boot_wait_ms,omitempty"`
	// Probes are smoke checks against the booted app: a health endpoint
	// plus one probe per shipped entry point.
	Probes []Probe `yaml:"probes,omitempty"`
}

// Probe is one HTTP smoke check run against the booted application.
type Probe struct {
	URL          string `yaml:"url"`
	Method       string `yaml:"method,omitempty"`        // default GET
	ExpectStatus int    `yaml:"expect_status,omitempty"` // default any 2xx
}

// Configured reports whether a runtime block is present at all.
func (r Runtime) Configured() bool {
	return r.CleanInstall != "" || r.Build != "" || r.Boot != ""
}

// Autonomy (v0.8) configures how far an autonomous agent may advance a
// unit up the truth-level ladder before a human is required. The whole
// block is opt-in: an absent block means default_mode "" — "human
// approves everything", which is exactly the v0.7 behaviour.
//
// Modes are policy over the truth-levels; they do not change what is
// true, only who may sign off on a transition. Independent of mode, the
// validator continues to enforce Lattice's safety floor (LLM-BRD
// approval, regulatory/legal/financial constraints) — no mode lowers it.
type Autonomy struct {
	// DefaultMode names the active policy: "gated" | "autonomous" |
	// "tiered". Empty means fully human-gated (v0.7 behaviour).
	DefaultMode string `yaml:"default_mode,omitempty"`

	// RequireActor makes an unattributed transition a validation warning
	// (UNATTRIBUTED_CHANGE) — every lease and ledger entry must name an
	// --actor.
	RequireActor bool `yaml:"require_actor,omitempty"`

	// Modes holds the per-mode policy. When empty, BuiltinModes() supplies
	// the three standard profiles so a workspace need only set DefaultMode.
	Modes map[string]AutonomyMode `yaml:"modes,omitempty"`

	// Attestation records the independence level of check runs:
	//   self     — the authoring agent ran its own checks; reports carry
	//              the SELF-ATTESTED banner and prove nothing adversarially
	//   isolated — distinct actors did the work and an orchestrator (not
	//              the author) ran the checks; human gates were real humans
	//   bound    — checks run in CI on push and approvals are signed
	//              commits/PR reviews from distinct credentials
	// It is reported on the RTM header; claims must never exceed it.
	// Empty means "self".
	Attestation string `yaml:"attestation,omitempty"`
}

// AttestationLevel returns the configured attestation, defaulting to
// "self" — the honest floor when nothing was declared.
func (a Autonomy) AttestationLevel() string {
	switch a.Attestation {
	case "isolated", "bound":
		return a.Attestation
	default:
		return "self"
	}
}

// AutonomyMode is one named policy profile.
type AutonomyMode struct {
	// AgentMayAdvance lists the truth-levels an agent may move a unit to
	// without a human: any of "declared", "wired", "demonstrated",
	// "correctly-meant".
	AgentMayAdvance []string `yaml:"agent_may_advance,omitempty"`
	// HumanGate lists transition classes that always require a human:
	// "correctly-meant", "brd_approval".
	HumanGate []string `yaml:"human_gate,omitempty"`
	// AutoMergeAt, when set, names the truth-level at which a unit's change
	// auto-merges (tiered mode).
	AutoMergeAt string `yaml:"auto_merge_at,omitempty"`
}

// BuiltinModes returns the three standard profiles. A workspace that sets
// only default_mode inherits these; an explicit modes: block overrides
// the matching entry.
func BuiltinModes() map[string]AutonomyMode {
	return map[string]AutonomyMode{
		"autonomous": {
			AgentMayAdvance: []string{"declared", "wired", "demonstrated", "correctly-meant"},
		},
		"gated": {
			AgentMayAdvance: []string{"declared", "wired", "demonstrated"},
			HumanGate:       []string{"correctly-meant", "brd_approval"},
		},
		"tiered": {
			AgentMayAdvance: []string{"declared", "wired"},
			AutoMergeAt:     "demonstrated",
			HumanGate:       []string{"correctly-meant"},
		},
	}
}

// Mode resolves the active mode profile, falling back to the builtin of
// the same name. The second result is false when no mode is active
// (DefaultMode empty) — the human-approves-everything default.
func (a Autonomy) Mode() (AutonomyMode, bool) {
	if a.DefaultMode == "" {
		return AutonomyMode{}, false
	}
	if m, ok := a.Modes[a.DefaultMode]; ok {
		return m, true
	}
	if m, ok := BuiltinModes()[a.DefaultMode]; ok {
		return m, true
	}
	return AutonomyMode{}, false
}

// MayAdvanceMeaning reports whether the active mode lets an agent advance
// a unit to "correctly-meant" without a human. The safety floor (§7)
// applies on top regardless.
func (a Autonomy) MayAdvanceMeaning() bool {
	m, ok := a.Mode()
	if !ok {
		return false
	}
	for _, lvl := range m.AgentMayAdvance {
		if lvl == "correctly-meant" {
			return true
		}
	}
	return false
}

// Architecture configures v0.7's AMA enforcement layer. The whole
// block is opt-in: with AMAMode=false (the default) the structural
// checks still run but fire as warnings, and CROSS_FEATURE_IMPORT
// downgrades from error to warning. Existing brownfield projects
// see no behavior change unless the operator turns AMAMode on.
type Architecture struct {
	// AMAMode flips the enforcement to error-severity for
	// CROSS_FEATURE_IMPORT and tightens MIXED_COMMAND_QUERY (which
	// otherwise stays silent — a "mixed" capability is the legacy
	// default and shouldn't pollute the validation noise floor).
	AMAMode bool `yaml:"ama_mode,omitempty"`

	// FileLineCap is the maximum source-file length in lines before
	// FILE_LINE_CAP fires. Default 150 (AMA spec §5).
	FileLineCap int `yaml:"file_line_cap,omitempty"`

	// MethodLineCap is the maximum method/function length in lines
	// before METHOD_LINE_CAP fires. Default 25 (AMA spec §5).
	// Detection is approximate — we use the distance to the next
	// symbol's start line as the method's footprint, which is enough
	// to catch sprawling functions without an adapter-side AST end
	// position.
	MethodLineCap int `yaml:"method_line_cap,omitempty"`
}

// EffectiveFileLineCap returns the configured cap or the AMA default.
func (a Architecture) EffectiveFileLineCap() int {
	if a.FileLineCap > 0 {
		return a.FileLineCap
	}
	return 150
}

// EffectiveMethodLineCap returns the configured cap or the AMA default.
func (a Architecture) EffectiveMethodLineCap() int {
	if a.MethodLineCap > 0 {
		return a.MethodLineCap
	}
	return 25
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
			Model:     "claude-sonnet-4-6",
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
