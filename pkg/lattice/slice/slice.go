// Package slice answers, for one invariant (a promise): which ways into the
// system end up running the code that keeps it, and through which layers?
//
// The audit says whether an enforcer is reached (reach class entry, test_only,
// unreached). A slice adds the paths: for every root of the call graph that
// reaches an enforcer, one shortest path of hops, each hop an edge of the
// reach graph with the runtime layer on both ends. Nothing is guessed. A hop
// is resolved when the graph resolved the reference, and unknown when the
// only edge the graph has is an ambiguous reference followed to one of its
// recorded candidates; a reference with no recorded candidate is not followed.
// A slice with an unknown hop is never complete.
package slice

// @module-feature reachability

import (
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Contract is the version tag of the JSON record.
const Contract = "lattice.slice/1"

// MaxPaths caps the paths one record lists; Truncated counts the rest.
const MaxPaths = 50

// The readings of a slice, in the order Compute decides them:
//
//	no_enforcer       the promise has no enforcer to slice;
//	unknown           there is no reach graph (review mode) or it has no roots;
//	unreached         some enforcer has no path from any root;
//	unknown_from_hop  every enforcer has a path, and a path has an unknown hop;
//	complete          every enforcer has a path and every hop on every path is resolved.
const (
	ReadingComplete       = "complete"
	ReadingUnknownFromHop = "unknown_from_hop"
	ReadingUnreached      = "unreached"
	ReadingNoEnforcer     = "no_enforcer"
	ReadingUnknown        = "unknown"
)

// Truth of a hop. Exercised and break_tested are separate fields: an absent
// field is not a false one.
const (
	TruthResolved = "resolved" // the graph resolved the reference to this target
	TruthUnknown  = "unknown"  // an ambiguous reference that lists this target among its candidates
)

// channelKind is the node kind of a channel: the node a publish hop ends at
// and a consume hop starts from. Its layer is async, the layer of queue
// workers and event consumers, since it is where the path leaves the request.
const channelKind = "channel"

// postgresLanguage is the language the Postgres adapter reports; its nodes
// are data, the same rule graph.AssignLayers applies to its symbols.
const postgresLanguage = "postgres"

// Enforcer is one enforcing symbol of the promise, as the audit lists it.
type Enforcer struct {
	FQN   string `json:"fqn"`
	Layer string `json:"layer,omitempty"`
	Reach string `json:"reach,omitempty"`
	// AsyncHops is the audit's count: the channels the shortest path from a
	// root crosses, set only when no root reaches the enforcer synchronously.
	AsyncHops int `json:"async_hops,omitempty"`
}

// Root is where a path starts, and why the runtime starts there.
type Root struct {
	FQN    string `json:"fqn"`
	Reason string `json:"reason"`
	// EntryPoint is the id of the entry point whose handler the root is.
	EntryPoint string `json:"entry_point,omitempty"`
}

// Hop is one edge of a path.
type Hop struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is the edge kind of the reach graph: call, new, rpc, table, http,
	// jsx, ref, publish and consume (the two hops through a channel), and the
	// structural ones (import, member, use, scip).
	Kind      string `json:"kind"`
	FromLayer string `json:"from_layer"`
	ToLayer   string `json:"to_layer"`
	Truth     string `json:"truth"`
	// Exercised is set only when one verifier's own run executed symbols on
	// both sides of the hop (see Exercise); absent means not shown, never
	// that it did not happen. ExercisedBy lists those verifiers, sorted and
	// capped at MaxExercisedBy.
	Exercised   bool     `json:"exercised,omitempty"`
	ExercisedBy []string `json:"exercised_by,omitempty"`
	// BreakTested is set only when an edge break of this hop was killed: the
	// literal that names the hop's target, at its out end, was altered and a
	// verifier failed (see MarkBreakTested). BreakTestedBy lists the failing
	// verifiers, sorted and capped at MaxExercisedBy. Absent means no edge
	// break showed it, never that none would.
	BreakTested   bool     `json:"break_tested,omitempty"`
	BreakTestedBy []string `json:"break_tested_by,omitempty"`
}

// Path is one shortest path from a root to an enforcer.
type Path struct {
	Root     Root   `json:"root"`
	Enforcer string `json:"enforcer"`
	Hops     []Hop  `json:"hops"`
	// CarriedBy is the layers along the path in order, a run of the same
	// layer counted once.
	CarriedBy []string `json:"carried_by"`
	// AsyncHops counts the channels the path crosses; absent means none. A
	// path through a channel is listed only when no path without one reaches
	// the enforcer from the same root.
	AsyncHops int `json:"async_hops,omitempty"`
	// ExercisedHops counts the hops marked exercised, out of len(Hops). It is
	// absent when the record has no per-test coverage to count from.
	ExercisedHops *int `json:"exercised_hops,omitempty"`
	// BreakTestedHops counts the hops marked break_tested, out of len(Hops).
	// It is absent when the record has no edge-break runs to count from.
	BreakTestedHops *int `json:"break_tested_hops,omitempty"`
}

