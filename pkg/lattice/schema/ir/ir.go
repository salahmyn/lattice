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
	// Parameterized marks a test whose name is a title template: one call
	// site that runs once per row or loop iteration, under names the runner
	// fills in ("rounds %s to %s", "fits on ${view.name}"). Placeholders
	// are kept as written; a report joins each expanded name to it.
	Parameterized bool `json:"parameterized,omitempty"`
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
	// Altered lists the later statements of a change history that touch
	// this symbol's object after the statement that is its effective
	// definition (a SQL ALTER POLICY, ALTER FUNCTION, ...), in the order
	// they apply. A later full redefinition is not one: it replaces the
	// symbol and starts a new list.
	Altered []Alteration `json:"altered,omitempty"`
	// DeclaredAs is the name the symbol's own statement gave it, set only
	// when a later rename changed Name (SQL ALTER ... RENAME TO).
	DeclaredAs string `json:"declared_as,omitempty"`
	// Execute is who can run a SQL function, read from the GRANT, REVOKE and
	// ALTER DEFAULT PRIVILEGES statements of the migrations (SQL functions
	// only; nil for every other symbol).
	Execute *Execute `json:"execute,omitempty"`
}

// Execute states for ExecuteGrant.State.
const (
	ExecGranted = "granted"
	ExecRevoked = "revoked"
	ExecUnknown = "unknown"
)

// MemberAny stands for every role in a Membership: a statement the adapter
// could not read to roles.
const MemberAny = "*"

// Membership is whether Member is a member of Role, with the statement that
// decided it. State is ExecGranted (a member), ExecRevoked or ExecUnknown: a
// membership that does not pass privileges on automatically (WITH INHERIT
// FALSE, a NOINHERIT member), or one a statement the adapter could not read
// may have changed. Member or Role is MemberAny for such a statement.
type Membership struct {
	Member string `json:"member"`
	Role   string `json:"role"`
	State  string `json:"state"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// ExecPublic is the Role of the entry that holds PUBLIC's state.
const ExecPublic = "public"

// Execute is the EXECUTE privilege of one SQL function as the statements
// state it. It holds only what the statements say: which roles are named, and
// what PUBLIC holds. Resolving a role the statements never name needs the
// platform's own defaults, which come from configuration, so the core does it
// (graph.ExecutableBy) and the adapter never guesses.
type Execute struct {
	// SecurityDefiner is true when the function runs with its owner's
	// rights (SECURITY DEFINER, as CREATE or a later ALTER FUNCTION set it).
	SecurityDefiner bool `json:"security_definer,omitempty"`
	// DefinerFile and DefinerLine locate the statement that set it.
	DefinerFile string `json:"definer_file,omitempty"`
	DefinerLine int    `json:"definer_line,omitempty"`
	// Signature is the normalized argument list of the effective CREATE.
	Signature string `json:"signature,omitempty"`
	// Procedure is true for CREATE PROCEDURE.
	Procedure bool `json:"procedure,omitempty"`
	// Roles are the entries the statements decided, sorted by role. The
	// entry for PUBLIC (Role "public") is always present: with Default set
	// it is PostgreSQL's own default, cited at the CREATE line.
	Roles []ExecuteGrant `json:"roles,omitempty"`
	// Unknown, when set, is why no entry can be trusted: an overload, or a
	// statement the adapter could not resolve to this function.
	Unknown string `json:"unknown,omitempty"`
}

// ExecuteGrant is the state of one role, with the statement that decided it.
type ExecuteGrant struct {
	Role  string `json:"role"`
	State string `json:"state"` // ExecGranted, ExecRevoked or ExecUnknown
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	// Default marks PUBLIC's state when no statement named it.
	Default bool `json:"default,omitempty"`
	// Reason says why the state is unknown.
	Reason string `json:"reason,omitempty"`
}

// Alteration is one statement that changes an object after its definition.
type Alteration struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line,omitempty"`
	// Sets says what the statement changes: "using" and "with_check" (the
	// policy expressions it sets), "to" (the roles), "rename", or "other".
	Sets []string `json:"sets,omitempty"`
}

