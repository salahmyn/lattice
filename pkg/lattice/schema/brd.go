package schema

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// BRDStatus is the lifecycle state of a Business Requirements Document.
//
// A BRD is the layer above the Feature axis: it captures *business intent*
// (problem, goals, stakeholders, success criteria, constraints) and points
// downward at the features that implement it. One BRD → many features;
// one feature → at most one BRD.
//
// The lifecycle is intentionally separate from feature lifecycle:
//   - draft       — scaffolded or LLM-regenerated, needs human review
//   - proposed    — circulated, awaiting approval signatures
//   - approved    — signed off; downstream features can reference it
//   - superseded  — replaced by a later BRD; kept for history
type BRDStatus string

const (
	BRDDraft      BRDStatus = "draft"
	BRDProposed   BRDStatus = "proposed"
	BRDApproved   BRDStatus = "approved"
	BRDSuperseded BRDStatus = "superseded"
)

// ValidBRDStatuses lists every legal BRD status.
var ValidBRDStatuses = []BRDStatus{BRDDraft, BRDProposed, BRDApproved, BRDSuperseded}

// BRDProvenanceSource discriminates where a BRD originated. Brownfield
// projects regenerate BRDs from existing code via LLM (`llm_from_code`),
// and the validator treats those differently — they always require a
// human approval pass before becoming the source of truth.
type BRDProvenanceSource string

const (
	BRDSourceHuman       BRDProvenanceSource = "human"
	BRDSourceLLMFromCode BRDProvenanceSource = "llm_from_code"
)

// BRD is a YAML file declaring exactly one Business Requirements Document.
//
// Lives at lattice/brds/<id>.yaml. Field order on disk follows struct
// order so two unrelated edits never produce noisy reorderings — same
// canonical-YAML contract as Manifest.
type BRD struct {
	// --- Required ---
	ID              string    `yaml:"id"`
	Version         int       `yaml:"version"`
	Status          BRDStatus `yaml:"status"`
	Title           string    `yaml:"title"`
	BusinessProblem string    `yaml:"business_problem"`

	// --- Body ---
	BusinessGoals   []string        `yaml:"business_goals,omitempty"`
	Stakeholders    BRDStakeholders `yaml:"stakeholders,omitempty"`
	UserScenarios   []UserScenario  `yaml:"user_scenarios,omitempty"`
	SuccessCriteria []BRDCriterion  `yaml:"success_criteria,omitempty"`
	Constraints     []BRDConstraint `yaml:"constraints,omitempty"`
	OutOfScope      []string        `yaml:"out_of_scope,omitempty"`

	// ImplementsVia is the forward link to the Feature axis. Empty for a
	// freshly-drafted BRD; populated as features land. The validator
	// treats a non-empty entry that names a missing feature as an error
	// (BRD_PHANTOM_FEATURE).
	ImplementsVia []string `yaml:"implements_via,omitempty"`

	// Blocks are the block-graph blocks the BRD touches (block ids). The
	// engine rolls their status and test coverage up onto the BRD and finds
	// the blocks two BRDs share.
	Blocks []string `yaml:"blocks,omitempty"`
	// Flags are the switches and values the BRD depends on, each with what
	// it means in plain words.
	Flags []BRDFlag `yaml:"flags,omitempty"`
	// OpenQuestions are what the source cannot answer and a person must.
	OpenQuestions []string `yaml:"open_questions,omitempty"`

	// Approval is the human sign-off block. Set to all-empty for drafts;
	// when present, ApprovedVersion freezes which version of the BRD the
	// approver actually signed.
	Approval *BRDApproval `yaml:"approval,omitempty"`

	// Provenance records how the BRD came to exist. `llm_from_code` BRDs
	// in `draft` raise BRD_UNAPPROVED_LLM until a human flips them to
	// `approved`.
	Provenance BRDProvenance `yaml:"provenance,omitempty"`

	// HumanReviewRequired is a hint set by the LLM regenerator so the UI
	// surfaces a review badge. Not enforced by validation directly —
	// the BRD_UNAPPROVED_LLM rule covers the policy.
	HumanReviewRequired bool `yaml:"human_review_required,omitempty"`

	// --- Auto-populated (never hand-edited) ---
	// SourcePath is the repo-relative path the BRD was loaded from.
	SourcePath string `yaml:"-"`
}

// BRDStakeholders names the humans accountable for the business outcome.
// Owners is the technical owner ledger; Stakeholders is its business
// counterpart and intentionally has more roles (legal, compliance) than
// the technical Owners struct.
type BRDStakeholders struct {
	BusinessOwner    string `yaml:"business_owner,omitempty"`
	ProductOwner     string `yaml:"product_owner,omitempty"`
	EngineeringOwner string `yaml:"engineering_owner,omitempty"`
	Legal            string `yaml:"legal,omitempty"`
	Compliance       string `yaml:"compliance,omitempty"`
}

