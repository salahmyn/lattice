package schema

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
	BusinessGoals   []string         `yaml:"business_goals,omitempty"`
	Stakeholders    BRDStakeholders  `yaml:"stakeholders,omitempty"`
	UserScenarios   []UserScenario   `yaml:"user_scenarios,omitempty"`
	SuccessCriteria []BRDCriterion   `yaml:"success_criteria,omitempty"`
	Constraints     []BRDConstraint  `yaml:"constraints,omitempty"`
	OutOfScope      []string         `yaml:"out_of_scope,omitempty"`

	// ImplementsVia is the forward link to the Feature axis. Empty for a
	// freshly-drafted BRD; populated as features land. The validator
	// treats a non-empty entry that names a missing feature as an error
	// (BRD_PHANTOM_FEATURE).
	ImplementsVia []string `yaml:"implements_via,omitempty"`

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
}

// BRDCriterion is one measurable success criterion. It may reference a
// feature invariant via `maps_to_invariant: feature.id:INV-N`, which the
// graph builder uses to build the BRD → Invariant bridge.
type BRDCriterion struct {
	ID               string `yaml:"id"`
	Statement        string `yaml:"statement"`
	MapsToInvariant  string `yaml:"maps_to_invariant,omitempty"`
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
	ApprovedAt      string `yaml:"approved_at"`     // ISO datetime
	ApprovedVersion int    `yaml:"approved_version"`
}

// BRDProvenance records how the BRD came to exist. GeneratedAt is set
// only for llm_from_code; the model field captures which LLM produced
// it so audits can correlate with provider runs.
type BRDProvenance struct {
	Source      BRDProvenanceSource `yaml:"source,omitempty"`
	GeneratedAt string              `yaml:"generated_at,omitempty"`
	Model       string              `yaml:"model,omitempty"`
}
