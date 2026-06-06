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

	// Entry-point integrity (v0.3.0)
	CodeUnclassifiedEntryPoint = "UNCLASSIFIED_ENTRY_POINT" // EP reaches zero features
	CodeHandlerMissing         = "HANDLER_MISSING"          // EP handler symbol not in IR
	CodeDuplicateTrigger       = "DUPLICATE_TRIGGER"        // two EPs share (kind, trigger)
	CodePhantomFlow            = "PHANTOM_FLOW"             // flow step names a non-existent feature

	// BRD integrity (v0.5.0)
	//
	// All BRD rules are deliberately conservative — adoption is opt-in.
	// FEATURE_NO_BRD is warning (never error); BRD_UNREFERENCED and
	// BRD_DRIFT are info-level because a freshly-drafted BRD and a
	// version-skew during proposal review are both legitimate states.
	CodeBRDSchema           = "BRD_SCHEMA"            // parse / required-field error
	CodeBRDIDFormat         = "BRD_ID_FORMAT"         // id must start with brd. and use the manifest-id grammar
	CodeBRDIDDuplicate      = "BRD_ID_DUPLICATE"      // two BRDs share an id
	CodeBRDPhantomFeature   = "BRD_PHANTOM_FEATURE"   // implements_via names a missing feature
	CodeBRDUnreferenced     = "BRD_UNREFERENCED"      // BRD has no features yet (info)
	CodeFeatureNoBRD        = "FEATURE_NO_BRD"        // feature lacks an upstream BRD (warning)
	CodeBRDDrift            = "BRD_DRIFT"             // BRD.version != approval.approved_version (info)
	CodeBRDUnapprovedLLM    = "BRD_UNAPPROVED_LLM"    // llm_from_code draft needs human sign-off
	CodeFeatureBRDMissing   = "FEATURE_BRD_MISSING"   // feature.implements_brd names a missing BRD

	// RTM — Requirements Traceability Matrix (v0.6)
	//
	// These rules fire at the BRD-criterion level so the business
	// consequence (SC-1 has no backing verification) shows up alongside
	// the technical one (INV-1 has no enforcer, already covered by
	// UNENFORCED_INVARIANT). Operators triaging at the BRD layer see
	// what they need without drilling into per-feature rules.
	CodeBRDCriterionPhantomInvariant = "BRD_CRITERION_PHANTOM_INVARIANT" // SC.maps_to_invariant misses
	CodeBRDCriterionUnverified       = "BRD_CRITERION_UNVERIFIED"        // SC mapped but unenforced/unverified/partial
	CodeBRDCriterionUnmapped         = "BRD_CRITERION_UNMAPPED"          // SC has no maps_to_invariant

	// AMA enforcement (v0.7)
	//
	// All five default to warning. CROSS_FEATURE_IMPORT and
	// MIXED_COMMAND_QUERY escalate to error / surface respectively
	// when `architecture.ama_mode: true` is set. The other three
	// stay warning in both modes — they're code-hygiene signals,
	// not contract violations.
	CodeCrossFeatureImport  = "CROSS_FEATURE_IMPORT"   // feature A's symbol imports feature B internals
	CodeFeatureNotColocated = "FEATURE_NOT_COLOCATED"  // feature's symbols span multiple top-level dirs
	CodeFileLineCap         = "FILE_LINE_CAP"          // source file longer than architecture.file_line_cap
	CodeMethodLineCap       = "METHOD_LINE_CAP"        // method/function longer than architecture.method_line_cap
	CodeMixedCommandQuery   = "MIXED_COMMAND_QUERY"    // capability is `mixed` (or symbol mixes intents)
	CodeFeatureSpecTooLarge = "FEATURE_SPEC_TOO_LARGE" // .ai-spec.md render > 500 words → decomposition signal
)

// Info is the lowest violation severity — surfaced in the dashboard but
// not fail-blocking. Used for the v0.5 BRD rules where a non-canonical
// state is informative rather than wrong.
const SeverityInfo Severity = "info"

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
