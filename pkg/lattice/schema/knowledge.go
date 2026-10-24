package schema

import "strings"

// KnowledgeGraph is the single artifact Lattice emits as lattice.json.
//
// It is committed to git and written with deterministic ordering: extracting
// twice from the same input produces byte-identical output.
type KnowledgeGraph struct {
	SchemaVersion       string `json:"schema_version"`
	GeneratedAt         string `json:"generated_at"`
	GeneratedFromCommit string `json:"generated_from_commit"`

	// BRDs is the v0.5.0 business-intent layer. Optional and empty for
	// projects that haven't adopted the BRD axis; the validator never
	// errors on absence (FEATURE_NO_BRD is warning-level only).
	BRDs             []BRD                  `json:"brds,omitempty"`
	Features         []Manifest             `json:"features"`
	EntryPoints      []EntryPoint           `json:"entry_points,omitempty"`
	Symbols          []GraphSymbol          `json:"symbols"`
	Tests            []GraphSymbol          `json:"tests"`
	Modules          []GraphModule          `json:"modules"`
	Surfaces         []GraphSurface         `json:"surfaces"`
	Errors           []GraphError           `json:"errors"`
	Initiatives      []Initiative           `json:"initiatives"`
	Tasks            []Task                 `json:"tasks"`
	StructuralChecks []GraphStructuralCheck `json:"structural_checks"`

	CodeGraph  CodeGraph   `json:"code_graph"`
	Violations []Violation `json:"violations"`

	// Blocks, BlockEdges and Sources are the v0.9 block graph (rnd/0003),
	// written by `lattice refresh`. Omitted when empty so graphs without
	// block sources stay byte-identical.
	Blocks     []Block       `json:"blocks,omitempty"`
	BlockEdges []BlockEdge   `json:"block_edges,omitempty"`
	Sources    []SourceStamp `json:"sources,omitempty"`
	Flows      []Flow        `json:"flows,omitempty"`

	// Review marks a graph built without source code (manifest-only).
	Review bool `json:"review,omitempty"`
}

// GraphSymbol is a code (or test) symbol with its resolved Lattice edges.
type GraphSymbol struct {
	Name         string `json:"name"`
	FQN          string `json:"fqn"`
	Kind         string `json:"kind"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	EndLine      int    `json:"end_line,omitempty"`
	Language     string `json:"language"`
	EnclosingFQN string `json:"enclosing_fqn,omitempty"`
	IsTest       bool   `json:"is_test"`
	Exported     bool   `json:"exported"`

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
	LineCount          int      `json:"line_count,omitempty"` // v0.7 — for FILE_LINE_CAP
	HasCode            bool     `json:"has_code,omitempty"`   // top-level runtime code outside any symbol
	ChangeHistory      bool     `json:"change_history,omitempty"` // a migration: attributed per symbol, not as a module
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

// GraphSurface is one user- or system-facing interaction, fusing what a
// manifest declares with what the code actually exposes. It is the
// machine-readable interaction inventory: every HTTP route, event, webhook,
// and scheduled job, and whether declaration and implementation agree.
type GraphSurface struct {
	Type          string        `json:"type"`
	Method        string        `json:"method,omitempty"`
	Path          string        `json:"path,omitempty"`
	Name          string        `json:"name,omitempty"`
	Feature       string        `json:"feature,omitempty"`
	Declared      bool          `json:"declared"`    // present in a feature manifest
	Implemented   bool          `json:"implemented"` // found in source
	ImplementedBy []SurfaceImpl `json:"implemented_by,omitempty"`
}

// SurfaceImpl is one code site that exposes a surface.
type SurfaceImpl struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Symbol   string `json:"symbol,omitempty"`
	Detected bool   `json:"detected"` // auto-detected from a framework call
}

// GraphError is one entry of a feature's error/response contract, fusing what
// the manifest declares with the @error annotations found in code.
type GraphError struct {
	Code        string        `json:"code"`
	Status      int           `json:"status,omitempty"`
	Description string        `json:"description,omitempty"`
	Feature     string        `json:"feature,omitempty"`
	Declared    bool          `json:"declared"`    // present in a feature manifest
	Implemented bool          `json:"implemented"` // raised by annotated code
	RaisedBy    []SurfaceImpl `json:"raised_by,omitempty"`
}

// CodeGraph references the SCIP indexes; the call/reference graph stays in
// native binary form rather than being inlined.
type CodeGraph struct {
	IndexedBy       string            `json:"indexed_by"`
	LanguageIndexes map[string]string `json:"language_indexes,omitempty"`
}

// ModuleAttribution says whether one module is production code and whether
// it belongs to a feature.
type ModuleAttribution struct {
	File string
	// ProductionSymbols counts the module's non-test symbols that carry
	// behaviour or state (functions, methods, classes, traits, consts).
	// A module with none (tests only, or interfaces and type aliases only)
	// is not production code for attribution, unless it has top-level code.
	ProductionSymbols int
	// Production is true for a module with a production symbol or with
	// top-level runtime code (GraphModule.HasCode), so an entry file that
	// only calls render() or a module of plain value consts still counts.
	Production bool
	Attributed bool
}

// behaviourKinds are the symbol kinds that make a module production code.
var behaviourKinds = map[string]bool{
	"function": true, "method": true, "class": true, "trait": true, "const": true,
}

// Attribution classifies every module of the graph. A module is attributed
// by its @module-feature or by a feature on any of its symbols. It is the
// one definition behind UNATTRIBUTED_MODULE and `lattice coverage`.
func (kg KnowledgeGraph) Attribution() []ModuleAttribution {
	sym := map[string]GraphSymbol{}
	for _, s := range kg.Symbols {
		sym[s.FQN] = s
	}
	out := make([]ModuleAttribution, 0, len(kg.Modules))
	for _, m := range kg.Modules {
		a := ModuleAttribution{File: m.File, Attributed: m.Feature != ""}
		if nonProductFile(m.File) || m.ChangeHistory {
			out = append(out, a) // ProductionSymbols stays 0
			continue
		}
		for _, fqn := range m.SymbolFQNs {
			s, ok := sym[fqn] // tests live in kg.Tests, so they are not found here
			if !ok {
				continue
			}
			if behaviourKinds[s.Kind] {
				a.ProductionSymbols++
			}
			if s.Feature != "" {
				a.Attributed = true
			}
		}
		a.Production = a.ProductionSymbols > 0 || m.HasCode
		out = append(out, a)
	}
	return out
}

// nonProductFile reports files that hold code but not product behaviour:
// ambient type declarations (*.d.ts) and tool configuration
// (vite.config.ts, playwright.config.ts, jest.config.js, …). They are left
// out of attribution so a coverage figure measures the product, not its
// build setup; a workspace that wants them counted can attribute them anyway.
func nonProductFile(file string) bool {
	base := file
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasSuffix(base, ".d.ts") || strings.HasSuffix(base, ".d.mts") || strings.HasSuffix(base, ".d.cts") {
		return true
	}
	stem := base
	if i := strings.LastIndex(stem, "."); i > 0 {
		stem = stem[:i]
	}
	return strings.HasSuffix(stem, ".config")
}
