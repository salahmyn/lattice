// Package contract holds the shapes of the /api/v2 payloads that the React UI
// and agents bind to: lattice.meta/1, lattice.queue/1, lattice.brd/1 and
// lattice.health/1. (lattice.check/1 and lattice.prepare/1 are the shapes of
// the check and prepare packages, returned as they are.)
//
// The package is data and rules about data. It computes nothing that counts:
// every value in a payload is read from the kernel's packages. A field the
// kernel has no source for is NotComputed, never a guess. Lint is the rule
// every payload must pass before it leaves the server: unknown, UNPROVEN and
// not-computed are never a pass.
package contract

// @module-feature api

// Contract tags, frozen by the golden tests.
const (
	MetaV1   = "lattice.meta/1"
	QueueV1  = "lattice.queue/1"
	BRDV1    = "lattice.brd/1"
	HealthV1 = "lattice.health/1"
)

// Assurance states. Unknown is never a pass.
const (
	StatePass    = "pass"
	StateFail    = "fail"
	StateUnknown = "unknown"
	StateInfo    = "info"
	StateDrifted = "drifted"
	// Evidence states of a health cell: a fact about a file, not a verdict.
	StatePresent = "present"
	StatePartial = "partial"
	StateAbsent  = "absent"
	StateStale   = "stale"
)

// Honesty labels.
const (
	Fact       = "fact"
	UpperBound = "upper_bound"
	Proxy      = "proxy"
)

// CodeNotComputed marks a field the kernel has no source for.
const CodeNotComputed = "NOT_COMPUTED"

// Assurance is one measurement, the shape every assurance value has: the
// state, how far to trust it, a stable code and params a client can
// translate, and the kernel's English text.
type Assurance struct {
	State   string                 `json:"state"`
	Honesty string                 `json:"honesty"`
	Code    string                 `json:"code,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Text    string                 `json:"text,omitempty"`
}

// NotComputed is the value of a field the kernel does not compute yet.
// @enforces api:INV-1
func NotComputed(what string) Assurance {
	return Assurance{State: StateUnknown, Honesty: Fact, Code: CodeNotComputed, Text: "not computed: " + what + " has no kernel source yet"}
}

// Verdict is the answer for a scope: BROKEN, UNPROVEN, ASSURED or unknown.
// A scope that is not a change never reads ASSURED or BROKEN: those need a
// base (lattice check).
type Verdict struct {
	State  string                 `json:"state"`
	Code   string                 `json:"code"`
	Params map[string]interface{} `json:"params,omitempty"`
	Text   string                 `json:"text"`
}

// Header is what every payload carries.
type Header struct {
	Contract string `json:"contract"`
	// Commit is the commit the payload describes.
	Commit string `json:"commit"`
	// CIRun is the CI run that produced the test results, "" when none or local.
	CIRun string `json:"ci_run"`
	// EvidenceTied is true when the results are stamped with this commit and a CI run.
	EvidenceTied bool `json:"evidence_tied"`
}

// Reason says why an item is on a list.
type Reason struct {
	Code   string                 `json:"code"`
	Params map[string]interface{} `json:"params,omitempty"`
	Text   string                 `json:"text"`
}

// Chip is a sub-state of a BRD, shown beside its state.
type Chip struct {
	Code   string                 `json:"code"`
	Params map[string]interface{} `json:"params,omitempty"`
	Text   string                 `json:"text"`
}

// BRD states, from the UI plan's state machine. DRIFTED is computed from
// confirm.json, never stored.
const (
	BRDDraft      = "DRAFT"
	BRDProposed   = "PROPOSED"
	BRDConfirmed  = "CONFIRMED"
	BRDDrifted    = "DRIFTED"
	BRDSuperseded = "SUPERSEDED"
)

// --- lattice.meta/1 ---

// Meta is GET /api/v2/meta.
type Meta struct {
	Header
	Kernel   Kernel      `json:"kernel"`
	Dirty    *bool       `json:"dirty"`
	Evidence Provenance  `json:"evidence"`
	Prepare  PrepareInfo `json:"prepare"`
}

// Kernel is the binary that serves the payload.
type Kernel struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Stamp is where one evidence file came from.
type Stamp struct {
	Present bool   `json:"present"`
	Commit  string `json:"commit"`
	// CIRun is the CI run id the file is stamped with; "" when it has none.
	CIRun string `json:"ci_run"`
	// Tie is pass only when the file is stamped with the head commit and a CI
	// run. Anything else reads unknown, with the reason in its code.
	Tie Assurance `json:"tie"`
}

// Provenance is the provenance of every evidence file the verdicts stand on.
type Provenance struct {
	Results       Stamp `json:"results"`
	Coverage      Stamp `json:"coverage"`
	Breaks        Stamp `json:"breaks"`
	AuditSnapshot Stamp `json:"audit_snapshot"`
	Confirm       Stamp `json:"confirm"`
}

// PrepareInfo says whether this client may prepare a decision.
type PrepareInfo struct {
	Enabled bool `json:"enabled"`
	// Header is the request header that carries the token.
	Header string `json:"header,omitempty"`
	// CSRFToken is issued to loopback clients only.
	CSRFToken string `json:"csrf_token,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// --- lattice.queue/1 ---

// Queue kinds.
const (
	QueueBRDAwaiting    = "brd_awaiting"
	QueueBRDDrifted     = "brd_drifted"
	QueueConfirmedFake  = "confirmed_fake"
	QueuePromiseUnknown = "promise_unknown_t3"
)

// Queue is GET /api/v2/queue.
type Queue struct {
	Header
	Items []QueueItem `json:"items"`
	// Proposals has no kernel source: open proposals are not stored.
	Proposals Assurance `json:"proposals"`
}

// BlastRadius is what an item reaches: the entry points that run its
// features, and its promises. Items rank on entry points, then promises.
type BlastRadius struct {
	EntryPoints int `json:"entry_points"`
	Promises    int `json:"promises"`
}

// QueueItem is one thing that needs a human.
type QueueItem struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Rank    int    `json:"rank"`
	Subject string `json:"subject"`
	Version int    `json:"version,omitempty"`
	Tier    int    `json:"tier"`
	State   string `json:"state,omitempty"`
	Chips   []Chip `json:"chips,omitempty"`
	// BlastRadius is what the item touches.
	BlastRadius BlastRadius `json:"blast_radius"`
	// Age is when the item started waiting: info with params.since when a
	// source exists, unknown NOT_COMPUTED when none does.
	Age    Assurance `json:"age"`
	Reason Reason    `json:"reason"`
	Link   string    `json:"link"`
}

