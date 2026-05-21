package schema

// TaskStatus is the workflow state of a task.
type TaskStatus string

const (
	TaskNotStarted TaskStatus = "not_started"
	TaskInProgress TaskStatus = "in_progress"
	TaskBlocked    TaskStatus = "blocked"
	TaskInReview   TaskStatus = "in_review"
	TaskDone       TaskStatus = "done"
	TaskCancelled  TaskStatus = "cancelled"
)

// StatusSource records whether a task's status is derived from git or set
// manually.
type StatusSource string

const (
	StatusDerived StatusSource = "derived"
	StatusManual  StatusSource = "manual"
)

// Task is an atomic unit of work within an initiative.
//
// Lives at work/initiatives/<id>/tasks/<task-id>.yaml.
type Task struct {
	ID         string `yaml:"id"`
	Title      string `yaml:"title"`
	Initiative string `yaml:"initiative"`
	Stream     string `yaml:"stream"`

	Status       TaskStatus   `yaml:"status"`
	StatusSource StatusSource `yaml:"status_source"`

	Owner       string      `yaml:"owner,omitempty"`
	Suitability Suitability `yaml:"suitability,omitempty"`

	Produces  []TaskProduct `yaml:"produces,omitempty"`
	DependsOn []TaskDep     `yaml:"depends_on,omitempty"`
	Unblocks  []string      `yaml:"unblocks,omitempty"` // computed inverse

	Verifies []string `yaml:"verifies,omitempty"`
	Estimate string   `yaml:"estimate,omitempty"`

	Links TaskLinks `yaml:"links,omitempty"`

	// SourcePath is the repo-relative path the task was loaded from.
	SourcePath string `yaml:"-"`
}

// Suitability records who can perform a task.
type Suitability struct {
	AgentAutonomous bool `yaml:"agent_autonomous"`
	AgentPair       bool `yaml:"agent_pair"`
	HumanOnly       bool `yaml:"human_only"`
}

// ProductKind enumerates what a task produces.
type ProductKind string

const (
	ProductCode          ProductKind = "code"
	ProductSchema        ProductKind = "schema"
	ProductTest          ProductKind = "test"
	ProductDocumentation ProductKind = "documentation"
	ProductDecision      ProductKind = "decision"
	ProductInfra         ProductKind = "infra"
)

// TaskProduct is an artifact a task is expected to create.
type TaskProduct struct {
	Kind             ProductKind `yaml:"kind"`
	Description      string      `yaml:"description"`
	AnnotationTarget string      `yaml:"annotation_target,omitempty"`
}

// TaskDep is a dependency edge: exactly one of Task or Contract is set.
type TaskDep struct {
	Task     string `yaml:"task,omitempty"`
	Contract string `yaml:"contract,omitempty"`
}

// TaskLinks holds git and decision references for a task.
type TaskLinks struct {
	PR               string   `yaml:"pr,omitempty"`     // auto
	Branch           string   `yaml:"branch,omitempty"` // auto
	RelatedDecisions []string `yaml:"related_decisions,omitempty"`
}
