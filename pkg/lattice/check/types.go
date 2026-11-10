// Package check computes the one verdict for a change: BROKEN, UNPROVEN or
// ASSURED. It judges the change from merge-base(base, HEAD) to the working
// tree. It adds no measurement of its own: touched promises and lines come
// from impact, signals from audit.Compute at head and, when a snapshot of the
// base audit exists, from the same audit at base. Compute is a pure function
// of its Input, so the verdict can be tested without git or a run.
//
// The floor: the kernel computes facts; unknown is never a pass; only CI's
// verdict counts (evidence for another commit is set aside in every run, and
// `--ci` also refuses evidence that is not from a CI run); a waiver is shown
// and never counted as proof.
package check

// @module-feature check

// Contract is the version tag of the JSON report, frozen by a golden test.
const Contract = "lattice.check/1"

// Verdict states.
const (
	Broken   = "BROKEN"
	Unproven = "UNPROVEN"
	Assured  = "ASSURED"
)

// Assurance states. Unknown is never a pass.
const (
	Pass    = "pass"
	Fail    = "fail"
	Unknown = "unknown"
)

// Honesty labels, as in the audit.
const (
	Fact       = "fact"
	UpperBound = "upper_bound"
	Proxy      = "proxy"
)

// Signal names.
const (
	SigTraced      = "traced"
	SigConfirmed   = "confirmed"
	SigLive        = "live"
	SigExercised   = "exercised"
	SigBreakTested = "break_tested"
	SigNothingFell = "nothing_fell"
	SigSuiteGreen  = "suite_green"
)

// Codes. Each is a stable machine reading of a state; Text is the English
// fallback.
const (
	CodeGaps             = "GAPS"
	CodeBroken           = "BROKEN"
	CodeAllSignals       = "ALL_SIGNALS"
	CodeNothingTouched   = "NOTHING_TOUCHED"
	CodeUntracedLines    = "UNTRACED_LINES"
	CodeUnguardedLines   = "UNGUARDED_LINES"
	CodeNoBaseAudit      = "NO_BASE_AUDIT"
	CodeNewGap           = "NEW_GAP"
	CodeUntracedRose     = "UNTRACED_ROSE"
	CodeTier3            = "TIER3_INVOLVED"
	CodeMergeOK          = "MERGE_OK"
	CodeEvidenceNotCI    = "EVIDENCE_NOT_CI"
	CodeEvidenceStale    = "EVIDENCE_STALE"
	CodeNoResults        = "NO_RESULTS"
	CodeSuiteRed         = "SUITE_RED"
	CodeEnforcerRemoved  = "ENFORCER_REMOVED"
	CodeVerifierRemoved  = "VERIFIER_REMOVED"
	CodeLevelDropped     = "LEVEL_DROPPED"
	CodeBreakSurvived    = "BREAK_SURVIVED"
	CodeConfirmationVoid = "CONFIRMATION_VOIDED"
)

