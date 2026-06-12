package schema

// Revision is a change request (CR) against grounded business intent —
// the only legitimate path for changing an approved BRD criterion,
// scope, tier, or decision. Grounded artifacts are never edited in
// place; a Revision captures the proposal, its computed impact, the
// human decision, and the demotions/work it spawned.
//
// Lives at lattice/revisions/<id>.yaml. Field order on disk follows
// struct order — same canonical-YAML contract as Manifest and BRD.
type Revision struct {
	// --- Required ---
	ID     string         `yaml:"id"` // CR-<n>, global, append-only
	Status RevisionStatus `yaml:"status"`

	// Targets are the artifact units the proposal touches: BRD ids
	// (brd.x.y) or criterion refs (brd.x.y/SC-1).
	Targets []string `yaml:"targets"`

	// PreviousText is copied verbatim at propose time so the decision
	// gate sees an exact diff even after the target moves on.
	PreviousText string `yaml:"previous_text,omitempty"`
	ProposedText string `yaml:"proposed_text"`

	// --- Filled at price (CR-2) ---
	// Class is the strictest classification of the change. Gate
	// consequences: wording is mandate-delegable; widening spawns work
	// items; narrowing is never delegable and spawns retirement items;
	// contradiction is blocked until the conflicting Decision is
	// explicitly superseded.
	Class  RevisionClass  `yaml:"class,omitempty"`
	Impact RevisionImpact `yaml:"impact,omitempty"`

	// --- Filled at decide (CR-3) ---
	Decision *RevisionDecision `yaml:"decision,omitempty"`

	// SupersedesDecision names the Decision record a contradiction-class
	// approval explicitly retires. Required before a contradiction may
	// be approved; the old Decision is never deleted.
	SupersedesDecision string `yaml:"supersedes_decision,omitempty"`

	// --- Filled at propagate (CR-4) ---
	// Demotions lists criteria dropped to flagged on approval — code
	// proven against the old requirement is unproven against the new
	// one; stale green never rides.
	Demotions []string `yaml:"demotions,omitempty"`
	// WorkItems (widening) and RetirementItems (narrowing) are the
	// spawned follow-ups. Deleting a test is legal ONLY against a
	// retirement item — that is what distinguishes legitimate descoping
	// from gaming the suite.
	WorkItems       []string `yaml:"work_items,omitempty"`
	RetirementItems []string `yaml:"retirement_items,omitempty"`

	// --- Auto-populated (never hand-edited) ---
	SourcePath string `yaml:"-"`
}

// RevisionStatus is the CR lifecycle: proposed → approved|rejected →
// reconverged (every demoted criterion re-demonstrated).
type RevisionStatus string

const (
	RevisionProposed    RevisionStatus = "proposed"
	RevisionApproved    RevisionStatus = "approved"
	RevisionRejected    RevisionStatus = "rejected"
	RevisionReconverged RevisionStatus = "reconverged"
)

// ValidRevisionStatuses lists every legal revision status.
var ValidRevisionStatuses = []RevisionStatus{
	RevisionProposed, RevisionApproved, RevisionRejected, RevisionReconverged,
}

// RevisionClass classifies what kind of change a CR is. When a change
// mixes classes, the strictest wins (contradiction > narrowing >
// widening > wording).
type RevisionClass string

const (
	RevisionWording       RevisionClass = "wording"       // meaning-preserving; delegable under mandate
	RevisionWidening      RevisionClass = "widening"      // new scope; spawns work items
	RevisionNarrowing     RevisionClass = "narrowing"     // scope removed; never delegable
	RevisionContradiction RevisionClass = "contradiction" // conflicts with a grounded Decision
)

// ValidRevisionClasses lists every legal revision class.
var ValidRevisionClasses = []RevisionClass{
	RevisionWording, RevisionWidening, RevisionNarrowing, RevisionContradiction,
}

// RevisionImpact is the computed forward blast radius of the proposal:
// criterion → invariants → enforcers → verifiers → scenarios → entry
// points. Filled mechanically at price time; the decision gate prices
// the change before committing to it.
type RevisionImpact struct {
	AffectedInvariants  []string `yaml:"affected_invariants,omitempty"`
	AffectedSymbols     []string `yaml:"affected_symbols,omitempty"`
	AffectedTests       []string `yaml:"affected_tests,omitempty"`
	AffectedScenarios   []string `yaml:"affected_scenarios,omitempty"`
	AffectedEntryPoints []string `yaml:"affected_entry_points,omitempty"`

	// MaxTier is the highest tier in the radius. Tier 2+ requires the
	// human decision to acknowledge the tier explicitly; mandates never
	// cover it.
	MaxTier int `yaml:"max_tier,omitempty"`

	// InFlightConflicts lists active leases whose scope intersects the
	// radius — the decision must hold the CR or attach it to that work.
	InFlightConflicts []string `yaml:"in_flight_conflicts,omitempty"`
}

// RevisionDecision records the CR-3 human gate outcome.
type RevisionDecision struct {
	Outcome string `yaml:"outcome"` // approved | rejected
	By      string `yaml:"by"`
	At      string `yaml:"at"` // ISO datetime
	// Mandate names the pre-authorized delegation used, if any. Only
	// wording-class CRs within the mandate's tier ceiling may carry one.
	Mandate string `yaml:"mandate,omitempty"`
	Note    string `yaml:"note,omitempty"`
}