// --- lattice.brd/1 ---

// BRDList is GET /api/v2/brds.
type BRDList struct {
	Header
	BRDs []BRDSummary `json:"brds"`
}

// BRDSummary is one row of the list.
type BRDSummary struct {
	ID       string   `json:"id"`
	Version  int      `json:"version"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	State    string   `json:"state"`
	Chips    []Chip   `json:"chips,omitempty"`
	TierMax  int      `json:"tier_max"`
	Features []string `json:"features"`
	Verdict  Verdict  `json:"verdict"`
	Link     string   `json:"link"`
}

// Confirmation is who confirmed which version, as the platform recorded it.
type Confirmation struct {
	State            Assurance `json:"state"`
	ConfirmedVersion int       `json:"confirmed_version,omitempty"`
	ConfirmedBy      string    `json:"confirmed_by,omitempty"`
	Via              string    `json:"via,omitempty"`
}

// BRDRef is the BRD a feature belongs to.
type BRDRef struct {
	ID               string `json:"id"`
	Version          int    `json:"version"`
	State            string `json:"state"`
	ConfirmedVersion int    `json:"confirmed_version,omitempty"`
	ConfirmedBy      string `json:"confirmed_by,omitempty"`
	Via              string `json:"via,omitempty"`
}

// Signals are the signals of one promise. nothing_fell is measured against a
// base, so a scope view carries it as NOT_COMPUTED.
type Signals struct {
	Traced      Assurance `json:"traced"`
	Confirmed   Assurance `json:"confirmed"`
	Live        Assurance `json:"live"`
	Exercised   Assurance `json:"exercised"`
	BreakTested Assurance `json:"break_tested"`
	NothingFell Assurance `json:"nothing_fell"`
}

// Gap is one required signal of a promise that is not a pass.
type Gap struct {
	Signal  string `json:"signal"`
	State   string `json:"state"`
	Honesty string `json:"honesty"`
	Code    string `json:"code,omitempty"`
	Text    string `json:"text,omitempty"`
	Next    string `json:"next,omitempty"`
}

// Enforcer is a symbol that keeps a promise. Lines are the symbol's first
// line only: the kernel records no span.
type Enforcer struct {
	FQN  string `json:"fqn"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Live is the symbol's reach class, an upper bound.
	Live Assurance `json:"live"`
}

// Verifier is a test that guards a promise, and what its own run did.
type Verifier struct {
	FQN    string `json:"fqn"`
	File   string `json:"file,omitempty"`
	Result string `json:"result"`
	// Coverage is the basis of the test's coverage (per_test, per_file, none, "").
	Coverage    string `json:"coverage,omitempty"`
	RanEnforcer bool   `json:"ran_enforcer"`
}

