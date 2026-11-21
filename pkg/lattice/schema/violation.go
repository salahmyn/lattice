package schema

// @module-feature schema

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
	CodeAnnotationOrphaned         = "ANNOTATION_ORPHANED"
	CodeUnattributedModule         = "UNATTRIBUTED_MODULE"
	CodeAllowlistStale             = "ALLOWLIST_STALE"
	CodeAllowlistSchema            = "ALLOWLIST_SCHEMA"
	CodeValidatePolicy             = "VALIDATE_POLICY"
	CodeResultAmbiguous            = "RESULT_AMBIGUOUS"
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
	// A runtime root (a serve handler, a cron job, a detected route) that
	// cannot become an entry point: its handler is not in the graph, or its
	// key is computed (info).
	CodeRuntimeRootUnproposed = "RUNTIME_ROOT_UNPROPOSED"

	// The http join (info, always on). A literal request that no route
	// extracted from code serves, and, one per file, the requests whose URL
	// or method is computed.
	CodeHTTPCallUnserved = "HTTP_CALL_UNSERVED"
	CodeHTTPCallDynamic  = "HTTP_CALL_DYNAMIC"

	// The channel join (info, always on). A literal publish that no consumer
	// of the workspace names, and a consumer whose channel no publisher names.
	CodeChannelNoConsumer  = "CHANNEL_NO_CONSUMER"
	CodeChannelNoPublisher = "CHANNEL_NO_PUBLISHER"
	// One per file: the channel ends whose name is computed, so no edge.
	CodeChannelDynamic = "CHANNEL_DYNAMIC"

	// BRD integrity (v0.5.0)
	//
	// All BRD rules are deliberately conservative — adoption is opt-in.
	// FEATURE_NO_BRD is warning (never error); BRD_UNREFERENCED and
	// BRD_DRIFT are info-level because a freshly-drafted BRD and a
	// version-skew during proposal review are both legitimate states.
	CodeBRDSchema         = "BRD_SCHEMA"          // parse / required-field error
	CodeBRDIDFormat       = "BRD_ID_FORMAT"       // id must start with brd. and use the manifest-id grammar
	CodeBRDIDDuplicate    = "BRD_ID_DUPLICATE"    // two BRDs share an id
	CodeBRDPhantomFeature = "BRD_PHANTOM_FEATURE" // implements_via names a missing feature
	CodeBRDUnreferenced   = "BRD_UNREFERENCED"    // BRD has no features yet (info)
	CodeFeatureNoBRD      = "FEATURE_NO_BRD"      // feature lacks an upstream BRD (warning)
	CodeBRDDrift          = "BRD_DRIFT"           // BRD.version != approval.approved_version (info)
	CodeBRDUnapprovedLLM  = "BRD_UNAPPROVED_LLM"  // llm_from_code draft needs human sign-off
	CodeFeatureBRDMissing = "FEATURE_BRD_MISSING" // feature.implements_brd names a missing BRD
	CodeBRDLinkOneSided   = "BRD_LINK_ONE_SIDED"  // implements_via and implements_brd disagree (warning)
	// A source with an unknown kind, an empty ref, or a url that is not an
	// absolute http(s) URL (error).
	CodeBRDSourceInvalid = "BRD_SOURCE_INVALID"
	// A source's checked_by and checked_at are not set together, or
	// checked_at is not a date or lies in the future (error).
	CodeBRDSourceCheckInvalid = "BRD_SOURCE_CHECK_INVALID"

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

	// v0.8 — agent-steerable knowledge graph.
	//
	// α/β close demonstration; δ closes meaning fidelity; the §5/§6 rules
	// keep a multi-agent fleet honest. All default to info/warning so a
	// v0.7 workspace upgrades without its validate exit code changing.

	// α — executable scenario verifiers (peer to BRD_CRITERION_*).
	CodeBRDScenarioUnverified = "BRD_SCENARIO_UNVERIFIED" // scenario verified_by names nothing on the graph
	CodeBRDScenarioUnmapped   = "BRD_SCENARIO_UNMAPPED"   // scenario declares no verified_by

	// β — entry-point journey coverage.
	CodeFeatureUnreached     = "FEATURE_UNREACHED"      // production feature on no entry-point flow
	CodeScenarioNoEntryPoint = "SCENARIO_NO_ENTRYPOINT" // scenario verified only at pure-logic altitude
	CodeBreakNoEntryPoint    = "BREAK_NO_ENTRYPOINT"    // a break-tested promise whose killing verifiers ran no way in (info)
	CodeEntryPointParse      = "ENTRYPOINT_PARSE_ERROR" // a persisted entry-point file could not be used

	// γ — demonstration (ingested test results).
	CodeVerifierFailing = "VERIFIER_FAILING" // an ingested result for a @verifies test is red

	// CodeBRDBelowLevel: a criterion or scenario is below validate.require_level.
	CodeBRDBelowLevel = "BRD_BELOW_LEVEL"

	// Coverage (rnd/0009). All four are off unless the workspace has a
	// coverage: block (or sets their severity), and each fires only on
	// ingested data, stating the evidence it rests on.
	CodeVerifierMissesEnforcer = "VERIFIER_MISSES_ENFORCER" // no verifier's run executed any enforcer (warning)
	CodeVerifierUnproven       = "VERIFIER_UNPROVEN"        // a verifier has no per-test coverage (info)
	CodeCoverageStale          = "COVERAGE_STALE"           // coverage is of another commit, or a dirty tree (warning)
	CodeCoverageBelowFloor     = "COVERAGE_BELOW_FLOOR"     // a feature, invariant or tier is under its declared floor (warning)

	// CodeVerifiedLinesBelowFloor: a feature's verified lines (the lines of
	// its blocks that their own verifiers ran, from lattice break --lines)
	// are under coverage.floors.verified_lines (warning); a feature with no
	// line data reads "no line data" as info. Never fires without the floor.
	CodeVerifiedLinesBelowFloor = "VERIFIED_LINES_BELOW_FLOOR"

	// CodeResultsStale: the ingested test results are of another commit
	// than the graph's (info, always on). It names the evidence and never
	// changes a level.
	CodeResultsStale = "RESULTS_STALE"

	// Reachability (rnd/0009, phase 2). Every one is off until enabled in
	// validate.severity; a coverage: block does not turn them on, because
	// they depend on the call graph's quality. REACHABILITY_NO_ROOTS,
	// RPC_UNRESOLVED and IMPORT_UNRESOLVED come on (info) with the first of
	// the others.
	CodeSymbolUnreached     = "SYMBOL_UNREACHED"      // no root, test or reached symbol reaches it, and it did not run (warning)
	CodeSymbolTestOnly      = "SYMBOL_TEST_ONLY"      // only tests reach it (info)
	CodeSymbolUntested      = "SYMBOL_UNTESTED"       // a root reaches it, but no test executed it (info, needs suite coverage)
	CodeUnusedExport        = "UNUSED_EXPORT"         // exported, and no other module imports or references it (info)
	CodeReachabilityNoRoots = "REACHABILITY_NO_ROOTS" // the graph has no roots, so no symbol is classified (info)
	CodeRPCUnresolved       = "RPC_UNRESOLVED"        // an .rpc() whose function name is not a literal (info)
	CodeImportUnresolved    = "IMPORT_UNRESOLVED"     // an import() or require() whose module is not a literal (info)
	CodeSuiteOverlap        = "SUITE_OVERLAP"         // two test suites verify nearly the same targets (info)

	// SQL exposure. Both are info and always on: an off code would hide an
	// unknown. They never fail a gate unless validate.severity says so.
	CodeEnforcerExposed = "ENFORCER_EXPOSED" // a SQL enforcer an API role can execute, with no security case that calls it as that role (info)
	CodeExposureUnknown = "EXPOSURE_UNKNOWN" // who can execute a SQL enforcer cannot be told from the migrations, or no API roles are declared (info)

	// Writers of a promise's columns. Both are info and always on, for the
	// same reason: they never fail a gate unless validate.severity says so.
	CodeUnguardedWriter = "UNGUARDED_WRITER" // a SQL writer of an invariant's columns that is neither an enforcer of it nor under a trigger or policy that enforces it (info)
	CodeWritersUnknown  = "WRITERS_UNKNOWN"  // an invariant's columns lie on a table whose writers cannot all be told from the migrations (info)

	// Council approval (v0.12).
	CodeBRDCouncilUnreviewed = "BRD_COUNCIL_UNREVIEWED" // a council approval its human has not acknowledged (info)
	CodeBRDCouncilInvalid    = "BRD_COUNCIL_INVALID"

	// CodeCriterionTierUnset: a BRD has criteria whose tier is not 1, 2 or 3,
	// which every tier rule reads as tier 1 (info, always on).
	CodeCriterionTierUnset = "CRITERION_TIER_UNSET"

	// CodeSQLTagDropped: a later migration redefines a function, policy or
	// trigger without tags an earlier definition carried (warning).
	CodeSQLTagDropped = "SQL_TAG_DROPPED" // a council approval no pinned mandate, floor or vote supports (error)

	// δ — meaning fidelity (does the wired link mean what was asked?).
	CodeCriterionInvariantNarrower = "CRITERION_INVARIANT_NARROWER" // invariant does not entail its criterion (assisted)
	CodeInvariantUnfalsifiable     = "INVARIANT_UNFALSIFIABLE"      // enforcer has no reachable violating path
	CodeInvariantMutantSurvived    = "INVARIANT_MUTANT_SURVIVED"    // a mutant of the enforcer survived the suite
	CodeEnforcerNotGuard           = "ENFORCER_NOT_GUARD"           // @enforces symbol carries the tag but guards nothing

	// §5/§6 — fleet coordination + attribution.
	CodeLeaseScopeOverlap         = "LEASE_SCOPE_OVERLAP"         // two live leases claim overlapping paths
	CodeConcurrentSurfaceConflict = "CONCURRENT_SURFACE_CONFLICT" // leased working sets diverge a shared contract surface
	CodeUnattributedChange        = "UNATTRIBUTED_CHANGE"         // a transition with no actor under require_actor

	// v0.8.1 — lifecycle governance.
	//
	// V8 author separation, tier-gated mutation evidence, the
	// forbidden-move sweep, and open meaning flags. All info/warning —
	// they report honesty problems, they do not block builds.
	CodeAuthorNotSeparated   = "AUTHOR_NOT_SEPARATED"   // same actor wired AND demonstrated a unit, no independent pass (V8)
	CodeMutationRequiredTier = "MUTATION_REQUIRED_TIER" // tier-2+ criterion chain lacks mutation evidence (V10 gate)
	CodeTestRetiredIllegally = "TEST_RETIRED_ILLEGALLY" // verifier disappeared without a CR retirement item (sweep)
	CodeRevisionSchema       = "REVISION_SCHEMA"        // CR file parse / required-field error
	CodeCriterionFlagged     = "CRITERION_FLAGGED"      // open meaning/demotion flag — reported alongside green, never hidden

	// Acceptance cases under an invariant (rnd/0010 §B).
	CodeCaseSchema           = "CASE_SCHEMA"            // a case's kind is unknown or its statement is empty (error)
	CodeCaseIDFormat         = "CASE_ID_FORMAT"         // a case id is not lowercase letters (error)
	CodeCaseIDDuplicate      = "CASE_ID_DUPLICATE"      // two cases of one invariant share an id (error)
	CodeOrphanAnnotationCase = "ORPHAN_ANNOTATION_CASE" // @verifies f:INV-N#c names a case the invariant does not declare (error)
	CodeCaseRetiredVerified  = "CASE_RETIRED_VERIFIED"  // @verifies names a retired case (warning)
	CodeCaseEnforcerUnknown  = "CASE_ENFORCER_UNKNOWN"  // a case's enforcer is not a symbol that enforces its invariant (error)
	CodeInvariantBroad       = "INVARIANT_BROAD"        // an invariant with no cases reads as several claims (info)

	// Drift between the spec and the code. Both are info and always on: they
	// state a fact the graph holds and never fail a gate unless
	// validate.severity says so.
	CodeInvariantUnrequired = "INVARIANT_UNREQUIRED" // a declared invariant that no criterion's maps_to_invariant names (info)
	CodeStatusDrift         = "STATUS_DRIFT"         // a proposal or accepted feature on the flow of a production entry point (info)

	// Break facts, read from breaks.json. Both are info and always on.
	CodeBreakSurvived     = "BREAK_SURVIVED"      // break-tested promises whose break no verifier noticed (info)
	CodeBreakUnknownCause = "BREAK_UNKNOWN_CAUSE" // unknown break runs by cause, or no break evidence at all (info)

	// Workspace hygiene: derived files git should not track (warning).
	CodeGraphTracked = "GRAPH_TRACKED" // lattice/lattice.json is tracked, so every extract dirties the tree
	CodeCacheTracked = "CACHE_TRACKED" // files under lattice/.cache/ are tracked
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

	// Subject names the thing the finding is about in a stable form an
	// allowlist entry can match: "brd.<id>:SC-N", a module file, a symbol
	// FQN. Empty when the rule does not set one.
	Subject string `json:"subject,omitempty"`

	// Refs lists the items one rolled-up finding covers, each a Subject an
	// allowlist entry can match on its own (INVARIANT_UNREQUIRED lists
	// "<feature>:<INV-N>"). Empty for a finding about one thing.
	Refs []string `json:"refs,omitempty"`

	Location   *Location   `json:"location,omitempty"`
	NextAction *NextAction `json:"next_action,omitempty"`
}

// IsError reports whether the violation should fail validation.
func (v Violation) IsError() bool { return v.Severity == SeverityError }