// Reference kinds (Ref.Kind).
const (
	RefCall  = "call"  // a call: f(), obj.m()
	RefNew   = "new"   // a construction: new C(), T{} in Go
	RefRPC   = "rpc"   // a database function by name: .rpc('name')
	RefTable = "table" // a database table by name: .from('table')
	// RefHTTP is a request a client sends: fetch('/api/x'), axios.get('/api/x'),
	// supabase.functions.invoke('x'). Name is the key, "METHOD /path": the
	// method upper-case, the path with no origin, query or trailing slash
	// (see HTTPKey). Name is "" when the URL or the method is not a literal.
	RefHTTP  = "http"
	RefJSX   = "jsx" // a JSX component: <Component />
	RefValue = "ref" // a function, type or value named without being called (a callback, a type)
	// RefImport is a module loaded at run time: import('./x'), require('./x')
	// where no binding says which exports are used. Name is the specifier,
	// "" when it is not a literal; the module's Import of that specifier
	// (Dynamic) carries its resolution.
	RefImport = "import"
	// RefPublish is the out end of a channel: a job enqueued, a message sent,
	// a NOTIFY. Name is the key "<kind>:<name>" (see ChannelKey), "" when the
	// channel name is not a literal.
	RefPublish = "publish"
	// RefConsume is the in end of a channel: the code that owns the reference
	// (or the function Handler names) runs when a message arrives. Name is
	// the key, "" when the channel name is not a literal.
	RefConsume = "consume"
)

// Ref is one raw reference in a symbol's body or a module's top-level code.
type Ref struct {
	// Name is the referenced name; "" for an .rpc() or a request whose
	// argument is not a literal.
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
	// Handler, on a consume ref, is the bare name of the function the call
	// registers as the handler (new Worker('q', handler)); the core resolves
	// it in the owner's scope. Empty when the handler is not a named
	// function, and then the owner of the reference is the consumer.
	Handler string `json:"handler,omitempty"`
	// LitStart and LitEnd are the byte offsets, in the file as parsed, of
	// the string literal (quotes included) that carries a request, database
	// or channel name: the out end of an http, rpc, table or publish
	// reference. Both are 0 when the name is not a literal in source or the
	// adapter does not record where it is. A consumer that alters the key
	// edits exactly this span and nothing else.
	LitStart int `json:"lit_start,omitempty"`
	LitEnd   int `json:"lit_end,omitempty"`
	// Secret, on a request with no name, is the Vault secret the URL was
	// read whole from when no http.secrets entry declares its route: the
	// finding for a computed URL names it, so it can be declared.
	Secret string `json:"secret,omitempty"`
}

// HasLiteral reports whether the reference records where its literal is.
func (r Ref) HasLiteral() bool { return r.LitEnd > r.LitStart && r.LitStart >= 0 }

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
	// Dynamic marks a module loaded at run time (import('./x')); the code
	// that loads it holds a RefImport, so the import is no edge of the
	// module's top-level code.
	Dynamic bool `json:"dynamic,omitempty"`
	Line    int  `json:"line"`
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
	// Job and Schedule are a cron job's name and schedule, when the source
	// gives them as literals. KeyComputed marks a job whose name or schedule
	// is an expression, so the job has no stable key.
	Job         string `json:"job,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	KeyComputed bool   `json:"key_computed,omitempty"`
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
	// Memberships are the role memberships a change history leaves in
	// force (GRANT role TO role, CREATE ROLE ... IN ROLE), folded across
	// every migration in apply order. Membership belongs to no one
	// function, so it sits on the module, not on Symbol.Execute; the SQL
	// adapter writes the whole set on one module and the others carry none.
	Memberships []Membership `json:"memberships,omitempty"`

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