// Waiver is a human's exception. It is shown and never counted as proof.
type Waiver struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind,omitempty"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Note is a second reader's verdict on a promise.
type Note struct {
	Reader  string `json:"reader"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
}

// Promise is one promise with its signals and evidence.
type Promise struct {
	ID        string  `json:"id"`
	Feature   string  `json:"feature"`
	Statement string  `json:"statement"`
	Tier      int     `json:"tier"`
	Class     string  `json:"class"`
	Kind      string  `json:"kind,omitempty"`
	Reason    string  `json:"reason"`
	Verdict   Verdict `json:"verdict"`
	// Criteria are the BRD criteria that map to the promise.
	Criteria  []string   `json:"criteria,omitempty"`
	Signals   Signals    `json:"signals"`
	Gaps      []Gap      `json:"gaps"`
	Enforcers []Enforcer `json:"enforcers"`
	Verifiers []Verifier `json:"verifiers"`
	// Cases (happy, edge, error...) have no kernel source.
	Cases      Assurance `json:"cases"`
	OutOfReach *Waiver   `json:"out_of_reach,omitempty"`
	Waivers    []Waiver  `json:"waivers,omitempty"`
	Notes      []Note    `json:"notes,omitempty"`
	NextAction string    `json:"next_action"`
}

// Criterion is one success criterion.
type Criterion struct {
	ID        string   `json:"id"`
	Statement string   `json:"statement"`
	Tier      int      `json:"tier"`
	MapsTo    []string `json:"maps_to"`
}

// Constraint is an external rule. A regulatory, legal or financial one is
// locked: human-written, human-judged, and it carries no verdict.
type Constraint struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref,omitempty"`
	Note   string `json:"note,omitempty"`
	Locked bool   `json:"locked"`
	// Author is who wrote this version of the BRD, since the constraints have no author of their own.
	Author string `json:"author,omitempty"`
}

// Scenario is a user scenario from the BRD.
type Scenario struct {
	ID        string `json:"id"`
	Actor     string `json:"actor,omitempty"`
	Narrative string `json:"narrative"`
}

// Intent is the BRD's own words.
type Intent struct {
	// Story ("As a ..., I want ...") has no kernel source: the story layer is not in code.
	Story           Assurance  `json:"story"`
	BusinessProblem string     `json:"business_problem"`
	Goals           []string   `json:"goals"`
	Scenarios       []Scenario `json:"scenarios"`
	OutOfScope      []string   `json:"out_of_scope"`
	OpenQuestions   []string   `json:"open_questions"`
}

// Provenance of a BRD version.
type BRDProvenance struct {
	Source      string `json:"source,omitempty"`
	AuthoredBy  string `json:"authored_by,omitempty"`
	Model       string `json:"model,omitempty"`
	GeneratedAt string `json:"generated_at,omitempty"`
}