// Record is the slice of one invariant: lattice.slice/1.
type Record struct {
	Contract  string `json:"contract"`
	Invariant string `json:"invariant"`
	Commit    string `json:"commit,omitempty"`
	Reading   string `json:"reading"`
	// Hop is, for unknown_from_hop, the 1-based index of the first unknown
	// hop on the first path that has one (the first path listed).
	Hop int `json:"hop,omitempty"`
	// Why says the reading in a sentence.
	Why       string     `json:"why"`
	Enforcers []Enforcer `json:"enforcers"`
	// Unreached lists the enforcers no root reaches.
	Unreached []string `json:"unreached,omitempty"`
	// Paths lists at most MaxPaths paths: those with an unknown hop first,
	// then the rest, each group by root and then enforcer.
	Paths []Path `json:"paths"`
	// Truncated counts the paths left out of Paths.
	Truncated int `json:"truncated,omitempty"`
	// Exercised is "measured" when per-test coverage was read for the hops
	// and "unknown" when there is none; ExercisedWhy says so. Both are absent
	// until Exercise runs. This never changes Reading, which is structure.
	Exercised    string `json:"exercised,omitempty"`
	ExercisedWhy string `json:"exercised_why,omitempty"`
	// Verifiers are the promise's verifiers with the layers their own runs
	// executed, or ["unknown"] when a verifier has no per-test coverage.
	Verifiers []Verifier `json:"verifiers,omitempty"`
}

// step is one outgoing edge of a node.
type step struct {
	to, kind string
	unknown  bool
	async    bool // a publish or consume edge: it crosses a channel
}

// The search modes of paths, as bits: a mode allows unknown hops (modeUnknown)
// and hops through channels (modeAsync). Modes are tried in the order
// searchOrder, so a path of resolved synchronous hops beats one through a
// channel, which beats one with an unknown hop.
const (
	modeUnknown = 1
	modeAsync   = 2
)

var searchOrder = [...]int{0, modeAsync, modeUnknown, modeUnknown | modeAsync}

// Graph is the part of the reach result a slice reads: the edges, the roots
// and the layer of every node. It is built once and never written to, so
// requests may share it.
type Graph struct {
	succ  map[string][]step
	rev   [4]map[string][]string // per search mode: to -> from, over the steps the mode allows
	roots []reach.Root
	layer map[string]string // symbol FQN -> layer, as the graph assigned it
	isSym map[string]bool   // every symbol of the knowledge graph: a node coverage can speak of
	cov   Coverage          // ingested per-test coverage, nil when none
	rules []schema.LayerRule
	info  func(string) (reach.NodeInfo, bool)
}

// NewGraph reads a classified call graph and the knowledge graph whose
// symbols carry the layers. It returns nil when there is no reach graph
// (review mode has no source), which Compute reads as unknown.
func NewGraph(rr *reach.Result, kg schema.KnowledgeGraph, rules []schema.LayerRule) *Graph {
	if rr == nil || rr.Graph == nil {
		return nil
	}
	layers := map[string]string{}
	syms := map[string]bool{}
	for _, s := range kg.Symbols {
		syms[s.FQN] = true
		if s.Layer != "" {
			layers[s.FQN] = s.Layer
		}
	}
	// A direct call, not the method value: the call graph follows calls, and
	// a method passed as a value reaches no entry point.
	g := rr.Graph
	info := func(fqn string) (reach.NodeInfo, bool) { return g.NodeInfo(fqn) }
	sg := newGraph(rr.Roots, g.Edges, g.Unresolved, layers, rules, info)
	sg.isSym = syms
	return sg
}