// UserScenario is a narrative description of one user-visible flow the
// BRD must cover. Not a use-case template — short prose, optionally
// keyed to an actor.
type UserScenario struct {
	ID        string `yaml:"id"`
	Actor     string `yaml:"actor,omitempty"`
	Narrative string `yaml:"narrative"`

	// VerifiedBy lists the units that demonstrate this scenario (v0.8 α):
	// each entry is either a verifier test FQN (the test carries a
	// `@verifies brd.<id>:US-N` tag) or a declared entry-point id. A
	// scenario with no VerifiedBy is BRD_SCENARIO_UNMAPPED (info); one
	// whose entries name nothing on the graph is BRD_SCENARIO_UNVERIFIED
	// (warning). When at least one entry resolves to an entry point the
	// scenario counts toward journey coverage.
	VerifiedBy []string `yaml:"verified_by,omitempty"`

	// Given, When and Then are the scenario's steps (Gherkin); Blocks are
	// the block-graph blocks that realise it.
	Given  []string `yaml:"given,omitempty"`
	When   []string `yaml:"when,omitempty"`
	Then   []string `yaml:"then,omitempty"`
	Blocks []string `yaml:"blocks,omitempty"`
}

// BRDFlag is a switch or value a BRD depends on, in plain words.
type BRDFlag struct {
	ID      string `yaml:"id"`
	Meaning string `yaml:"meaning,omitempty"`
}

// BRDCriterion is one measurable success criterion. It may reference a
// feature invariant via `maps_to_invariant: feature.id:INV-N`, or several
// as a list, which the graph builder uses to build the BRD → Invariant
// bridge. A criterion mapped to several invariants holds only when all of
// them do.
type BRDCriterion struct {
	ID              string        `yaml:"id"`
	Statement       string        `yaml:"statement"`
	MapsToInvariant InvariantRefs `yaml:"maps_to_invariant,omitempty"`

	// Tier is the pinned criticality (v0.8.1): 1 default; 2 for
	// auth/permissions/data-loss/cross-tenant; 3 for money movement and
	// regulatory. Proposed mechanically, pinned by a human, changed only
	// through the CR flow. Tier 2+ requires mutation evidence
	// (MUTATION_REQUIRED_TIER) and is never delegable in CR decisions.
	Tier int `yaml:"tier,omitempty"`

	// DirectWire (v0.8.1) marks a criterion that already IS an
	// enforceable single sentence: enforcers and verifiers attach
	// directly here instead of minting a 1:1 restatement invariant.
	// Checks treat the criterion as its own invariant.
	DirectWire bool     `yaml:"direct_wire,omitempty"`
	EnforcedBy []string `yaml:"enforced_by,omitempty"`
	VerifiedBy []string `yaml:"verified_by,omitempty"`
}

// InvariantRefs is a criterion's `maps_to_invariant`: one
// `feature.id:INV-N` ref or a list of them. On disk a single ref stays a
// plain string, so files written before lists were allowed keep their
// shape.
type InvariantRefs []string

// UnmarshalYAML accepts a string or a list of strings.
func (r *InvariantRefs) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*r = nil
		if s := strings.TrimSpace(n.Value); s != "" {
			*r = InvariantRefs{s}
		}
		return nil
	}
	var refs []string
	if err := n.Decode(&refs); err != nil {
		return fmt.Errorf("maps_to_invariant: want a ref or a list of refs: %w", err)
	}
	*r = nil
	for _, s := range refs {
		if s = strings.TrimSpace(s); s != "" {
			*r = append(*r, s)
		}
	}
	return nil
}

// MarshalYAML writes a single ref as a string and several as a list.
func (r InvariantRefs) MarshalYAML() (interface{}, error) {
	if len(r) == 1 {
		return r[0], nil
	}
	return []string(r), nil
}

// UnmarshalJSON accepts a string too, the form graphs extracted before
// lists were allowed carry.
func (r *InvariantRefs) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*r = nil
		if s = strings.TrimSpace(s); s != "" {
			*r = InvariantRefs{s}
		}
		return nil
	}
	var refs []string
	if err := json.Unmarshal(b, &refs); err != nil {
		return err
	}
	*r = refs
	return nil
}

// String joins the refs for messages: "a:INV-1, b:INV-2".
func (r InvariantRefs) String() string { return strings.Join(r, ", ") }

