package reach

// @module-feature reachability

import (
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// runtimeKinds are the symbol kinds that hold runtime code, the ones that
// get a reach class.
var runtimeKinds = map[string]bool{"function": true, "method": true, "class": true, "const": true, "trait": true}

// Result is a classified call graph: the graph, its roots and each runtime
// symbol's static reach class.
type Result struct {
	Graph *Graph
	Roots []Root
	// Class maps each runtime (non-test) symbol to its reach class.
	Class map[string]string
	// Classes counts symbols per class.
	Classes map[string]int
	// Handler maps each runtime (non-test) symbol and interface to its
	// handler reach: does the handler of a declared entry point reach it
	// (see ClassifyHandlers)? Nil until ClassifyHandlers runs.
	Handler map[string]string
}

// HasRoots reports whether any root exists.
func (r *Result) HasRoots() bool { return r != nil && len(r.Roots) > 0 }

// Classify computes every runtime symbol's static reach class:
//
//   - entry: a root reaches it through resolved edges, or it is a method a
//     reached ambiguous call names and its type is reached too (see walk);
//   - test_only: no root reaches it, a test does;
//   - unreached: nothing reaches it, not even through a reference left
//     ambiguous (one that names it among other candidates);
//   - unknown: there are no roots; or its language has no reference data;
//     or it is reached only through an ambiguous reference whose type
//     nothing reached, which the graph does not resolve.
//
// @enforces reachability:INV-4
func Classify(g *Graph, roots []Root) *Result {
	r := &Result{Graph: g, Roots: roots, Class: map[string]string{}, Classes: map[string]int{}}
	var rootFQNs, testFQNs []string
	for _, rt := range roots {
		rootFQNs = append(rootFQNs, rt.FQN)
	}
	for f, n := range g.nodes {
		if n.isTest {
			testFQNs = append(testFQNs, f)
		}
	}
	sort.Strings(testFQNs)
	fromRoots, mayRoots := g.walk(rootFQNs)
	fromTests, mayTests := g.walk(testFQNs)
	for f, n := range g.nodes {
		if !n.symbol || n.isTest || !runtimeKinds[n.kind] {
			continue
		}
		c := schema.ReachUnknown
		switch {
		case len(roots) == 0:
		case fromRoots[f]:
			c = schema.ReachEntry
		case !n.refsKnown, mayRoots[f]:
		case fromTests[f]:
			c = schema.ReachTestOnly
		case mayTests[f]:
		default:
			c = schema.ReachUnreached
		}
		r.Class[f] = c
		r.Classes[c]++
	}
	return r
}

// Handlers lists the handler symbols of the entry points that resolve to a
// runtime symbol of the graph, sorted. A handler that names no symbol of the
// graph (a typo, a renamed function, a language with no symbol data) is left
// out: it reaches nothing.
func (g *Graph) Handlers(eps []schema.EntryPoint) []string {
	seen := map[string]bool{}
	var out []string
	for _, ep := range eps {
		f := ep.Handler.Symbol
		n := g.nodes[f]
		if f == "" || seen[f] || n == nil || !n.symbol || n.isTest {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// ClassifyHandlers records, for every runtime symbol and interface,
// whether the call graph reaches it from one of handlers (see Handlers), by
// the walk Classify uses:
//
//   - reached: a handler reaches it through resolved edges, or through an
//     ambiguous method call whose type reached code uses;
//   - unknown: its language has no reference data, or a handler reaches it
//     only through an ambiguous reference the graph does not resolve;
//   - unreached: no handler reaches it, not even through an ambiguous
//     reference; with no handler at all, nothing is reached.
//
// Other roots (func main, entry files, coverage.roots) do not count: this
// is the reach of the declared entry points alone. Interfaces get a handler
// reach too, by the rule types already follow in the walk: a type is reached
// when reached code names or builds it.
// @enforces check:INV-12
func (r *Result) ClassifyHandlers(handlers []string) {
	r.Handler = map[string]string{}
	if r.Graph == nil {
		return
	}
	g := r.Graph
	must, may := g.walk(handlers)
	for f, n := range g.nodes {
		if !n.symbol || n.isTest || !(runtimeKinds[n.kind] || n.kind == "interface") {
			continue
		}
		c := schema.HandlerUnreached
		switch {
		case must[f]:
			c = schema.HandlerReached
		case len(handlers) == 0:
		case !n.refsKnown, may[f]:
			c = schema.HandlerUnknown
		}
		r.Handler[f] = c
	}
}

// walk returns the nodes reachable from start, and those reachable once
// ambiguous references are followed to every candidate too.
//
// The first walk follows resolved edges and, for an ambiguous call (a
// method called on a value of unknown type: an interface call, x.Parse()
// with five types that have Parse), takes a candidate only once its type
// is reached as well: Rapid Type Analysis, the rule of Go's own deadcode
// tool. A type is reached when reached code names or builds it. The call
// stays unresolved in the graph, and a candidate whose type nothing
// reached is left to the second walk, so it reads unknown, never entry.
func (g *Graph) walk(start []string) (must, may map[string]bool) {
	bfs := func(withMay bool) map[string]bool {
		seen := map[string]bool{}
		pending := map[string][]string{} // type -> its methods an ambiguous call named
		queue := append([]string(nil), start...)
		for _, s := range start {
			seen[s] = true
		}
		visit := func(n string) {
			if seen[n] {
				return
			}
			seen[n] = true
			queue = append(queue, n)
			for _, m := range pending[n] {
				if !seen[m] {
					seen[m] = true
					queue = append(queue, m)
				}
			}
			delete(pending, n)
		}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, n := range g.out[cur] {
				visit(n)
			}
			for _, c := range g.may[cur] {
				if withMay {
					visit(c)
					continue
				}
				typ := ""
				if n := g.nodes[c]; n != nil {
					typ = n.enclosing
				}
				switch {
				case typ == "":
				case seen[typ]:
					visit(c)
				default:
					pending[typ] = append(pending[typ], c)
				}
			}
		}
		return seen
	}
	return bfs(false), bfs(true)
}

// Annotate writes each runtime symbol's class onto the graph
// (GraphSymbol.Reach), and its handler reach (GraphSymbol.HandlerReach)
// when ClassifyHandlers ran. With no result it writes nothing.
func Annotate(kg *schema.KnowledgeGraph, r *Result) {
	if r == nil {
		return
	}
	for i := range kg.Symbols {
		kg.Symbols[i].Reach = r.Class[kg.Symbols[i].FQN]
		if r.Handler != nil {
			kg.Symbols[i].HandlerReach = r.Handler[kg.Symbols[i].FQN]
		}
	}
}

// Callers returns the nodes with a resolved edge to fqn, sorted.
func (g *Graph) Callers(fqn string) []string {
	if g.rev == nil {
		g.rev = g.reverse()
	}
	return g.rev[fqn]
}

// Imported reports whether another namespace imports or references fqn.
func (g *Graph) Imported(fqn string) bool { return g.imported[fqn] }

// reverse is the resolved edges reversed, for Callers.
func (g *Graph) reverse() map[string][]string {
	rev := map[string][]string{}
	for from, tos := range g.out {
		for _, t := range tos {
			rev[t] = append(rev[t], from)
		}
	}
	for k := range rev {
		sort.Strings(rev[k])
	}
	return rev
}
