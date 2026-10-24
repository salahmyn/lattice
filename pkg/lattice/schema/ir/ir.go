// Package ir defines the intermediate representation produced by language
// adapters and consumed by the Lattice core.
//
// The IR is deliberately language-agnostic: an adapter's only job is to turn
// source text into these structs. Everything downstream — graph building,
// validation, analysis — operates on IR and never imports a parser.
package ir

// @module-feature schema

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
	Name         string     `json:"name"`
	FQN          string     `json:"fqn"` // adapter-determined, globally unique
	Kind         SymbolKind `json:"kind"`
	File         string     `json:"file"`
	Line         int        `json:"line"`
	EndLine      int        `json:"end_line,omitempty"`      // last line of the declaration, when the adapter knows it
	EnclosingFQN string     `json:"enclosing_fqn,omitempty"` // for methods
	BaseClasses  []string   `json:"base_classes,omitempty"`  // FQNs of bases
	IsTest       bool       `json:"is_test"`
	// Exported reports whether the symbol is part of its module's public
	// surface (TS `export`, a non-underscore Python name, a non-private PHP
	// member). Private helpers are kept in the graph but excluded from a
	// feature's implementation edges.
	Exported    bool         `json:"exported"`
	Annotations []Annotation `json:"annotations,omitempty"`
	// Refs are the raw references in the symbol's body (calls, `new`,
	// `.rpc('x')`, `.from('t')`, JSX components, named values), as the
	// adapter saw them. The core resolves them into call edges
	// (docs/rnd/0009); an adapter never guesses a target.
	Refs []Ref `json:"refs,omitempty"`
	// Root says why the runtime invokes this symbol on its own, with no
	// caller in the code: "main" (Go's func main in package main),
	// "trigger" or "policy" (the database runs them). Empty for others.
	Root string `json:"root,omitempty"`
}

// Reference kinds (Ref.Kind).
const (
	RefCall  = "call"  // a call: f(), obj.m()
	RefNew   = "new"   // a construction: new C(), T{} in Go
	RefRPC   = "rpc"   // a database function by name: .rpc('name')
	RefTable = "table" // a database table by name: .from('table')
	RefJSX   = "jsx"   // a JSX component: <Component />
	RefValue = "ref"   // a function, type or value named without being called (a callback, a type)
)