func newGraph(roots []reach.Root, edges []reach.Edge, unresolved []reach.Unresolved, layers map[string]string,
	rules []schema.LayerRule, info func(string) (reach.NodeInfo, bool)) *Graph {
	g := &Graph{succ: map[string][]step{},
		roots: append([]reach.Root(nil), roots...), layer: layers, rules: rules, info: info, isSym: map[string]bool{}}
	for fqn := range layers {
		g.isSym[fqn] = true
	}
	for m := range g.rev {
		g.rev[m] = map[string][]string{}
	}
	for _, e := range edges {
		g.succ[e.From] = append(g.succ[e.From], step{to: e.To, kind: e.Kind, async: e.Kind == ir.RefPublish || e.Kind == ir.RefConsume})
	}
	// An ambiguous reference is followed to every candidate it recorded, as
	// unknown. A missing or dynamic one records none and is not followed.
	for _, u := range unresolved {
		if u.Why != "ambiguous" {
			continue
		}
		for _, c := range u.Candidates {
			g.succ[u.From] = append(g.succ[u.From], step{to: c, kind: u.Kind, unknown: true})
		}
	}
	for from, steps := range g.succ {
		sort.SliceStable(steps, func(i, j int) bool {
			a, b := steps[i], steps[j]
			if a.to != b.to {
				return a.to < b.to
			}
			if a.unknown != b.unknown {
				return !a.unknown // a resolved edge to the same node comes first
			}
			return a.kind < b.kind
		})
		g.succ[from] = steps
		for _, st := range steps {
			for m := range g.rev {
				if (!st.unknown || m&modeUnknown != 0) && (!st.async || m&modeAsync != 0) {
					g.rev[m][st.to] = append(g.rev[m][st.to], from)
				}
			}
		}
	}
	return g
}

// layerOf is the layer of a node: the one the graph assigned to a symbol; for
// a node that is no symbol (a module's top-level code, a table) the adapter
// fact of a Postgres node, else the declared rule for its file; else unknown.
func (g *Graph) layerOf(fqn string) string {
	if l, ok := g.layer[fqn]; ok {
		return l
	}
	var n reach.NodeInfo
	ok := false
	if g.info != nil {
		n, ok = g.info(fqn)
	}
	switch {
	case !ok:
		return schema.LayerUnknown
	case n.Kind == channelKind:
		return schema.LayerAsync
	case n.Language == postgresLanguage:
		return schema.LayerData
	}
	return schema.LayerOfFile(g.rules, n.File)
}

// EnforcersOf lists the enforcers of an audit row, sorted by FQN.
func EnforcersOf(row audit.Row) []Enforcer {
	out := make([]Enforcer, 0, len(row.Evidence.Enforcers))
	for _, e := range row.Evidence.Enforcers {
		out = append(out, Enforcer{FQN: e.FQN, Layer: e.Layer, Reach: e.Reach, AsyncHops: e.AsyncHops})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].FQN < out[j].FQN })
	return out
}

// Compute slices one promise: the paths from the graph's roots to its
// enforcers. A nil graph reads unknown.
func Compute(g *Graph, invariant string, enforcers []Enforcer) Record {
	rec := Record{Contract: Contract, Invariant: invariant, Enforcers: append([]Enforcer{}, enforcers...), Paths: []Path{}}
	sort.SliceStable(rec.Enforcers, func(i, j int) bool { return rec.Enforcers[i].FQN < rec.Enforcers[j].FQN })
	if len(rec.Enforcers) > 0 && g != nil && len(g.roots) > 0 {
		rec.Paths, rec.Unreached = g.paths(rec.Enforcers)
	}
	reading(&rec, g)
	if len(rec.Paths) > MaxPaths {
		rec.Truncated = len(rec.Paths) - MaxPaths
		rec.Paths = rec.Paths[:MaxPaths]
	}
	return rec
}

// reading decides the reading from every path, before the list is cut, and
// says why. A path with an unknown hop is never read as complete.
// @enforces reachability:INV-10
func reading(rec *Record, g *Graph) {
	switch {
	case len(rec.Enforcers) == 0:
		rec.Reading, rec.Why = ReadingNoEnforcer, "the promise has no enforcer to slice"
	case g == nil:
		rec.Reading, rec.Why = ReadingUnknown, "there is no reach graph (review mode has no source)"
	case len(g.roots) == 0:
		rec.Reading, rec.Why = ReadingUnknown, "the reach graph has no roots, so nothing is known to start a path"
	case len(rec.Unreached) > 0:
		rec.Reading = ReadingUnreached
		rec.Why = "no root reaches " + strings.Join(rec.Unreached, ", ") + ", through resolved edges or recorded candidates"
	default:
		for _, p := range rec.Paths {
			for i, h := range p.Hops {
				if h.Truth != TruthResolved {
					rec.Reading, rec.Hop = ReadingUnknownFromHop, i+1
					rec.Why = "hop " + strconv.Itoa(i+1) + " of the path from " + p.Root.FQN + " to " + p.Enforcer +
						" is an ambiguous reference followed to one of its candidates, so it is not known to run"
					return
				}
			}
		}
		rec.Reading, rec.Why = ReadingComplete, "every enforcer is reached, and every hop on every path is resolved"
	}
}

