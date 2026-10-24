package schema

// @module-feature schema

// Block is the one primitive of the block graph (rnd/0003): a controlled logic
// path with inputs, decisions and outputs. Blocks nest through Parent. Features,
// invariants and entry points are converted into blocks at build time.
type Block struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	Parent      string             `json:"parent,omitempty"`
	Title       string             `json:"title"`
	Description string             `json:"description,omitempty"`
	Tier        string             `json:"tier,omitempty"`
	System      string             `json:"system,omitempty"` // the system block whose source stamp applies
	Truth       string             `json:"truth"`
	Provenance  Provenance         `json:"provenance"`
	BRD         string             `json:"brd,omitempty"` // set on blocks converted from features
	Attrs       map[string]string  `json:"attrs,omitempty"`
	Overlays    map[string]Overlay `json:"overlays,omitempty"`   // initiative id -> its verdict on this block
	Initiative  string             `json:"initiative,omitempty"` // set on blocks that exist only in a proposal
}

// Overlay is one initiative's verdict on a block: the top-ranked class of its
// cases, and the cases themselves.
type Overlay struct {
	Verdict string        `json:"verdict"`
	Title   string        `json:"title,omitempty"`
	Cases   []OverlayCase `json:"cases,omitempty"`
}

// OverlayCase is one evidence row about a block.
type OverlayCase struct {
	Case        string   `json:"case,omitempty"`
	ID          string   `json:"id"` // the evidence row's block id, as written
	Class       string   `json:"class"`
	Requirement string   `json:"requirement,omitempty"`
	Evidence    string   `json:"evidence,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	Gaps        []string `json:"gaps,omitempty"` // gap codes that cite this block
}

// BlockEdge is a typed, directed link between two blocks.
type BlockEdge struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Kind       string     `json:"kind"`
	Labels     []string   `json:"labels,omitempty"`
	When       []string   `json:"when,omitempty"`
	Provenance Provenance `json:"provenance"`
}

// Provenance says where a block or edge came from.
type Provenance struct {
	Kind   string `json:"kind"`             // adapter | judgment | extraction | human
	Source string `json:"source,omitempty"` // e.g. "shop@abc1234:docs/flows/cart.json", "judgment:landscape"
}

// SourceStamp says what a source was read at and how that stands against its
// live release.
type SourceStamp struct {
	ID         string   `json:"id"`
	Repo       string   `json:"repo,omitempty"`
	Ref        string   `json:"ref,omitempty"`
	Commit     string   `json:"commit,omitempty"`
	CommitDate string   `json:"commit_date,omitempty"`
	System     string   `json:"system,omitempty"` // the system block this source describes
	Branch     string   `json:"branch,omitempty"` // snapshot stamps: the branch the snapshot was taken from
	State      string   `json:"state,omitempty"`  // one of the Fresh* states
	Behind     int      `json:"behind,omitempty"` // commits the release has that Commit lacks
	Ahead      int      `json:"ahead,omitempty"`  // commits Commit has that the release lacks
	Release    *Release `json:"release,omitempty"`
}

// Release is the deployed version of a source's repo (release/v1).
type Release struct {
	Repo        string `json:"repo,omitempty"`
	Version     string `json:"version"`
	Commit      string `json:"commit,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	DeployedAt  string `json:"deployed_at,omitempty"`
	Run         string `json:"run,omitempty"`
	Source      string `json:"source,omitempty"`
}

// Freshness states of a source stamp.
const (
	FreshCurrent   = "current"    // Commit is the released commit
	FreshBehind    = "behind"     // the release has commits Commit lacks
	FreshAhead     = "ahead"      // Commit has commits not yet released
	FreshUnknown   = "unknown"    // the release commit is not in the clone
	FreshNoRelease = "no_release" // no release given
	FreshSnapshot  = "snapshot"   // read from a directory snapshot, not a clone
)

// Flow is one simulated journey over blocks, cut at its first loop, with the
// round trips to the blocks that answer its requests.
type Flow struct {
	ID    string            `json:"id"`
	Title string            `json:"title,omitempty"`
	End   string            `json:"end"` // "ends", "loops back", or the walk's stop reason
	Facts map[string]string `json:"facts,omitempty"`
	Steps []FlowMove        `json:"steps"`
}

// FlowMove is one step of a flow.
type FlowMove struct {
	From   string   `json:"from,omitempty"`
	To     string   `json:"to"`
	Label  string   `json:"label,omitempty"`
	When   []string `json:"when,omitempty"`
	Kind   string   `json:"kind"` // start | flow | loop | api | reply
	Chosen bool     `json:"chosen,omitempty"`
}

// Truth levels a block can carry.
const (
	TruthDeclared = "declared"
	TruthWired    = "wired"
)

// Provenance kinds.
const (
	ProvenanceAdapter    = "adapter"
	ProvenanceJudgment   = "judgment"
	ProvenanceExtraction = "extraction"
	ProvenanceHuman      = "human"
)
