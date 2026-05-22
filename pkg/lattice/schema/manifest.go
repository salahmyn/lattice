// Package schema holds the authoritative data-model structs for Lattice.
//
// These structs are the contract: the readable mirror in the design doc
// follows them, not the other way around. YAML field order is the struct
// field order, which gives canonical, diff-stable on-disk output.
package schema

import "strings"

// InlineText collapses all internal whitespace (including newlines) in s to
// single spaces and trims the ends. Use it when a multi-line field such as a
// manifest `purpose` is rendered in a single-line or inline context.
func InlineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Status is the lifecycle state of a feature manifest.
type Status string

const (
	StatusProposal   Status = "proposal"
	StatusAccepted   Status = "accepted"
	StatusProduction Status = "production"
	StatusDeprecated Status = "deprecated"
)

// ValidStatuses lists every legal manifest status.
var ValidStatuses = []Status{StatusProposal, StatusAccepted, StatusProduction, StatusDeprecated}

// Manifest is a YAML file declaring exactly one feature.
type Manifest struct {
	// --- Required ---
	ID      string `yaml:"id"`
	Version int    `yaml:"version"`
	Status  Status `yaml:"status"`
	Purpose string `yaml:"purpose"`
	Owners  Owners `yaml:"owners"`

	// --- Optional ---
	Value                *Value            `yaml:"value,omitempty"`
	Capabilities         []Capability      `yaml:"capabilities,omitempty"`
	Invariants           []Invariant       `yaml:"invariants,omitempty"`
	DependsOn            []string          `yaml:"depends_on,omitempty"`
	ComposesInvariantsOf []string          `yaml:"composes_invariants_of,omitempty"`
	Surface              []Surface         `yaml:"surface,omitempty"`
	Errors               []ErrorDecl       `yaml:"errors,omitempty"`
	Decisions            []Decision        `yaml:"decisions,omitempty"`
	Migration            *Migration        `yaml:"migration,omitempty"`
	Roles                []Role            `yaml:"roles,omitempty"`
	StructuralChecks     []StructuralCheck `yaml:"structural_checks,omitempty"`

	// --- Auto-populated (never hand-edited) ---
	Implementations []Implementation   `yaml:"implementations,omitempty"`
	Verifications   []Verification     `yaml:"verifications,omitempty"`
	MutationScores  map[string]float64 `yaml:"mutation_scores,omitempty"`
	Children        []string           `yaml:"children,omitempty"`

	// SourcePath is the repo-relative path the manifest was loaded from.
	// Not serialized; populated by the extractor.
	SourcePath string `yaml:"-"`
}

// Owners names the teams accountable for a feature.
type Owners struct {
	Business    string `yaml:"business"`
	Engineering string `yaml:"engineering"`
	OnCall      string `yaml:"on_call,omitempty"`
}

// Value describes who a feature is for.
type Value struct {
	Customer string `yaml:"customer,omitempty"`
	Business string `yaml:"business,omitempty"`
}

// Capability is a named behavior of a feature with prose rules.
type Capability struct {
	ID              string   `yaml:"id"`
	Summary         string   `yaml:"summary"`
	Rules           []string `yaml:"rules"`
	CounterExamples []string `yaml:"counter_examples,omitempty"`
}

// VerifiableBy enumerates how an invariant may be verified.
type VerifiableBy string

const (
	VerifiableByTest       VerifiableBy = "test"
	VerifiableByStructural VerifiableBy = "structural"
	VerifiableByMutation   VerifiableBy = "mutation"
)

// Invariant is a constraint a feature must always satisfy.
type Invariant struct {
	ID           string         `yaml:"id"` // format "INV-N"
	Statement    string         `yaml:"statement"`
	VerifiableBy []VerifiableBy `yaml:"verifiable_by,omitempty"` // default ["test"]
}

// EffectiveVerifiableBy returns the verification methods, applying the
// documented default of ["test"] when none are declared.
func (inv Invariant) EffectiveVerifiableBy() []VerifiableBy {
	if len(inv.VerifiableBy) == 0 {
		return []VerifiableBy{VerifiableByTest}
	}
	return inv.VerifiableBy
}

// SurfaceType discriminates the Surface union.
type SurfaceType string

const (
	SurfaceHTTP           SurfaceType = "http"
	SurfaceEventEmit      SurfaceType = "event_emit"
	SurfaceEventConsume   SurfaceType = "event_consume"
	SurfaceWebhookReceive SurfaceType = "webhook_receive"
	SurfaceScheduled      SurfaceType = "scheduled"
	SurfaceModule         SurfaceType = "module"
)