// Has reports whether ref is one of the refs.
func (r InvariantRefs) Has(ref string) bool {
	for _, s := range r {
		if s == ref {
			return true
		}
	}
	return false
}

// EffectiveTier returns the pinned tier, defaulting to 1.
func (c BRDCriterion) EffectiveTier() int {
	if c.Tier >= 1 && c.Tier <= 3 {
		return c.Tier
	}
	return 1
}

// BRDConstraintKind narrows the constraint taxonomy enough that the UI
// can icon them and the validator can spot LLM-fabricated regulatory
// claims (any non-human constraint is high-risk to invent).
type BRDConstraintKind string

const (
	ConstraintRegulatory BRDConstraintKind = "regulatory"
	ConstraintLegal      BRDConstraintKind = "legal"
	ConstraintFinancial  BRDConstraintKind = "financial"
	ConstraintTechnical  BRDConstraintKind = "technical"
	ConstraintTimeline   BRDConstraintKind = "timeline"
)

// BRDConstraint is one external rule the implementation must respect.
type BRDConstraint struct {
	Kind BRDConstraintKind `yaml:"kind"`
	Ref  string            `yaml:"ref,omitempty"`
	Note string            `yaml:"note,omitempty"`
}

// BRDApproval freezes who signed off on which version. ApprovedVersion
// is checked against BRD.Version on extract — a drifted version raises
// BRD_DRIFT so downstream features can flag a stale parent.
type BRDApproval struct {
	ApprovedBy      string `yaml:"approved_by"`
	ApprovedAt      string `yaml:"approved_at"` // ISO datetime
	ApprovedVersion int    `yaml:"approved_version"`

	// Mandate is set when a council approved under a human-pinned
	// brd_approve mandate; ApprovedBy is then "council:<mandate>" and
	// DelegatedBy names the human the council acted for.
	Mandate     string              `yaml:"mandate,omitempty"`
	DelegatedBy string              `yaml:"delegated_by,omitempty"`
	Decision    *BRDCouncilDecision `yaml:"decision,omitempty"`
	// HumanReview tracks the delegating human's read of a council
	// decision: pending until `lattice brd ack`.
	HumanReview HumanReviewState `yaml:"human_review,omitempty"`
	ReviewedBy  string           `yaml:"reviewed_by,omitempty"`
	ReviewedAt  string           `yaml:"reviewed_at,omitempty"`
}

// HumanReviewState is where a council decision stands with its human.
type HumanReviewState string

const (
	HumanReviewPending      HumanReviewState = "pending"
	HumanReviewAcknowledged HumanReviewState = "acknowledged"
)

// BRDCouncilDecision is what a council decided and why, kept on the BRD so
// the approval carries its own explanation.
type BRDCouncilDecision struct {
	Outcome  string           `yaml:"outcome"` // approved
	Summary  string           `yaml:"summary"`
	Why      string           `yaml:"why,omitempty"`
	Evidence string           `yaml:"evidence,omitempty"` // computed by the engine, e.g. "criteria 4/4 demonstrated"
	Votes    []BRDCouncilVote `yaml:"votes"`
}

// BRDCouncilVote is one seat's review of one BRD version.
type BRDCouncilVote struct {
	Seat    string `yaml:"seat"`
	Actor   string `yaml:"actor"`
	Verdict string `yaml:"verdict"` // approve | changes
	Reason  string `yaml:"reason"`
	At      string `yaml:"at,omitempty"`
}

// Council vote verdicts.
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
)

// BRDProvenance records how the BRD came to exist. GeneratedAt is set
// only for llm_from_code; the model field captures which LLM produced
// it so audits can correlate with provider runs.
type BRDProvenance struct {
	Source      BRDProvenanceSource `yaml:"source,omitempty"`
	GeneratedAt string              `yaml:"generated_at,omitempty"`
	Model       string              `yaml:"model,omitempty"`
	// AuthoredBy is the actor that wrote this version (--actor of the
	// command that created or regenerated it). A council seat may not be
	// held by the author of the version it reviews.
	AuthoredBy string `yaml:"authored_by,omitempty"`
}

// IsAgentActor reports whether an actor names an agent: an "agent"
// prefix, as in "agent:refunds" (case-insensitive). The same test gates
// the human-only commands (deciding a CR, clearing a flag).
func IsAgentActor(actor string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(actor)), "agent")
}

// MachineWritten reports whether this version of the BRD was written by
// a machine — regenerated from code by an LLM, or authored by an agent —
// and so needs a human (or a council) to review it before it is the
// source of truth.
func (b BRD) MachineWritten() bool {
	return b.Provenance.Source == BRDSourceLLMFromCode || IsAgentActor(b.Provenance.AuthoredBy)
}
