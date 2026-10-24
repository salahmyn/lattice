// Package reach builds Lattice's call graph from the raw references the
// language adapters emit, finds the roots the runtime starts from, and
// classifies every runtime symbol by what reaches it (docs/rnd/0009, phase
// 2). Resolution is cautious: a reference resolves through the module's
// imports, then its own scope, then, for a method called on a value of
// unknown type, a member name that is unique in its language. Anything
// else stays unresolved and is counted, never guessed. A SCIP index, when
// one exists for a language, replaces this resolution for that language.
package reach

// @module-feature reachability

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
)

// Edge is one resolved reference: From's code names To.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is the reference kind (call, new, rpc, table, jsx, ref), or
	// import (a module loads another), member (a member implies its
	// type), use (a dynamic import uses an export) or scip.
	Kind string `json:"kind"`
	// Via is how it resolved: target (the adapter named the FQN), import,
	// scope (the module's own names), this (the enclosing type), unique (a
	// member name unique in the language), structure, or scip.
	Via  string `json:"via"`
	Line int    `json:"line,omitempty"`
}

// Unresolved is a reference left without a target.
type Unresolved struct {
	From      string `json:"from"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	Qualifier string `json:"qualifier,omitempty"`
	// Why: ambiguous (several members share the name), missing (it names
	// a module of the workspace, or a database function, that has no such
	// symbol) or dynamic (an .rpc() whose name is not a literal).
	Why        string   `json:"why"`
	Candidates []string `json:"candidates,omitempty"`
}

// Stats counts what resolution did.
type Stats struct {
	Refs       int `json:"refs"`
	Resolved   int `json:"resolved"`
	Unresolved int `json:"unresolved"`
	Ambiguous  int `json:"ambiguous"`
	Missing    int `json:"missing"`
	Dynamic    int `json:"dynamic"`
	// External refs name nothing in the workspace: the standard library,
	// a dependency, a builtin, a local variable the adapter could not tell
	// apart. They are not unresolved.
	External int `json:"external"`
	// SCIPEdges are edges a SCIP index gave, for the languages it covers.
	SCIPEdges int            `json:"scip_edges,omitempty"`
	SCIP      []string       `json:"scip_languages,omitempty"`
	ByLang    map[string]int `json:"refs_by_language,omitempty"`
}

// node is a symbol or pseudo-symbol of the graph.
type node struct {
	fqn, name, kind, file, lang, enclosing string
	namespace                              string
	isTest, exported, symbol, refsKnown    bool
	root                                   string
	bases                                  []string
	mod                                    int // module index
}

// Graph is the resolved call graph.
type Graph struct {
	Edges      []Edge
	Unresolved []Unresolved
	Stats      Stats

	nodes map[string]*node
	out   map[string][]string // resolved edges
	may   map[string][]string // ambiguous references, to every candidate
	// imported holds the symbols another namespace imports or references.
	imported map[string]bool
	modules  []ir.Module
	byNS     map[string][]int // namespace -> module indexes
	tops     map[int]string   // module index -> its top node
	rev      map[string][]string
}

// Options tunes Build.
type Options struct {
	// SCIP maps a language to its index. A language with one is resolved
	// from the index alone; its adapter references are not read.
	SCIP map[string]*scip.Corpus
}

type builder struct {
	g       *Graph
	members map[string]map[string][]string // language -> member name -> FQNs
	edgeSet map[string]bool
}

// Build resolves every module's references into a call graph.
// @enforces reachability:INV-2
func Build(modules []ir.Module, opts Options) *Graph {
	g := &Graph{nodes: map[string]*node{}, out: map[string][]string{}, may: map[string][]string{},
		imported: map[string]bool{}, modules: modules, byNS: map[string][]int{}, tops: map[int]string{},
		Stats: Stats{ByLang: map[string]int{}}}
	b := &builder{g: g, members: map[string]map[string][]string{}, edgeSet: map[string]bool{}}

	for mi := range modules {
		m := &modules[mi]
		if m.Namespace != "" {
			g.byNS[m.Namespace] = append(g.byNS[m.Namespace], mi)
		}
		for _, s := range m.Symbols {
			n := &node{fqn: s.FQN, name: s.Name, kind: string(s.Kind), file: s.File, lang: m.Language,
				enclosing: s.EnclosingFQN, namespace: m.Namespace, isTest: s.IsTest, exported: s.Exported,
				symbol: true, refsKnown: m.RefsKnown, root: s.Root, bases: s.BaseClasses, mod: mi}
			if _, dup := g.nodes[s.FQN]; !dup {
				g.nodes[s.FQN] = n
			}
			if s.EnclosingFQN != "" {
				if b.members[m.Language] == nil {
					b.members[m.Language] = map[string][]string{}
				}
				last := lastSegment(s.FQN)
				b.members[m.Language][last] = append(b.members[m.Language][last], s.FQN)
			}
		}
		for _, nd := range m.Nodes {
			if have, ok := g.nodes[nd.FQN]; ok {
				if have.root == "" {
					have.root = nd.Root
				}
			} else {
				g.nodes[nd.FQN] = &node{fqn: nd.FQN, name: nd.FQN, kind: nd.Kind, file: m.File, lang: m.Language,
					namespace: m.Namespace, isTest: nd.IsTest, refsKnown: m.RefsKnown, root: nd.Root, mod: mi}
			}
			if nd.Kind == ir.NodeTop {
				g.tops[mi] = nd.FQN
			}
		}
	}
	for _, list := range b.members {
		for k := range list {
			sort.Strings(list[k])
		}
	}

	scipLangs := map[string]bool{}
	for lang, c := range opts.SCIP {
		if c != nil && !c.Empty() {
			scipLangs[lang] = true
			g.Stats.SCIP = append(g.Stats.SCIP, lang)
		}
	}
	sort.Strings(g.Stats.SCIP)

	for mi := range modules {
		m := &modules[mi]
		if !m.RefsKnown {
			continue
		}
		top := g.tops[mi]
		for _, im := range m.Imports {
			b.importEdges(mi, top, im)
		}
		if scipLangs[m.Language] {
			continue
		}
		mc := b.context(mi)
		for _, s := range m.Symbols {
			for _, r := range s.Refs {
				b.resolve(mc, g.nodes[s.FQN], s.FQN, r)
			}
		}
		for _, nd := range m.Nodes {
			for _, r := range nd.Refs {
				b.resolve(mc, g.nodes[nd.FQN], nd.FQN, r)
			}
		}
	}
	for lang := range scipLangs {
		b.scipEdges(lang, opts.SCIP[lang])
	}

	// A member that is reached makes its type used.
	for _, n := range g.nodes {
		if n.symbol && n.enclosing != "" {
			if _, ok := g.nodes[n.enclosing]; ok {
				b.addEdge(Edge{From: n.fqn, To: n.enclosing, Kind: "member", Via: "structure"})
			}
		}
	}

	sort.Slice(g.Edges, func(i, j int) bool {
		a, c := g.Edges[i], g.Edges[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.To != c.To {
			return a.To < c.To
		}
		return a.Kind < c.Kind
	})
	sort.SliceStable(g.Unresolved, func(i, j int) bool {
		a, c := g.Unresolved[i], g.Unresolved[j]
		if a.File != c.File {
			return a.File < c.File
		}
		if a.Line != c.Line {
			return a.Line < c.Line
		}
		return a.Name < c.Name
	})
	for k := range g.out {
		sort.Strings(g.out[k])
	}
	for k := range g.may {
		sort.Strings(g.may[k])
	}
	return g
}

func lastSegment(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

func (b *builder) addEdge(e Edge) {
	key := e.From + "\x00" + e.To + "\x00" + e.Kind
	if b.edgeSet[key] || e.From == e.To {
		return
	}
	b.edgeSet[key] = true
	b.g.Edges = append(b.g.Edges, e)
	b.g.out[e.From] = append(b.g.out[e.From], e.To)
	from, to := b.g.nodes[e.From], b.g.nodes[e.To]
	if from != nil && to != nil && to.symbol && from.namespace != to.namespace && e.Kind != "member" {
		b.g.imported[e.To] = true
	}
}

func (b *builder) exists(fqn string) bool {
	_, ok := b.g.nodes[fqn]
	return ok
}

// topOf returns the top node of a namespace, or "" when it has none.
func (b *builder) topOf(ns string) string {
	for _, mi := range b.g.byNS[ns] {
		if t, ok := b.g.tops[mi]; ok && !b.g.nodes[t].isTest {
			return t
		}
	}
	return ""
}

// importEdges adds the edges an import implies: the importing module loads
// the imported one (its top-level code runs), a dynamic import() uses the
// export it names, and a named import marks its target imported.
func (b *builder) importEdges(mi int, top string, im ir.Import) {
	if im.Module == "" || top == "" || im.Dynamic {
		return // a dynamic import is an edge of the code that loads it
	}
	g := b.g
	if t := b.topOf(im.Module); t != "" && im.Module != g.modules[mi].Namespace {
		b.addEdge(Edge{From: top, To: t, Kind: "import", Via: "import", Line: im.Line})
	}
	if im.Member == "" || im.Member == "*" {
		return
	}
	fqn, _ := b.member(im.Module, im.Member, 0)
	if fqn == "" {
		return
	}
	if im.Module != g.modules[mi].Namespace {
		g.imported[fqn] = true
	}
	if im.Name == "" && im.Export == "" {
		b.addEdge(Edge{From: top, To: fqn, Kind: "use", Via: "import", Line: im.Line})
	}
}

// member resolves an export of a namespace to a symbol FQN, or to a
// namespace when the name is a submodule (Python's from pkg import mod).
// It follows re-exports (export { a as b } from, export * from, and a
// binding the target module itself imports) up to a small depth.
func (b *builder) member(ns, name string, depth int) (fqn, namespace string) {
	if depth > 8 || ns == "" || name == "" {
		return "", ""
	}
	g := b.g
	if name == "default" {
		for _, mi := range g.byNS[ns] {
			if d := g.modules[mi].DefaultExport; d != "" {
				return b.member(ns, d, depth+1)
			}
		}
		return b.topOf(ns), ""
	}
	if b.exists(ns + "." + name) {
		return ns + "." + name, ""
	}
	if _, ok := g.byNS[ns+"."+name]; ok {
		return "", ns + "." + name
	}
	for _, mi := range g.byNS[ns] {
		for _, im := range g.modules[mi].Imports {
			if im.Module == "" {
				continue
			}
			switch {
			case im.Export == name, im.Export == "" && im.Name == name:
				if im.Member == "" {
					return "", im.Module
				}
				if f, n := b.member(im.Module, im.Member, depth+1); f != "" || n != "" {
					return f, n
				}
			case im.Export == "*" && im.Member == "*":
				if f, n := b.member(im.Module, name, depth+1); f != "" || n != "" {
					return f, n
				}
			}
		}
	}
	return "", ""
}

// exportsOf lists a namespace's exported symbols (methods of exported
// types too) and what it re-exports from other modules.
func (b *builder) exportsOf(ns string) []string {
	var out []string
	for _, mi := range b.g.byNS[ns] {
		for _, s := range b.g.modules[mi].Symbols {
			if s.Exported && !s.IsTest {
				out = append(out, s.FQN)
			}
		}
		out = append(out, b.g.reexported(mi)...)
	}
	return out
}

// binding is what a local import name stands for: a symbol or a namespace.
type binding struct {
	fqn, ns  string
	external bool
}

// modCtx is what resolution needs about one module: its import bindings
// and the namespaces it dot-imports.
type modCtx struct {
	mi    int
	mod   *ir.Module
	binds map[string]binding
	dots  []string
	// dynamic maps a specifier loaded at run time to its namespace ("" when
	// it is outside the workspace).
	dynamic map[string]string
}

func (b *builder) context(mi int) *modCtx {
	mc := &modCtx{mi: mi, mod: &b.g.modules[mi], binds: map[string]binding{}, dynamic: map[string]string{}}
	for _, im := range mc.mod.Imports {
		if im.Dynamic {
			mc.dynamic[im.From] = im.Module
			continue
		}
		switch {
		case im.Name == "" || im.Name == "_":
		case im.Name == ".":
			if im.Module != "" {
				mc.dots = append(mc.dots, im.Module)
			}
		case im.Module == "":
			mc.binds[im.Name] = binding{external: true}
		case im.Member == "":
			mc.binds[im.Name] = binding{ns: im.Module}
		default:
			f, n := b.member(im.Module, im.Member, 0)
			mc.binds[im.Name] = binding{fqn: f, ns: n, external: f == "" && n == ""}
		}
	}
	return mc
}

// resolve resolves one reference of from (a symbol or node of module mi).
// @enforces reachability:INV-2
func (b *builder) resolve(mc *modCtx, owner *node, from string, r ir.Ref) {
	g := b.g
	m := mc.mod
	g.Stats.Refs++
	g.Stats.ByLang[m.Language]++
	resolved := func(to, via string) {
		g.Stats.Resolved++
		b.addEdge(Edge{From: from, To: to, Kind: r.Kind, Via: via, Line: r.Line})
	}
	unresolved := func(why string, cands []string) {
		g.Stats.Unresolved++
		switch why {
		case "ambiguous":
			g.Stats.Ambiguous++
			g.may[from] = append(g.may[from], cands...)
		case "missing":
			g.Stats.Missing++
		case "dynamic":
			g.Stats.Dynamic++
		}
		g.Unresolved = append(g.Unresolved, Unresolved{From: from, File: m.File, Line: r.Line, Kind: r.Kind,
			Name: r.Name, Qualifier: r.Qualifier, Why: why, Candidates: cands})
	}
	external := func() { g.Stats.External++ }

	switch {
	case r.Kind == ir.RefImport:
		// import('./x'): the module loads, and which of its exports the
		// caller then uses is not told apart, so all of them are reached.
		if r.Name == "" {
			unresolved("dynamic", nil)
			return
		}
		ns := mc.dynamic[r.Name]
		if ns == "" {
			external()
			return
		}
		g.Stats.Resolved++
		if t := b.topOf(ns); t != "" {
			b.addEdge(Edge{From: from, To: t, Kind: ir.RefImport, Via: "import", Line: r.Line})
		}
		for _, f := range b.exportsOf(ns) {
			b.addEdge(Edge{From: from, To: f, Kind: "use", Via: "import", Line: r.Line})
		}
		return
	case r.Kind == ir.RefRPC:
		if r.Name == "" {
			unresolved("dynamic", nil)
			return
		}
		schema := r.Qualifier
		if schema == "" {
			schema = "public"
		}
		if t := "sql." + schema + "." + r.Name; b.exists(t) {
			resolved(t, "target")
		} else {
			unresolved("missing", nil)
		}
		return
	case r.Kind == ir.RefTable:
		schema := r.Qualifier
		if schema == "" {
			schema = "public"
		}
		if t := "sql.table." + schema + "." + r.Name; b.exists(t) {
			resolved(t, "target")
		} else {
			external() // a table with no policy or trigger runs no code
		}
		return
	case r.Target != "":
		if b.exists(r.Target) {
			resolved(r.Target, "target")
		} else {
			external()
		}
		return
	}
	if owner == nil {
		external()
		return
	}
	binds := mc.binds
	callLike := r.Kind == ir.RefCall || r.Kind == ir.RefNew || r.Kind == ir.RefJSX

	if r.Qualifier == "" {
		if bd, ok := binds[r.Name]; ok {
			switch {
			case bd.external:
				external()
			case bd.fqn != "":
				resolved(bd.fqn, "import")
			case bd.ns != "":
				if t := b.topOf(bd.ns); t != "" {
					resolved(t, "import")
				} else {
					external()
				}
			}
			return
		}
		for _, scope := range b.scopes(owner, m.Namespace) {
			if t := scope + "." + r.Name; b.exists(t) {
				resolved(t, "scope")
				return
			}
		}
		for _, ns := range mc.dots {
			if t := ns + "." + r.Name; b.exists(t) {
				resolved(t, "import")
				return
			}
		}
		external()
		return
	}

	// A value whose declared type the adapter read (Go's func f(kg
	// schema.KnowledgeGraph), x := &T{}): the member of that type. A type
	// outside the workspace makes the reference external; a workspace type
	// without that member (an interface, a promoted method) leaves a call
	// to the member name.
	if t, ok := strings.CutPrefix(r.Qualifier, "type:"); ok {
		typ := b.resolveType(mc, owner, t)
		switch {
		case typ == "":
			external()
		case b.exists(typ + "." + r.Name):
			resolved(typ+"."+r.Name, "type")
		case callLike:
			b.byMember(m.Language, owner, r.Name, resolved, unresolved, external)
		default:
			external()
		}
		return
	}

	// The result of a call through an import (time.Now().Add, x :=
	// os.Open(p); x.Close()): external when the import is, else a value of
	// unknown type.
	if t, ok := strings.CutPrefix(r.Qualifier, "result:"); ok {
		head, _, _ := strings.Cut(t, ".")
		if bd, ok := binds[head]; ok && bd.external || !callLike {
			external()
			return
		}
		b.byMember(m.Language, owner, r.Name, resolved, unresolved, external)
		return
	}

	// The qualifier is looked up whole first (Python binds `import a.b`
	// as a.b), then by its first segment.
	head, rest := r.Qualifier, ""
	if _, whole := binds[head]; !whole {
		if i := strings.IndexByte(head, '.'); i >= 0 {
			head, rest = head[:i], head[i+1:]
		}
	}
	if r.Qualifier == "this" {
		if cls := b.enclosingType(owner); cls != "" {
			for _, c := range append([]string{cls}, b.basesOf(cls, binds)...) {
				if t := c + "." + r.Name; b.exists(t) {
					resolved(t, "this")
					return
				}
			}
		}
		if callLike {
			b.byMember(m.Language, owner, r.Name, resolved, unresolved, external)
			return
		}
		external()
		return
	}
	if bd, ok := binds[head]; ok && head != "~" {
		switch {
		case bd.external:
			external()
			return
		case bd.ns != "":
			t := bd.ns
			if rest != "" {
				t += "." + rest
			}
			t += "." + r.Name
			if b.exists(t) {
				resolved(t, "import")
				return
			}
			if f, _ := b.member(bd.ns, r.Name, 0); rest == "" && f != "" {
				resolved(f, "import")
				return
			}
		case bd.fqn != "":
			t := bd.fqn
			if rest != "" {
				t += "." + rest
			}
			if t += "." + r.Name; b.exists(t) {
				resolved(t, "import")
				return
			}
		}
		if callLike {
			unresolved("missing", nil)
		} else {
			external()
		}
		return
	}
	if head != "~" {
		for _, scope := range b.scopes(owner, m.Namespace) {
			if t := scope + "." + r.Qualifier + "." + r.Name; b.exists(t) {
				resolved(t, "scope")
				return
			}
		}
	}
	if callLike {
		b.byMember(m.Language, owner, r.Name, resolved, unresolved, external)
		return
	}
	external()
}

// resolveType returns the FQN of a type named in owner's code (T, or
// pkg.T through an import binding), or "" when it is not a symbol of the
// workspace.
func (b *builder) resolveType(mc *modCtx, owner *node, t string) string {
	if head, rest, dotted := strings.Cut(t, "."); dotted {
		if bd, ok := mc.binds[head]; ok && bd.ns != "" && b.exists(bd.ns+"."+rest) {
			return bd.ns + "." + rest
		}
		return ""
	}
	for _, scope := range b.scopes(owner, mc.mod.Namespace) {
		if b.exists(scope + "." + t) {
			return scope + "." + t
		}
	}
	for _, ns := range mc.dots {
		if b.exists(ns + "." + t) {
			return ns + "." + t
		}
	}
	return ""
}

// byMember resolves a member called on a value of unknown type: the one
// member of that name in the language, or, with several, an ambiguous
// reference. A production caller never resolves to a test's member.
func (b *builder) byMember(lang string, owner *node, name string, resolved func(string, string), unresolved func(string, []string), external func()) {
	var cands []string
	for _, f := range b.members[lang][name] {
		if n := b.g.nodes[f]; n != nil && (!n.isTest || owner.isTest) {
			cands = append(cands, f)
		}
	}
	switch len(cands) {
	case 0:
		external()
	case 1:
		resolved(cands[0], "unique")
	default:
		unresolved("ambiguous", cands)
	}
}

// scopes lists where a bare name of owner's code is looked up, innermost
// first: the owner itself (a nested function), each enclosing function,
// then the module's namespace. A class is not a scope for bare names.
func (b *builder) scopes(owner *node, ns string) []string {
	var out []string
	for n := owner; n != nil && n.symbol; {
		if n.kind != string(ir.KindClass) && n.kind != string(ir.KindInterface) {
			out = append(out, n.fqn)
		}
		if n.enclosing == "" {
			break
		}
		n = b.g.nodes[n.enclosing]
	}
	if ns != "" {
		out = append(out, ns)
	}
	return out
}

// enclosingType returns the nearest enclosing class (or object const) of
// owner, the type "this" names.
func (b *builder) enclosingType(owner *node) string {
	for n := owner; n != nil && n.symbol; n = b.g.nodes[n.enclosing] {
		switch n.kind {
		case string(ir.KindClass), string(ir.KindConst), string(ir.KindTrait):
			return n.fqn
		}
		if n.enclosing == "" {
			return ""
		}
	}
	return ""
}

// basesOf returns a class's base classes that are in the graph, recorded
// as FQNs or resolved through the module's imports by simple name.
func (b *builder) basesOf(cls string, binds map[string]binding) []string {
	n := b.g.nodes[cls]
	if n == nil {
		return nil
	}
	var out []string
	for _, base := range n.bases {
		if b.exists(base) {
			out = append(out, base)
			continue
		}
		if bd, ok := binds[lastSegment(base)]; ok && bd.fqn != "" {
			out = append(out, bd.fqn)
		}
	}
	return out
}

// scipEdges adds the edges of a SCIP index for one language: each
// reference from the definition that encloses it to the referenced
// symbol's definition, both located on the adapter's symbols by file and
// line. Top-level references come from the module's top node.
func (b *builder) scipEdges(lang string, c *scip.Corpus) {
	g := b.g
	type key struct {
		file string
		line int
	}
	at := map[key]string{}
	var files = map[string]int{}
	for mi, m := range g.modules {
		if m.Language != lang {
			continue
		}
		files[m.File] = mi
		for _, s := range m.Symbols {
			k := key{s.File, s.Line}
			if _, ok := at[k]; !ok {
				at[k] = s.FQN
			}
		}
	}
	locate := func(file string, line int) string {
		if f, ok := at[key{file, line}]; ok {
			return f
		}
		// The definition's name may sit below the symbol's first line
		// (a doc comment, a decorator, an export keyword): the innermost
		// symbol of the file that spans the line.
		mi, ok := files[file]
		if !ok {
			return ""
		}
		best, span := "", 1<<30
		for _, s := range g.modules[mi].Symbols {
			if s.Line <= line && line <= s.EndLine && s.EndLine-s.Line < span {
				best, span = s.FQN, s.EndLine-s.Line
			}
		}
		return best
	}
	for _, r := range c.References() {
		to := locate(r.Callee.File, r.Callee.Line)
		if to == "" {
			continue
		}
		from := ""
		if r.Caller.Symbol != "" {
			from = locate(r.File, r.Caller.Line)
		}
		if from == "" {
			if mi, ok := files[r.File]; ok {
				from = g.tops[mi]
			}
		}
		if from == "" || from == to {
			continue
		}
		before := len(g.Edges)
		b.addEdge(Edge{From: from, To: to, Kind: "scip", Via: "scip", Line: r.Line})
		if len(g.Edges) > before {
			g.Stats.SCIPEdges++
		}
	}
}