// Surface is a discriminated union keyed on Type. A flat struct is used so
// YAML round-trips cleanly; only fields relevant to Type are populated.
type Surface struct {
	Type SurfaceType `yaml:"type"`

	// http / webhook_receive
	Method string `yaml:"method,omitempty"`
	Path   string `yaml:"path,omitempty"`

	// http
	RequestSchema      string   `yaml:"request_schema,omitempty"`
	ResponseSchema     string   `yaml:"response_schema,omitempty"`
	Behavior           []string `yaml:"behavior,omitempty"`
	BreakingChangeFrom string   `yaml:"breaking_change_from,omitempty"`

	// event_emit / event_consume
	Name          string `yaml:"name,omitempty"`
	PayloadSchema string `yaml:"payload_schema,omitempty"`
	Semantics     string `yaml:"semantics,omitempty"`

	// webhook_receive
	Auth string `yaml:"auth,omitempty"`

	// scheduled
	Schedule string `yaml:"schedule,omitempty"`
	Job      string `yaml:"job,omitempty"`

	// module
	Description string `yaml:"description,omitempty"`
}

// ErrorDecl is one entry in a feature's error/response contract: a named
// failure mode a caller can observe.
type ErrorDecl struct {
	Code        string `yaml:"code"`
	Status      int    `yaml:"status,omitempty"` // HTTP status, when applicable
	Description string `yaml:"description,omitempty"`
}

// Decision links a feature to an architecture decision record.
type Decision struct {
	ADR     string `yaml:"adr"`
	Summary string `yaml:"summary"`
}

// MigrationStrategy enumerates breaking-change rollout strategies.
type MigrationStrategy string

const (
	MigrationParallelEndpoints MigrationStrategy = "parallel_endpoints"
	MigrationFeatureFlag       MigrationStrategy = "feature_flag"
	MigrationGradualRollout    MigrationStrategy = "gradual_rollout"
	MigrationBreakingCutover   MigrationStrategy = "breaking_cutover"
)

// Migration describes a breaking-change rollout plan.
type Migration struct {
	Strategy     MigrationStrategy `yaml:"strategy"`
	CurrentPhase string            `yaml:"current_phase"`
	Phases       []MigrationPhase  `yaml:"phases"`
}

// PhaseStatus is the state of one migration phase.
type PhaseStatus string

const (
	PhasePending    PhaseStatus = "pending"
	PhaseInProgress PhaseStatus = "in_progress"
	PhaseComplete   PhaseStatus = "complete"
)

// MigrationPhase is one step of a migration.
type MigrationPhase struct {
	ID          string      `yaml:"id"`
	Description string      `yaml:"description"`
	Gate        string      `yaml:"gate,omitempty"`
	Status      PhaseStatus `yaml:"status"`
	Consumers   []string    `yaml:"consumers,omitempty"`
}

// Role is a named category of symbols carrying invariants automatically.
type Role struct {
	ID                string   `yaml:"id"`
	Description       string   `yaml:"description"`
	AppliesInvariants []string `yaml:"applies_invariants"`
}

// StructuralCheck declares a user-provided executable that verifies an
// invariant by inspecting code structure.
type StructuralCheck struct {
	ID                 string                 `yaml:"id"`
	Command            []string               `yaml:"command"`
	VerifiesInvariants []string               `yaml:"verifies_invariants"`
	Scope              StructuralCheckScope   `yaml:"scope,omitempty"`
	Config             map[string]interface{} `yaml:"config,omitempty"`
}

// StructuralCheckScope narrows where a structural check applies.
type StructuralCheckScope struct {
	Modules []string `yaml:"modules,omitempty"`
	Files   []string `yaml:"files,omitempty"`
}

// Implementation is an auto-populated link from code to a feature.
type Implementation struct {
	Symbol          string   `yaml:"symbol"`
	File            string   `yaml:"file"`
	Line            int      `yaml:"line"`
	Language        string   `yaml:"language"`
	ViaCapabilities []string `yaml:"via_capabilities,omitempty"`
}

// Verification is an auto-populated link from a test to a feature concept.
type Verification struct {
	Symbol   string   `yaml:"symbol"`
	File     string   `yaml:"file"`
	Line     int      `yaml:"line"`
	Language string   `yaml:"language"`
	Verifies []string `yaml:"verifies"`
}
