package schema

// Block is the one primitive of the block graph (rnd/0003): a controlled logic
// path with inputs, decisions and outputs. Blocks nest through Parent. Features,
// invariants and entry points are converted into blocks at build time.
type Block struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Parent      string            `json:"parent,omitempty"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Tier        string            `json:"tier,omitempty"`
	System      string            `json:"system,omitempty"` // the system block whose source stamp applies
	Truth       string            `json:"truth"`
	Provenance  Provenance        `json:"provenance"`
	BRD         string            `json:"brd,omitempty"` // set on blocks converted from features
	Attrs       map[string]string `json:"attrs,omitempty"`
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

// SourceStamp records what one workspace source was read at.
type SourceStamp struct {
	ID         string `json:"id"`
	Repo       string `json:"repo,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Commit     string `json:"commit,omitempty"`
	CommitDate string `json:"commit_date,omitempty"`
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