// Ref is one raw reference in a symbol's body or a module's top-level code.
type Ref struct {
	// Name is the referenced name; "" for an .rpc() whose argument is not
	// a literal.
	Name string `json:"name"`
	// Qualifier is what the name is reached through: an import binding
	// ("results" in results.Lookup()), "this" for the enclosing type (this,
	// self, a Go receiver), "~" for a value of unknown type, or a schema
	// for rpc and table refs.
	Qualifier string `json:"qualifier,omitempty"`
	// Target is an FQN the adapter resolved itself, from the language's
	// own rules (SQL names are global). The core only checks it exists.
	Target string `json:"target,omitempty"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
}

// Import is one import (or re-export) of a module.
type Import struct {
	// Name is the local binding: an alias, a default or namespace name, a
	// Go package name, "." for a dot import. "" for a side-effect import,
	// a dynamic import() and a re-export.
	Name string `json:"name,omitempty"`
	// From is the module specifier as written ("./wallet.js",
	// "github.com/x/y/pkg"); "" for a local `export { a as b }`.
	From string `json:"from,omitempty"`
	// Member is the export imported: a name, "default", "*" for every
	// export (export *), or "" for the module itself (a namespace import, a
	// Go package).
	Member string `json:"member,omitempty"`
	// Export is the name this module re-exports the import under
	// (export { a as b } from './x'), "*" for export * from.
	Export string `json:"export,omitempty"`
	// Module is the workspace namespace From resolves to (a module's or
	// package's Namespace), set by the adapter once it has seen every file;
	// "" when the target is outside the workspace.
	Module string `json:"module,omitempty"`
	Line   int    `json:"line"`
}

// Node kinds (Node.Kind).
const (
	NodeTop   = "top"   // a module's top-level code
	NodeCron  = "cron"  // a scheduled job body (cron.schedule)
	NodeServe = "serve" // a request handler passed to Deno.serve / serve
	NodeTable = "table" // a database table: reaches its policies and triggers
)

// Node is code the call graph treats like a symbol without it being one:
// a module's top-level code (<namespace>.<top>), a cron job, a table. Nodes
// never reach lattice.json; they hold references and can be roots.
type Node struct {
	FQN  string `json:"fqn"`
	Kind string `json:"kind"`
	// Root says why the runtime runs the node on its own ("cron",
	// "serve"); empty for a node that is reached only through edges.
	Root   string `json:"root,omitempty"`
	IsTest bool   `json:"is_test,omitempty"`
	Line   int    `json:"line"`
	Refs   []Ref  `json:"refs,omitempty"`
}

// TopFQN is the FQN of a namespace's top-level code: <namespace>.<top>, or
// <namespace>.<test> for the top-level code of its test files.
func TopFQN(namespace string, test bool) string {
	if test {
		return namespace + ".<test>"
	}
	return namespace + ".<top>"
}

// Diagnostic is a non-fatal issue an adapter found while parsing — e.g. an
// annotation argument that is not a string literal. Diagnostics are turned
// into validation violations by the extract pipeline.
type Diagnostic struct {
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Surface is a user- or system-facing interaction point a module exposes —
// an HTTP route, an emitted/consumed event, a webhook, a scheduled job.
// Adapters populate these from framework route-registration calls
// (Detected=true) and from @surface annotations (Detected=false).
type Surface struct {
	Type     string `json:"type"`             // http|event_emit|event_consume|webhook_receive|scheduled
	Method   string `json:"method,omitempty"` // http / webhook
	Path     string `json:"path,omitempty"`   // http / webhook
	Name     string `json:"name,omitempty"`   // event / scheduled
	Symbol   string `json:"symbol,omitempty"` // FQN of the implementing symbol, when known
	Line     int    `json:"line"`
	Detected bool   `json:"detected,omitempty"` // auto-detected from a framework call
}

// Module is the IR for a single source file.
type Module struct {
	File     string `json:"file"`
	Language string `json:"language"`
	// LineCount is the source-file length. Populated by the extractor
	// (counted from the raw bytes), not by each adapter — gives the
	// v0.7 AMA FILE_LINE_CAP rule a value without touching every
	// adapter implementation.
	LineCount int `json:"line_count,omitempty"`
	// HasCode reports top-level runtime code outside any symbol (a render
	// call, an unannotated value const). It makes a module with no
	// behaviour symbols production code for attribution. Set by adapters
	// that can tell it apart from imports and type declarations; never set
	// on test files.
	HasCode bool `json:"has_code,omitempty"`
	// ChangeHistory marks a file that records changes rather than holding
	// code, such as a database migration: its symbols are attributed by
	// their own tags, and the file itself is not a module to attribute.
	ChangeHistory     bool         `json:"change_history,omitempty"`
	ModuleAnnotations []Annotation `json:"module_annotations,omitempty"`
	Symbols           []Symbol     `json:"symbols,omitempty"`
	Surfaces          []Surface    `json:"surfaces,omitempty"`
	Diagnostics       []Diagnostic `json:"diagnostics,omitempty"`

	// The fields below feed the call graph (docs/rnd/0009). An adapter
	// that does not extract references leaves them empty and RefsKnown
	// false, and the core then never calls its symbols unreached.

	// RefsKnown reports that the adapter extracted this module's
	// references, so a symbol nothing references is really unreferenced.
	RefsKnown bool `json:"refs_known,omitempty"`
	// Namespace is the scope a bare name resolves in: the module path for
	// TS/JS and Python, the package directory for Go. Symbols of the
	// module are <Namespace>.<name>.
	Namespace string `json:"namespace,omitempty"`
	// Package is the package clause name, for languages that have one (Go).
	Package string `json:"package,omitempty"`
	// DefaultExport is the local name of the module's default export
	// (export default Foo), when it names one.
	DefaultExport string   `json:"default_export,omitempty"`
	Imports       []Import `json:"imports,omitempty"`
	// Nodes are the module's top-level code, cron jobs, serve handlers and
	// tables, with their references.
	Nodes []Node `json:"nodes,omitempty"`
}
