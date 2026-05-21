package schema

// KnowledgeGraph is the single artifact Lattice emits as lattice.json.
//
// It is committed to git and written with deterministic ordering: extracting
// twice from the same input produces byte-identical output.
type KnowledgeGraph struct {
	SchemaVersion       string `json:"schema_version"`
	GeneratedAt         string `json:"generated_at"`
	GeneratedFromCommit string `json:"generated_from_commit"`

	Features         []Manifest             `json:"features"`
	Symbols          []GraphSymbol          `json:"symbols"`
	Tests            []GraphSymbol          `json:"tests"`
	Modules          []GraphModule          `json:"modules"`
	Initiatives      []Initiative           `json:"initiatives"`
	Tasks            []Task                 `json:"tasks"`
	StructuralChecks []GraphStructuralCheck `json:"structural_checks"`

	CodeGraph  CodeGraph   `json:"code_graph"`
	Violations []Violation `json:"violations"`
}

// GraphSymbol is a code (or test) symbol with its resolved Lattice edges.
type GraphSymbol struct {
	Name         string `json:"name"`
	FQN          string `json:"fqn"`
	Kind         string `json:"kind"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Language     string `json:"language"`
	EnclosingFQN string `json:"enclosing_fqn,omitempty"`
	IsTest       bool   `json:"is_test"`

	// Resolved edges (effective annotation set, after module/role/inheritance
	// propagation).
	Feature              string                `json:"feature,omitempty"`
	Capabilities         []string              `json:"capabilities,omitempty"`
	EnforcesInvariants   []string              `json:"enforces_invariants,omitempty"`
	DependsOnFeatures    []string              `json:"depends_on_features,omitempty"`
	Roles                []string              `json:"roles,omitempty"`
	Verifies             []string              `json:"verifies,omitempty"`
	SuppressedInvariants []SuppressedInvariant `json:"suppressed_invariants,omitempty"`
}

// SuppressedInvariant records an explicit, reasoned suppression.
type SuppressedInvariant struct {
	Invariant string `json:"invariant"`
	Reason    string `json:"reason"`
}

// GraphModule is a source file with its module-level edges.
type GraphModule struct {
	File               string   `json:"file"`
	Language           string   `json:"language"`
	Feature            string   `json:"feature,omitempty"`
	EnforcesInvariants []string `json:"enforces_invariants,omitempty"`
	DependsOnFeatures  []string `json:"depends_on_features,omitempty"`
	SymbolFQNs         []string `json:"symbol_fqns,omitempty"`
}

// GraphStructuralCheck is a structural check hoisted to graph level with its
// owning feature.
type GraphStructuralCheck struct {
	ID                 string               `json:"id"`
	Feature            string               `json:"feature"`
	Command            []string             `json:"command"`
	VerifiesInvariants []string             `json:"verifies_invariants"`
	Scope              StructuralCheckScope `json:"scope,omitempty"`
}

// CodeGraph references the SCIP indexes; the call/reference graph stays in
// native binary form rather than being inlined.
type CodeGraph struct {
	IndexedBy       string            `json:"indexed_by"`
	LanguageIndexes map[string]string `json:"language_indexes,omitempty"`
}
