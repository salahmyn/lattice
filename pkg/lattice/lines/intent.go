package lines

// @module-feature audit

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Intent kinds of a block.
const (
	IntentEnforces = "enforces" // the block carries @enforces
	IntentServes   = "serves"   // every caller is an enforcer, or serves
	IntentShared   = "shared"   // reached from enforcers and from blocks with no intent
	IntentNone     = "none"
)

// Edge is one resolved reference of the call graph: From's code names To.
// Kind is the reference kind (call, new, rpc, ref, import, member, ...).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// callKinds are the edge kinds that mean the code of From runs the code of
// To. An import or a member edge only says a module loads another or a type
// holds a member, and a publish or consume edge crosses a channel, which the
// synchronous call graph does not follow.
func callKinds(kind string) bool {
	switch kind {
	case "import", "member", "use", "publish", "consume":
		return false
	}
	return true
}

// blockKinds are the symbol kinds that are a block: code with its own lines
// that something runs. A symbol of another kind that carries @enforces is a
// block too (a const that holds a function).
var blockKinds = map[string]bool{"function": true, "method": true, "policy": true, "trigger": true}

// isSQL reports whether a symbol is SQL: its lines have no line data.
func isSQL(s schema.GraphSymbol) bool { return s.Language == "postgres" || s.Language == "sql" }

// SourceFiles are the workspace files a coverage report is kept for: the
// graph's files that hold a production symbol.
func SourceFiles(kg schema.KnowledgeGraph) map[string]bool {
	test := map[string]bool{}
	for _, t := range kg.Tests {
		test[t.File] = true
	}
	out := map[string]bool{}
	for _, s := range kg.Symbols {
		if !s.IsTest && !test[s.File] {
			out[s.File] = true
		}
	}
	return out
}

// ref qualifies an invariant reference with the feature that wrote it.
func ref(feature, r string) string {
	if strings.Contains(r, ":") {
		return r
	}
	return feature + ":" + r
}

// node is a block's place in the intent computation. Several symbols can
// share an FQN (a SQL object defined again in a later migration), and the
// intent is the FQN's.
type node struct {
	fqn      string
	enforces []string
	inv      map[string]bool // the intent, once propagated
	cont     bool            // reached from code with no intent
	callers  []string
	isEnf    bool
}

// intents computes the intent of every block FQN by two least fixed points,
// so a cycle of helpers is decided the same way every run:
//
//  1. an enforcer's intent is its @enforces; any other block takes the union
//     of the intents of the blocks that call it, until nothing changes;
//  2. a block that is not an enforcer is shared when some caller has no
//     intent (a caller that is not a block, or one with an empty intent) or
//     is itself shared; it serves otherwise. An enforcer's own callers never
//     make what it calls shared.
//
// A test never counts as a caller: it verifies, it does not use. A caller
// that is not a block (module-level code, a channel) has no intent.
// @enforces audit:INV-10
func intents(kg schema.KnowledgeGraph, blocks map[string]bool, edges []Edge) map[string]*node {
	testFile := map[string]bool{}
	test := map[string]bool{}
	for _, t := range kg.Tests {
		test[t.FQN] = true
		testFile[t.File] = true
	}
	for _, s := range kg.Symbols {
		if s.IsTest || testFile[s.File] {
			test[s.FQN] = true
		}
	}
	nodes := map[string]*node{}
	for _, s := range kg.Symbols {
		if !blocks[s.FQN] {
			continue
		}
		n := nodes[s.FQN]
		if n == nil {
			n = &node{fqn: s.FQN, inv: map[string]bool{}}
			nodes[s.FQN] = n
		}
		for _, e := range s.EnforcesInvariants {
			n.enforces = append(n.enforces, ref(s.Feature, e))
		}
	}
	// A block a BRD criterion names in enforced_by (a direct-wired criterion)
	// is guarded the way lattice check counts it: it enforces that criterion,
	// as <brd>:<criterion>.
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			for _, f := range sc.EnforcedBy {
				if n := nodes[f]; n != nil {
					n.enforces = append(n.enforces, b.ID+":"+sc.ID)
				}
			}
		}
	}
	for _, n := range nodes {
		sort.Strings(n.enforces)
		n.isEnf = len(n.enforces) > 0
		for _, e := range n.enforces {
			n.inv[e] = true
		}
	}
	// The callers of each block, once each, and the callers that are not
	// blocks, which contaminate.
	seen := map[[2]string]bool{}
	for _, e := range edges {
		if !callKinds(e.Kind) || e.From == e.To || nodes[e.To] == nil || test[e.From] {
			continue
		}
		if k := [2]string{e.From, e.To}; !seen[k] {
			seen[k] = true
			nodes[e.To].callers = append(nodes[e.To].callers, e.From)
		}
	}
	for _, n := range nodes {
		sort.Strings(n.callers)
	}
	order := make([]string, 0, len(nodes))
	for f := range nodes {
		order = append(order, f)
	}
	sort.Strings(order)

	for changed := true; changed; {
		changed = false
		for _, f := range order {
			n := nodes[f]
			if n.isEnf {
				continue
			}
			for _, c := range n.callers {
				if cn := nodes[c]; cn != nil {
					for inv := range cn.inv {
						if !n.inv[inv] {
							n.inv[inv] = true
							changed = true
						}
					}
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, f := range order {
			n := nodes[f]
			if n.isEnf || n.cont || len(n.inv) == 0 {
				continue
			}
			for _, c := range n.callers {
				cn := nodes[c]
				if cn == nil || len(cn.inv) == 0 || (!cn.isEnf && cn.cont) {
					n.cont, changed = true, true
					break
				}
			}
		}
	}
	return nodes
}

// kind is the node's intent kind.
func (n *node) kind() string {
	switch {
	case n == nil || len(n.inv) == 0:
		return IntentNone
	case n.isEnf:
		return IntentEnforces
	case n.cont:
		return IntentShared
	}
	return IntentServes
}

// refs are the invariants of the node's intent, sorted.
func (n *node) refs() []string {
	if n == nil {
		return nil
	}
	out := make([]string, 0, len(n.inv))
	for r := range n.inv {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
