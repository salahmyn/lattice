package schema

// Severity classifies how serious a violation is.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Validation rule codes. Each corresponds to one rule in design section 20.
const (
	// Manifest integrity
	CodeManifestSchema          = "MANIFEST_SCHEMA"
	CodeManifestIDDuplicate     = "MANIFEST_ID_DUPLICATE"
	CodeManifestIDFormat        = "MANIFEST_ID_FORMAT"
	CodeManifestVersionDecrease = "MANIFEST_VERSION_DECREASE"
	CodeManifestVersionSkipped  = "MANIFEST_VERSION_SKIPPED"

	// Dependency integrity
	CodeDependsOnMissing        = "DEPENDS_ON_MISSING"
	CodeDependsOnCycle          = "DEPENDS_ON_CYCLE"
	CodeSubfeatureParentMissing = "SUBFEATURE_PARENT_MISSING"
	CodeSubfeatureDepthExceeded = "SUBFEATURE_DEPTH_EXCEEDED"

	// Annotation integrity
	CodeOrphanAnnotationFeature    = "ORPHAN_ANNOTATION_FEATURE"
	CodeOrphanAnnotationCapability = "ORPHAN_ANNOTATION_CAPABILITY"
	CodeOrphanAnnotationInvariant  = "ORPHAN_ANNOTATION_INVARIANT"
	CodeOrphanAnnotationRole       = "ORPHAN_ANNOTATION_ROLE"
	CodeAnnotationArgNotLiteral    = "ANNOTATION_ARG_NOT_LITERAL"
	CodeModuleAnnotationConflict   = "MODULE_ANNOTATION_CONFLICT"

	// Verification integrity
	CodeUnverifiedInvariant         = "UNVERIFIED_INVARIANT"
	CodeUnenforcedInvariant         = "UNENFORCED_INVARIANT"
	CodeUnimplementedCapability     = "UNIMPLEMENTED_CAPABILITY"
	CodeMutationScoreBelowThreshold = "MUTATION_SCORE_BELOW_THRESHOLD"
	CodeStructuralCheckFailed       = "STRUCTURAL_CHECK_FAILED"
	CodeStructuralCheckMissing      = "STRUCTURAL_CHECK_MISSING"
	CodeStructuralCheckTimedOut     = "STRUCTURAL_CHECK_TIMED_OUT"
	CodeSuppressionWithoutReason    = "SUPPRESSION_WITHOUT_REASON"

	// Surface integrity
	CodeSurfaceUndeclared    = "SURFACE_UNDECLARED"
	CodeSurfaceUnimplemented = "SURFACE_UNIMPLEMENTED"

	// Error-contract integrity
	CodeErrorUndeclared    = "ERROR_UNDECLARED"
	CodeErrorUnimplemented = "ERROR_UNIMPLEMENTED"

	// Initiative and task integrity
	CodeInitiativeSchema                   = "INITIATIVE_SCHEMA"
	CodeTaskSchema                         = "TASK_SCHEMA"
	CodeTaskReferencesMissingInitiative    = "TASK_REFERENCES_MISSING_INITIATIVE"
	CodeTaskReferencesMissingStream        = "TASK_REFERENCES_MISSING_STREAM"
	CodeTaskDependencyCycle                = "TASK_DEPENDENCY_CYCLE"
	CodeTaskDependsOnMissingContract       = "TASK_DEPENDS_ON_MISSING_CONTRACT"
	CodeInitiativeMigrationConsumerMissing = "INITIATIVE_MIGRATION_CONSUMER_MISSING"

	// Cross-cutting
	CodeDependsOnFeatureNotDeclared = "DEPENDS_ON_FEATURE_NOT_DECLARED"
	CodeAdapterParseError           = "ADAPTER_PARSE_ERROR"
	CodeSCIPIndexStale              = "SCIP_INDEX_STALE"
)

// Location points at a place in a file.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

// NextAction tells a caller what to do about a violation. Agents branch on
// Kind, never on prose. Fields beyond Kind are populated per-kind.
type NextAction struct {
	Kind string `json:"kind"`

	// kind=add_annotation
	Annotation     string   `json:"annotation,omitempty"`
	Ref            string   `json:"ref,omitempty"`
	TargetKind     string   `json:"target_kind,omitempty"`
	SuggestedFiles []string `json:"suggested_files,omitempty"`

	// kind=edit_manifest / kind=run_command and generic guidance
	Field   string   `json:"field,omitempty"`
	Command []string `json:"command,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

// Violation is one structured finding from the validator.
type Violation struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`

	// Context (optional, populated when relevant).
	FeatureID    string `json:"feature_id,omitempty"`
	InvariantID  string `json:"invariant_id,omitempty"`
	CapabilityID string `json:"capability_id,omitempty"`
	InitiativeID string `json:"initiative_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`

	Location   *Location   `json:"location,omitempty"`
	NextAction *NextAction `json:"next_action,omitempty"`
}

// IsError reports whether the violation should fail validation.
func (v Violation) IsError() bool { return v.Severity == SeverityError }
