package schema

// @module-feature schema

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
	Title  string           `yaml:"title,omitempty"`
	Author string           `yaml:"author,omitempty"` // who wrote a store-record initiative

	// Pins are the commits a proposal was analysed at; Map says how it is drawn
	// over the block graph. Both are set on initiatives kept as store records.
	Pins []Pin          `yaml:"pins,omitempty"`
	Map  *InitiativeMap `yaml:"map,omitempty"`

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

// Pin is a repo commit an initiative was analysed at.
type Pin struct {
	Role    string `yaml:"role" json:"role"`
	Repo    string `yaml:"repo,omitempty" json:"repo,omitempty"`
	Ref     string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Commit  string `yaml:"commit,omitempty" json:"commit,omitempty"`
	Clone   string `yaml:"clone,omitempty" json:"clone,omitempty"`
	Package string `yaml:"package,omitempty" json:"package,omitempty"`
	Note    string `yaml:"note,omitempty" json:"note,omitempty"`
}

// PinCheck is how a pin stands against its local clone.
type PinCheck struct {
	State      string `json:"state"`
	Moved      int    `json:"moved,omitempty"` // commits the ref has past the pin
	CommitDate string `json:"commit_date,omitempty"`
	Tip        string `json:"tip,omitempty"`
	Pushed     bool   `json:"pushed,omitempty"` // a remote branch contains the pin
}

// InitiativeMap says how a proposal is drawn over the block graph.
type InitiativeMap struct {
	System    MapSystem     `yaml:"system"`
	HostBlock string        `yaml:"host_block,omitempty"` // the block that would mount the proposal
	Flag      string        `yaml:"flag,omitempty"`
	Decision  string        `yaml:"decision,omitempty"`
	Blocking  []string      `yaml:"blocking,omitempty"` // gap codes that block the initiative
	Externals []MapExternal `yaml:"externals,omitempty"`
}

// MapSystem titles the proposal's system block.
type MapSystem struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description,omitempty"`
}

// MapExternal is an edge from the proposal to an existing block.
type MapExternal struct {
	To    string `yaml:"to"`
	Kind  string `yaml:"kind"`
	Label string `yaml:"label,omitempty"`
}
