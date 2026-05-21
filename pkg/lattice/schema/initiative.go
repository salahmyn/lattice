package schema

// InitiativeStatus is the lifecycle state of an initiative.
type InitiativeStatus string

const (
	InitiativeProposed   InitiativeStatus = "proposed"
	InitiativeAccepted   InitiativeStatus = "accepted"
	InitiativeInProgress InitiativeStatus = "in_progress"
	InitiativePaused     InitiativeStatus = "paused"
	InitiativeComplete   InitiativeStatus = "complete"
	InitiativeCancelled  InitiativeStatus = "cancelled"
)

// Initiative is a coordinated piece of work proposing manifest changes,
// decomposed into streams and tasks.
//
// Lives at work/initiatives/<id>/initiative.yaml.
type Initiative struct {
	ID     string           `yaml:"id"`
	Type   string           `yaml:"type"` // always "initiative"
	Status InitiativeStatus `yaml:"status"`

	Created          string `yaml:"created"`                     // ISO datetime
	TargetCompletion string `yaml:"target_completion,omitempty"` // ISO date
	ActualCompletion string `yaml:"actual_completion,omitempty"` // ISO date, auto

	ProposesChangesTo []ProposedChange `yaml:"proposes_changes_to,omitempty"`

	Motivation string `yaml:"motivation"`

	SuccessCriteria []SuccessCriterion `yaml:"success_criteria,omitempty"`
	Contracts       []Contract         `yaml:"contracts,omitempty"`
	Streams         []Stream           `yaml:"streams,omitempty"`
	Migration       *Migration         `yaml:"migration,omitempty"`

	// SourcePath is the repo-relative path the initiative was loaded from.
	SourcePath string `yaml:"-"`
}

// ProposedChange records a manifest version bump an initiative will make.
type ProposedChange struct {
	Manifest    string `yaml:"manifest"`
	FromVersion int    `yaml:"from_version"`
	ToVersion   int    `yaml:"to_version"`
}

// SuccessCriterion is a measurable goal for an initiative.
type SuccessCriterion struct {
	ID        string `yaml:"id"`
	Statement string `yaml:"statement"`
	Measure   string `yaml:"measure"`
	Target    string `yaml:"target"`
	Actual    string `yaml:"actual,omitempty"` // auto-populated
}

// Contract is a locked schema or spec that tasks depend on.
type Contract struct {
	Path        string `yaml:"path"`
	Description string `yaml:"description"`
	LockedAt    string `yaml:"locked_at,omitempty"` // ISO datetime, set when frozen
}

// Stream is a parallel work track within an initiative.
type Stream struct {
	ID          string `yaml:"id"`
	Owner       string `yaml:"owner"`
	Description string `yaml:"description"`
}