// Assurance is one measurement: the shape every assurance value has in the
// Lattice contracts. Code and Params let a client translate the text; Text
// is the kernel's English fallback.
type Assurance struct {
	State   string                 `json:"state"`
	Honesty string                 `json:"honesty"`
	Code    string                 `json:"code,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Text    string                 `json:"text,omitempty"`
}

// Verdict is the check's one answer.
type Verdict struct {
	State  string                 `json:"state"`
	Code   string                 `json:"code"`
	Params map[string]interface{} `json:"params,omitempty"`
	Text   string                 `json:"text"`
}

// MergeRule is why merge_allowed has the value it has.
type MergeRule struct {
	Code   string                 `json:"code"`
	Params map[string]interface{} `json:"params,omitempty"`
	Text   string                 `json:"text"`
}

// Ref is one end of the change.
type Ref struct {
	Rev   string `json:"rev,omitempty"`
	SHA   string `json:"sha"`
	Dirty bool   `json:"dirty,omitempty"`
}

// BaseAudit says whether the base's audit snapshot was found.
type BaseAudit struct {
	Present bool   `json:"present"`
	Commit  string `json:"commit,omitempty"`
	Text    string `json:"text,omitempty"`
}

// EvidenceItem is one evidence file and whether the check may count it.
type EvidenceItem struct {
	Present bool   `json:"present"`
	Commit  string `json:"commit,omitempty"`
	CIRun   string `json:"ci_run,omitempty"`
	// Tied is true when the file is stamped with the head commit and a CI run.
	Tied bool `json:"tied"`
	// Counted is false when the check set the file aside: it reads unknown.
	Counted bool   `json:"counted"`
	Code    string `json:"code,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Evidence is what the verdict stands on.
type Evidence struct {
	Results   EvidenceItem `json:"results"`
	Coverage  EvidenceItem `json:"coverage"`
	Breaks    EvidenceItem `json:"breaks"`
	BaseAudit BaseAudit    `json:"base_audit"`
	Suite     SuiteResult  `json:"suite"`
}

// SuiteResult is the full test run at head, as ingested.
type SuiteResult struct {
	Present bool     `json:"present"`
	Cases   int      `json:"cases"`
	Failed  int      `json:"failed"`
	Red     []string `json:"red,omitempty"`
}

// Signals are the signals of one touched promise.
type Signals struct {
	Traced      Assurance `json:"traced"`
	Confirmed   Assurance `json:"confirmed"`
	Live        Assurance `json:"live"`
	Exercised   Assurance `json:"exercised"`
	BreakTested Assurance `json:"break_tested"`
	NothingFell Assurance `json:"nothing_fell"`
	SuiteGreen  Assurance `json:"suite_green"`
}

// PromiseRow is one touched promise.
type PromiseRow struct {
	Promise   string `json:"promise"`
	Feature   string `json:"feature"`
	Statement string `json:"statement,omitempty"`
	Tier      int    `json:"tier"`
	// Class is the audit's class at head.
	Class string `json:"class"`
	// Touched says why the promise is on the page.
	Touched    []string `json:"touched"`
	NewPromise bool     `json:"new_promise,omitempty"`
	Signals    Signals  `json:"signals"`
	// Needs lists the signals this tier requires, which a gap is measured against.
	Needs      []string `json:"needs"`
	NextAction string   `json:"next_action"`
}

// Delta compares a gap with the base.
const (
	DeltaNew     = "new"
	DeltaSame    = "same"
	DeltaClosed  = "closed"
	DeltaUnknown = "unknown" // no base audit to compare with
)

// Gap is one (promise, signal) pair that is not a pass at head, or a pair
// that was a gap at base and is closed.
type Gap struct {
	Promise string `json:"promise"`
	Signal  string `json:"signal"`
	State   string `json:"state"`
	Honesty string `json:"honesty,omitempty"`
	Code    string `json:"code,omitempty"`
	Text    string `json:"text,omitempty"`
	Delta   string `json:"delta"`
	// NewPromise marks a promise the change adds. Its unconfirmed gap cannot
	// have existed at base and does not block a merge by itself.
	NewPromise bool `json:"new_promise,omitempty"`
	// Waived is display only: a waiver names this pair. The gap still counts.
	Waived bool `json:"waived,omitempty"`
	// Blocks is true when the gap alone fails the merge rule.
	Blocks     bool   `json:"blocks,omitempty"`
	NextAction string `json:"next_action,omitempty"`
}

// Weakening is something that fell. Any weakening reads BROKEN.
type Weakening struct {
	Code    string                 `json:"code"`
	Promise string                 `json:"promise,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Text    string                 `json:"text"`
	Next    string                 `json:"next,omitempty"`
}

// UntracedLine is a touched production symbol that no promise covers.
type UntracedLine struct {
	File   string `json:"file"`
	Symbol string `json:"symbol"`
	Lines  string `json:"lines,omitempty"`
	Count  int    `json:"count"`
	// New is true when the change adds the symbol, or removes its
	// enforcement; only new untraced lines fail the merge rule.
	New bool `json:"new,omitempty"`
	// Reason says why it counts as untraced, for example a file tag only.
	Reason string `json:"reason"`
}

// OutOfReach is a touched promise a human marked out of reach.
type OutOfReach struct {
	Promise string `json:"promise"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason,omitempty"`
}

// BlindSpot is the honest edge of the other signals: it never fails the
// check by itself, and it keeps the verdict at UNPROVEN while it is not empty.
type BlindSpot struct {
	State   string                 `json:"state"`
	Honesty string                 `json:"honesty"`
	Code    string                 `json:"code"`
	Params  map[string]interface{} `json:"params"`
	Text    string                 `json:"text"`
	// Untraced lists touched lines no promise covers, by file and symbol.
	Untraced   []UntracedLine `json:"untraced"`
	OutOfReach []OutOfReach   `json:"out_of_reach"`
	// UnguardedLines counts the touched production lines no promise guards
	// (no enforcer span or checked reference covers them): the untraced
	// lines, plus the lines an entry point reaches (so they are traced) and
	// the lines of removed symbols that enforced nothing. They keep the
	// verdict at UNPROVEN and never fail the merge rule by themselves.
	UnguardedLines int `json:"unguarded_lines"`
	// GuardedLines counts the touched production lines a promise guards.
	GuardedLines int `json:"guarded_lines"`
	// Unguarded lists the unguarded symbols that are not in Untraced: traced
	// by an entry point only, or removed. With Untraced it covers every
	// unguarded line.
	Unguarded []UntracedLine `json:"unguarded"`
}

// Waiver is a human-written exception from lattice/waivers.yaml. It is shown
// and never counted as proof.
type Waiver struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind,omitempty"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// InChange is true when the waivers file is part of this change.
	InChange bool `json:"in_change,omitempty"`
}

// Report is the check: lattice.check/1.
type Report struct {
	Contract     string       `json:"contract"`
	Commit       string       `json:"commit"`
	Base         Ref          `json:"base"`
	Head         Ref          `json:"head"`
	CI           bool         `json:"ci"`
	Verdict      Verdict      `json:"verdict"`
	MergeAllowed bool         `json:"merge_allowed"`
	Merge        MergeRule    `json:"merge"`
	Evidence     Evidence     `json:"evidence"`
	Promises     []PromiseRow `json:"promises"`
	Gaps         []Gap        `json:"gaps"`
	Weakenings   []Weakening  `json:"weakenings"`
	BlindSpot    BlindSpot    `json:"blind_spot"`
	Waivers      []Waiver     `json:"waivers"`
	// Note is carried on every report: UNPROVEN is not a pass.
	Note string `json:"note"`
}

const note = "UNPROVEN is not a pass: it means nothing fell and something is not shown. Unknown is never a pass; only CI's verdict counts."
