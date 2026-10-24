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
// (GraphSymbol.Reach). With no result it writes nothing.
func Annotate(kg *schema.KnowledgeGraph, r *Result) {
	if r == nil {
		return
	}
	for i := range kg.Symbols {
		kg.Symbols[i].Reach = r.Class[kg.Symbols[i].FQN]
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