// Vote is one council seat's review.
type Vote struct {
	Seat    string `json:"seat"`
	Actor   string `json:"actor"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// Council is the council's advice on a version. It is advice, not a decision.
type Council struct {
	Mandate     string `json:"mandate,omitempty"`
	DelegatedBy string `json:"delegated_by,omitempty"`
	Summary     string `json:"summary,omitempty"`
	HumanReview string `json:"human_review,omitempty"`
	Votes       []Vote `json:"votes"`
	Overturned  string `json:"overturned,omitempty"`
}

// EntryPoint is an entry point that runs a feature, with a static reach.
type EntryPoint struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Live is upper_bound: a path the code allows, not one that ran.
	Live Assurance `json:"live"`
	// Step and Of place the feature in the entry point's flow.
	Step int `json:"step"`
	Of   int `json:"of"`
}

// Counts are counts of the promises in a scope.
type Counts struct {
	Promises int `json:"promises"`
	// Unproven is the promises with at least one gap in a required signal.
	Unproven int `json:"unproven"`
	// Unknown is the promises with no gap, whose fall against a base is not measured here.
	Unknown    int `json:"unknown"`
	OutOfReach int `json:"out_of_reach"`
}

// BRDPage is GET /api/v2/brds/{id}.
type BRDPage struct {
	Header
	ID           string            `json:"id"`
	Version      int               `json:"version"`
	Status       string            `json:"status"`
	State        string            `json:"state"`
	Chips        []Chip            `json:"chips,omitempty"`
	Title        string            `json:"title"`
	Summary      string            `json:"summary"`
	Verdict      Verdict           `json:"verdict"`
	Provenance   BRDProvenance     `json:"provenance"`
	Confirmation Confirmation      `json:"confirmation"`
	Features     []string          `json:"features"`
	TierMax      int               `json:"tier_max"`
	Owners       map[string]string `json:"owners,omitempty"`
	Counts       Counts            `json:"counts"`
	Intent       Intent            `json:"intent"`
	Criteria     []Criterion       `json:"criteria"`
	Promises     []Promise         `json:"promises"`
	Constraints  []Constraint      `json:"constraints"`
	Council      *Council          `json:"council,omitempty"`
	EntryPoints  []EntryPoint      `json:"entry_points"`
	Waivers      []Waiver          `json:"waivers"`
	BlindSpot    Assurance         `json:"blind_spot"`
	Sources      Assurance         `json:"sources"`
	History      History           `json:"history"`
	// Packet is set only when the page was asked for one version (?version=).
	Packet *Packet `json:"packet,omitempty"`
}

// HistoryEntry is one decision confirm.json holds for a BRD.
type HistoryEntry struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	State   string `json:"state"`
	By      string `json:"by,omitempty"`
	Via     string `json:"via,omitempty"`
}

// History is the decisions on a BRD, as the platform recorded them. State
// says whether confirm.json was there to read.
type History struct {
	State   Assurance      `json:"state"`
	Entries []HistoryEntry `json:"entries"`
}

// Diff is the structural change since the last confirmed version.
type Diff struct {
	// Since is the confirmed version the diff is against; 0 when none was.
	SinceVersion   int    `json:"since_version,omitempty"`
	SinceCommit    string `json:"since_commit,omitempty"`
	NeverConfirmed bool   `json:"never_confirmed"`
	// Structural lists criteria, tiers, mappings and constraints that changed.
	Structural []string `json:"structural"`
	// TextOnly has no kernel source: the kernel diffs structure only.
	TextOnly Assurance `json:"text_only"`
}

// Packet is the review packet of one BRD version.
type Packet struct {
	Version int  `json:"version"`
	Diff    Diff `json:"diff"`
	// PacketHash is confirm.PacketHash, computed exactly as a decision pins it.
	// It is NOT_COMPUTED-shaped (empty, with HashState unknown) when the diff base could not be read.
	PacketHash string    `json:"packet_hash"`
	HashState  Assurance `json:"hash_state"`
	// VotesHash and AckPacketHash are set when a council reviewed the version.
	VotesHash     string `json:"votes_hash,omitempty"`
	AckPacketHash string `json:"ack_packet_hash,omitempty"`
	Counts        Counts `json:"counts"`
	// BlindSpot is untraced touched lines: measured per change.
	BlindSpot  Assurance `json:"blind_spot"`
	OutOfReach []Waiver  `json:"out_of_reach"`
}

// FeaturePage is GET /api/v2/features/{id}: the drawer.
type FeaturePage struct {
	Header
	ID      string            `json:"id"`
	Status  string            `json:"status"`
	Purpose string            `json:"purpose"`
	Owners  map[string]string `json:"owners,omitempty"`
	Origin  Assurance         `json:"origin"`
	BRD     *BRDRef           `json:"brd,omitempty"`
	Verdict Verdict           `json:"verdict"`
	// NoPromise is true when the feature declares none: it never looks clean.
	NoPromise   bool         `json:"no_promise"`
	Counts      Counts       `json:"counts"`
	BlindSpot   Assurance    `json:"blind_spot"`
	Promises    []Promise    `json:"promises"`
	RunsIn      Assurance    `json:"runs_in"`
	EntryPoints []EntryPoint `json:"entry_points"`
	Related     Related      `json:"related"`
	Sources     Assurance    `json:"sources"`
	History     Assurance    `json:"history"`
}

// Related are the features this one depends on, and those that depend on it.
type Related struct {
	DependsOn  []string `json:"depends_on"`
	DependedBy []string `json:"depended_by"`
	// Effect (what a break in one does to the other) has no kernel source.
	Effect Assurance `json:"effect"`
}

// --- lattice.health/1 ---

// Cell is one cell of the evidence matrix: whether a feature has this
// evidence, from which commit, and whether it is stamped by CI. State is
// present, partial, absent, stale or unknown, never pass: a file being there
// is not a verdict on a promise.
type Cell struct {
	State   string                 `json:"state"`
	Honesty string                 `json:"honesty"`
	Code    string                 `json:"code,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Text    string                 `json:"text,omitempty"`
	Commit  string                 `json:"commit"`
	// CIStamped is true when the file carries a CI run, false when it does
	// not, and null when the file has no field for it.
	CIStamped *bool `json:"ci_stamped"`
}

// HealthRow is one feature across the evidence columns.
type HealthRow struct {
	Feature  string `json:"feature"`
	Promises int    `json:"promises"`
	Results  Cell   `json:"results"`
	Coverage Cell   `json:"coverage"`
	Breaks   Cell   `json:"breaks"`
	Confirm  Cell   `json:"confirm"`
}

// Health is GET /api/v2/health.
type Health struct {
	Header
	Columns []string    `json:"columns"`
	Rows    []HealthRow `json:"rows"`
	// Waivers by kind, the trend (it should only shrink) and ignored inputs
	// have no kernel source here.
	Waivers       []Waiver  `json:"waivers"`
	WaiverTrend   Assurance `json:"waiver_trend"`
	IgnoredInputs Assurance `json:"ignored_inputs"`
}
