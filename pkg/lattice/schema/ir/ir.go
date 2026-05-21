// Package ir defines the intermediate representation produced by language
// adapters and consumed by the Lattice core.
//
// The IR is deliberately language-agnostic: an adapter's only job is to turn
// source text into these structs. Everything downstream — graph building,
// validation, analysis — operates on IR and never imports a parser.
package ir

// Annotation is one Lattice annotation attached to a symbol or module.
//
// Args holds positional arguments (each a string or []string). Kwargs holds
// keyword arguments. Adapters normalize their language's annotation syntax
// into this shape.
type Annotation struct {
	Kind   string                 `json:"kind"`
	Args   []interface{}          `json:"args,omitempty"`
	Kwargs map[string]interface{} `json:"kwargs,omitempty"`
	Line   int                    `json:"line"`
}

// SymbolKind enumerates the kinds of code symbol an adapter reports.
type SymbolKind string

const (
	KindClass     SymbolKind = "class"
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindConst     SymbolKind = "const"
	KindInterface SymbolKind = "interface"
	KindTrait     SymbolKind = "trait"
)

// Symbol is one named code construct.
type Symbol struct {
	Name         string       `json:"name"`
	FQN          string       `json:"fqn"` // adapter-determined, globally unique
	Kind         SymbolKind   `json:"kind"`
	File         string       `json:"file"`
	Line         int          `json:"line"`
	EnclosingFQN string       `json:"enclosing_fqn,omitempty"` // for methods
	BaseClasses  []string     `json:"base_classes,omitempty"`  // FQNs of bases
	IsTest       bool         `json:"is_test"`
	Annotations  []Annotation `json:"annotations,omitempty"`
}

// Diagnostic is a non-fatal issue an adapter found while parsing — e.g. an
// annotation argument that is not a string literal. Diagnostics are turned
// into validation violations by the extract pipeline.
type Diagnostic struct {
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Module is the IR for a single source file.
type Module struct {
	File              string       `json:"file"`
	Language          string       `json:"language"`
	ModuleAnnotations []Annotation `json:"module_annotations,omitempty"`
	Symbols           []Symbol     `json:"symbols,omitempty"`
	Diagnostics       []Diagnostic `json:"diagnostics,omitempty"`
}