// paths finds, for each root and each enforcer it reaches, one shortest path,
// and lists the enforcers no root reaches. A path over resolved edges only
// wins over a shorter one that crosses an unknown hop, and one that crosses no
// channel wins over a shorter one that does; the longer kind is used only when
// the pair has no better path. Neighbours are visited in
// sorted order, so the path is the same on every run.
func (g *Graph) paths(enforcers []Enforcer) (paths []Path, unreached []string) {
	targets := map[string]bool{}
	for _, e := range enforcers {
		targets[e.FQN] = true
	}
	var can [4]map[string]bool
	for m := range can {
		can[m] = g.upstream(targets, g.rev[m])
	}

	reached := map[string]bool{}
	for _, root := range g.roots {
		if !can[modeUnknown|modeAsync][root.FQN] {
			continue
		}
		found := map[string]bool{}
		for _, mode := range searchOrder {
			if len(found) == len(targets) {
				break
			}
			if !can[mode][root.FQN] {
				continue
			}
			for t, hops := range g.bfs(root.FQN, targets, mode, can[mode]) {
				if !found[t] {
					found[t] = true
					paths = append(paths, g.pathOf(root, t, hops))
				}
			}
		}
	}
	for _, p := range paths {
		reached[p.Enforcer] = true
	}
	for _, e := range enforcers {
		if !reached[e.FQN] {
			unreached = append(unreached, e.FQN)
		}
	}
	sort.SliceStable(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if ua, ub := hasUnknown(a), hasUnknown(b); ua != ub {
			return ua
		}
		if a.Root.FQN != b.Root.FQN {
			return a.Root.FQN < b.Root.FQN
		}
		return a.Enforcer < b.Enforcer
	})
	return paths, unreached
}

func hasUnknown(p Path) bool {
	for _, h := range p.Hops {
		if h.Truth != TruthResolved {
			return true
		}
	}
	return false
}

// upstream is every node with a path to a target over the given reversed
// edges, the targets included. A search from a root stays inside it.
func (g *Graph) upstream(targets map[string]bool, rev map[string][]string) map[string]bool {
	seen := map[string]bool{}
	var queue []string
	for t := range targets {
		seen[t] = true
		queue = append(queue, t)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, p := range rev[cur] {
			if !seen[p] {
				seen[p] = true
				queue = append(queue, p)
			}
		}
	}
	return seen
}

type parent struct {
	from string
	st   step
}

// bfs walks from root in breadth-first order, neighbours sorted, inside
// allowed, over the steps the mode allows (resolved ones, synchronous ones,
// unless the mode says otherwise), and returns the shortest path to each
// target it reaches (the root itself is at distance 0).
func (g *Graph) bfs(root string, targets map[string]bool, mode int, allowed map[string]bool) map[string][]Hop {
	par := map[string]parent{}
	seen := map[string]bool{root: true}
	queue := []string{root}
	out := map[string][]Hop{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if targets[cur] {
			out[cur] = g.hopsTo(root, cur, par)
		}
		for _, st := range g.succ[cur] {
			if st.unknown && mode&modeUnknown == 0 || st.async && mode&modeAsync == 0 || seen[st.to] || !allowed[st.to] {
				continue
			}
			seen[st.to] = true
			par[st.to] = parent{from: cur, st: st}
			queue = append(queue, st.to)
		}
	}
	return out
}

// hopsTo rebuilds the path root -> to from the parent links.
func (g *Graph) hopsTo(root, to string, par map[string]parent) []Hop {
	var rev []Hop
	for cur := to; cur != root; {
		p := par[cur]
		truth := TruthResolved
		if p.st.unknown {
			truth = TruthUnknown
		}
		rev = append(rev, Hop{From: p.from, To: cur, Kind: p.st.kind,
			FromLayer: g.layerOf(p.from), ToLayer: g.layerOf(cur), Truth: truth})
		cur = p.from
	}
	hops := make([]Hop, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		hops = append(hops, rev[i])
	}
	return hops
}

func (g *Graph) pathOf(root reach.Root, enforcer string, hops []Hop) Path {
	r := Root{FQN: root.FQN, Reason: root.Reason}
	if id, ok := strings.CutPrefix(root.Reason, "entry point "); ok {
		r.EntryPoint = id
	}
	p := Path{Root: r, Enforcer: enforcer, Hops: hops}
	if p.Hops == nil {
		p.Hops = []Hop{}
	}
	p.CarriedBy = []string{g.layerOf(root.FQN)}
	for _, h := range hops {
		if h.Kind == ir.RefConsume {
			p.AsyncHops++ // each channel is crossed by one publish hop and one consume hop
		}
		if p.CarriedBy[len(p.CarriedBy)-1] != h.ToLayer {
			p.CarriedBy = append(p.CarriedBy, h.ToLayer)
		}
	}
	return p
}
