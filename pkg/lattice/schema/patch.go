package schema

// TargetKind identifies what artifact a patch edits.
type TargetKind string

const (
	TargetManifest   TargetKind = "manifest"
	TargetInitiative TargetKind = "initiative"
	TargetTask       TargetKind = "task"
)

// Operation op codes, grouped by target kind (design section 24).
const (
	// Manifest operations
	OpAddCapability         = "AddCapability"
	OpModifyCapability      = "ModifyCapability"
	OpRemoveCapability      = "RemoveCapability"
	OpAddInvariant          = "AddInvariant"
	OpModifyInvariant       = "ModifyInvariant"
	OpRemoveInvariant       = "RemoveInvariant"
	OpAddDependency         = "AddDependency"
	OpRemoveDependency      = "RemoveDependency"
	OpAddSurface            = "AddSurface"
	OpModifySurface         = "ModifySurface"
	OpRemoveSurface         = "RemoveSurface"
	OpAddDecision           = "AddDecision"
	OpAddRole               = "AddRole"
	OpModifyRole            = "ModifyRole"
	OpRemoveRole            = "RemoveRole"
	OpAddStructuralCheck    = "AddStructuralCheck"
	OpRemoveStructuralCheck = "RemoveStructuralCheck"
	OpSetStatus             = "SetStatus"
	OpSetMigration          = "SetMigration"

	// Initiative operations
	OpAddStream              = "AddStream"
	OpRemoveStream           = "RemoveStream"
	OpAddSuccessCriterion    = "AddSuccessCriterion"
	OpModifySuccessCriterion = "ModifySuccessCriterion"
	OpAddContract            = "AddContract"
	OpLockContract           = "LockContract"
	OpSetInitiativeStatus    = "SetInitiativeStatus"

	// Task operations
	OpCreateTask           = "CreateTask"
	OpModifyTask           = "ModifyTask"
	OpDeleteTask           = "DeleteTask"
	OpAddTaskDependency    = "AddTaskDependency"
	OpRemoveTaskDependency = "RemoveTaskDependency"
	OpSetTaskOwner         = "SetTaskOwner"
	OpSetTaskStatus        = "SetTaskStatus" // manual override only

	// Shared
	OpSetField = "SetField"
)

// Operation is one typed edit. Args carries op-specific payload, decoded by
// the patch engine according to Op.
type Operation struct {
	Op   string                 `json:"op" yaml:"op"`
	Args map[string]interface{} `json:"args,omitempty" yaml:"args,omitempty"`
}

// Patch is a typed, atomic set of operations editing one artifact.
type Patch struct {
	TargetKind  TargetKind  `json:"target_kind" yaml:"target_kind"`
	TargetID    string      `json:"target_id" yaml:"target_id"`
	BaseVersion int         `json:"base_version" yaml:"base_version"`
	Operations  []Operation `json:"operations" yaml:"operations"`
}

// PatchPreview is the result of a non-writing patch evaluation.
type PatchPreview struct {
	Diff                 string      `json:"diff"`
	IntroducedViolations []Violation `json:"introduced_violations,omitempty"`
	ResolvedViolations   []Violation `json:"resolved_violations,omitempty"`
	ConflictFindings     []string    `json:"conflict_findings,omitempty"`
}

// IsAcceptable reports whether applying the patch would introduce no new
// error-severity violations.
func (p PatchPreview) IsAcceptable() bool {
	for _, v := range p.IntroducedViolations {
		if v.IsError() {
			return false
		}
	}
	return true
}

// PatchResult is the outcome of applying a patch.
type PatchResult struct {
	Applied    bool        `json:"applied"`
	NewVersion int         `json:"new_version,omitempty"`
	Diff       string      `json:"diff,omitempty"`
	RolledBack bool        `json:"rolled_back,omitempty"`
	Violations []Violation `json:"violations,omitempty"`
	Message    string      `json:"message,omitempty"`
}
