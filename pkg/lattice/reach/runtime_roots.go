package reach

// @module-feature reachability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Sources of a RuntimeRoot (RuntimeRoot.Source).
const (
	RootServe   = "serve"   // a request handler passed to serve / Deno.serve
	RootCron    = "cron"    // a cron.schedule job
	RootSurface = "surface" // a route (or other trigger) an adapter detected in source
)

// Why a RuntimeRoot cannot become an entry point (RuntimeRoot.Blocked).
const (
	BlockedHandlerMissing = "handler_missing" // no symbol of the graph is the handler
	BlockedKeyComputed    = "key_computed"    // the job's name or schedule is an expression
)

// RuntimeRoot is code the runtime invokes by itself, as an adapter read it
// from source: a serve handler, a scheduled job, a detected route. Each is a
// candidate entry point.
type RuntimeRoot struct {
	// FQN is the handler: the root node of a serve handler or cron job, or
	// the symbol a route names. Empty when a route names none.
	FQN    string
	Source string // RootServe, RootCron or RootSurface
	// Kind is the entry-point kind the root takes (schema.EntryPointKind*).
	Kind string
	File string
	Line int
	// Job and Schedule are a cron job's literals; Method and Path a route's.
	Job, Schedule string
	Method, Path  string
	// Calls are the nodes a cron job's body or a serve root calls directly
	// (resolved edges): the SQL functions a job runs, the handler a serve
	// call builds.
	Calls []string
	// Reaches are the nodes a cron job's body reaches by a direct call, or
	// by one http hop to an edge function (the job's own, or that of a
	// function it calls) and then direct calls, to reachDepth edges in all.
	// Calls are among them.
	Reaches []string
	// Blocked is empty for a root that can become an entry point; otherwise
	// the reason it cannot (Blocked*).
	Blocked string
}

// surfaceKinds maps a detected surface type to its entry-point kind.
var surfaceKinds = map[string]string{
	"http":            schema.EntryPointKindHTTP,
	"webhook_receive": schema.EntryPointKindWebhook,
	"event_consume":   schema.EntryPointKindEventConsumer,
	"scheduled":       schema.EntryPointKindCron,
}

// RuntimeRoots lists the roots the runtime invokes by itself, in file and
// line order: serve handlers and cron.schedule bodies from the adapters'
// nodes, and the routes they detected in source. Test files hold none.
// Roots a person declares (entry points, coverage.roots) and roots that
// are not triggers (func main, entry files, SQL policies) are not listed.
// @enforces entrypoints:INV-5
func (g *Graph) RuntimeRoots() []RuntimeRoot {
	var out []RuntimeRoot
	edgesFrom := map[string][]Edge{}
	for _, e := range g.Edges {
		edgesFrom[e.From] = append(edgesFrom[e.From], e)
	}
	for mi := range g.modules {
		m := &g.modules[mi]
		for _, n := range m.Nodes {
			if n.IsTest || g.nodes[n.FQN] == nil {
				continue
			}
			switch {
			case n.Kind == ir.NodeServe && n.Root == "serve":
				out = append(out, RuntimeRoot{FQN: n.FQN, Source: RootServe, Kind: schema.EntryPointKindHTTP,
					File: m.File, Line: n.Line, Path: EdgeFunctionPath(m.File),
					Calls: append([]string(nil), g.out[n.FQN]...)})
			case n.Kind == ir.NodeCron:
				r := RuntimeRoot{FQN: n.FQN, Source: RootCron, Kind: schema.EntryPointKindCron,
					File: m.File, Line: n.Line, Job: n.Job, Schedule: n.Schedule,
					Calls: append([]string(nil), g.out[n.FQN]...), Reaches: cronReach(n.FQN, edgesFrom)}
				if n.KeyComputed {
					r.Blocked = BlockedKeyComputed
				}
				out = append(out, r)
			}
		}
		for _, s := range m.Surfaces {
			kind, ok := surfaceKinds[s.Type]
			if !ok || !s.Detected || isTestFile(m) {
				continue
			}
			r := RuntimeRoot{FQN: s.Symbol, Source: RootSurface, Kind: kind, File: m.File, Line: s.Line,
				Method: s.Method, Path: s.Path}
			if kind == schema.EntryPointKindCron {
				r.Job = s.Name
			}
			if n := g.nodes[s.Symbol]; s.Symbol == "" || n == nil || !n.symbol || n.isTest {
				r.Blocked = BlockedHandlerMissing
			}
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.FQN < b.FQN
	})
	return out
}

// reachDepth is the most edges a cron job's reach follows: the job's call to
// the edge function (or to a function that posts to it), the function's serve
// call to its handler, and one call beyond.
const reachDepth = 3

// cronReach lists what a cron job's body reaches: the nodes it calls directly
// and, through an http edge (a net.http_post to an edge function), the nodes
// that function's code calls, up to reachDepth edges from the job. The http
// edge may be the job's own or that of a function the job calls directly (a
// job that runs `select public.call_enrich()`, where call_enrich posts to the
// edge function). Any other path stops at its first node, so a job that calls
// a SQL function does not cover everything that function calls. Only an edge
// of the synchronous call graph is followed: a member, publish or consume edge
// is not.
func cronReach(from string, edgesFrom map[string][]Edge) []string {
	seen := map[string]bool{}
	var out []string
	add := func(fqn string) {
		if !seen[fqn] {
			seen[fqn] = true
			out = append(out, fqn)
		}
	}
	// follow adds what direct calls reach from the frontier, one edge per
	// depth from startDepth to reachDepth.
	follow := func(frontier []string, startDepth int) {
		for depth := startDepth; depth <= reachDepth; depth++ {
			var next []string
			for _, f := range frontier {
				for _, e := range edgesFrom[f] {
					if e.Kind == "member" || isAsyncKind(e.Kind) || seen[e.To] {
						continue
					}
					add(e.To)
					next = append(next, e.To)
				}
			}
			frontier = next
		}
	}
	for _, e := range edgesFrom[from] {
		if e.Kind == "member" {
			continue
		}
		add(e.To)
		if e.Kind == ir.RefHTTP {
			follow([]string{e.To}, 2)
			continue
		}
		var posted []string
		for _, e2 := range edgesFrom[e.To] {
			if e2.Kind == ir.RefHTTP {
				add(e2.To)
				posted = append(posted, e2.To)
			}
		}
		follow(posted, 3)
	}
	sort.Strings(out)
	return out
}

// EdgeFunctionPath is the path the platform serves the edge function in file
// at: /functions/v1/<name>. Empty when file is not an edge function's entry
// file. A Deno.serve handler answers every method, so no method goes with it.
func EdgeFunctionPath(file string) string {
	if m := functionRe.FindStringSubmatch(file); m != nil {
		return "/functions/v1/" + m[1]
	}
	return ""
}

// CoveredBy reports whether an entry point somebody declared or a detector
// found already stands for the root. A proposal made from a runtime root
// (DetectedBy set) does not cover anything: it is recomputed from the roots
// on every extract. Otherwise the entry point covers the root when any of
// these holds:
//
//   - their handlers are equal;
//   - the root is a detected route and the entry point is the same route
//     (kind, method and path);
//   - the root is a serve handler and the entry point is an http_route or a
//     webhook whose handler symbol or handler file is in the serve handler's
//     module: a manifest names the function the handler calls (or the file),
//     not the serve node, which has no name of its own;
//   - the root is a serve handler and the entry point is an http_route or a
//     webhook whose trigger path equals the path the platform serves the
//     function at, whatever its method (a proposal has none); or whose handler
//     the serve call calls anywhere in its argument expression, at any depth
//     (Deno.serve(withSentry('n', createHandler(...))) calls createHandler,
//     which a manifest names);
//   - the root is a cron job and the entry point is a cron with the same job
//     name; or, when either names no job, with the same schedule and a handler
//     the job's body reaches: by a direct call, or by one http hop to an edge
//     function, the job's own or that of a function it calls, followed by
//     direct calls, to depth 3 (a net.http_post to /functions/v1/<fn> reaches
//     <fn>'s handler). A different schedule never
//     covers, a different job name always keeps two jobs apart, and two jobs
//     on one schedule that reach different functions are not merged.
func (r RuntimeRoot) CoveredBy(eps []schema.EntryPoint) bool {
	for _, ep := range eps {
		if ep.DetectedBy != "" {
			continue
		}
		switch {
		case r.FQN != "" && ep.Handler.Symbol == r.FQN:
			return true
		case r.Source == RootSurface && ep.Kind == r.Kind && r.Path != "" &&
			ep.Trigger.Path == r.Path && strings.EqualFold(ep.Trigger.Method, r.Method):
			return true
		case r.Source == RootServe && (r.inServeModule(ep) || r.servedAt(ep) || r.callsHandler(ep)):
			return true
		case r.Source != RootServe && ep.Kind == schema.EntryPointKindCron && r.Kind == ep.Kind &&
			r.Job != "" && ep.Trigger.Job == r.Job:
			return true
		case r.Source == RootCron && ep.Kind == schema.EntryPointKindCron && (ep.Trigger.Job == "" || r.Job == "") &&
			r.Schedule != "" && ep.Trigger.Schedule == r.Schedule && r.reaches(ep.Handler.Symbol):
			return true
		}
	}
	return false
}

// inServeModule reports an http_route or webhook entry point whose handler
// lies in the module of the serve root r.
func (r RuntimeRoot) inServeModule(ep schema.EntryPoint) bool {
	if ep.Kind != schema.EntryPointKindHTTP && ep.Kind != schema.EntryPointKindWebhook {
		return false
	}
	if f := strings.TrimPrefix(strings.ReplaceAll(ep.Handler.File, "\\", "/"), "./"); f != "" && f == r.File {
		return true
	}
	mod := strings.TrimSuffix(r.FQN, "<serve>") // "<module>."
	sym := ep.Handler.Symbol
	return mod != "" && mod != "." && sym != "" && strings.HasPrefix(sym, mod)
}

// servedAt reports an http_route or webhook entry point declared at the path
// the serve root r is served at, with any method.
func (r RuntimeRoot) servedAt(ep schema.EntryPoint) bool {
	if ep.Kind != schema.EntryPointKindHTTP && ep.Kind != schema.EntryPointKindWebhook {
		return false
	}
	trim := func(p string) string { return strings.TrimRight(p, "/") }
	return r.Path != "" && trim(ep.Trigger.Path) == trim(r.Path)
}

// callsHandler reports an http_route or webhook entry point whose handler the
// serve root r calls: the adapter records every call in the serve call's
// arguments as the serve node's own, however deeply it is nested.
func (r RuntimeRoot) callsHandler(ep schema.EntryPoint) bool {
	return (ep.Kind == schema.EntryPointKindHTTP || ep.Kind == schema.EntryPointKindWebhook) &&
		containsFQN(r.Calls, ep.Handler.Symbol)
}

// reaches reports that the root's body reaches the node fqn (see Reaches).
func (r RuntimeRoot) reaches(fqn string) bool { return containsFQN(r.Reaches, fqn) }

func containsFQN(list []string, fqn string) bool {
	if fqn == "" {
		return false
	}
	for _, c := range list {
		if c == fqn {
			return true
		}
	}
	return false
}

// isTestFile reports a module that holds only test code: every symbol and
// node in it is a test's.
func isTestFile(m *ir.Module) bool {
	if len(m.Symbols) == 0 {
		return false
	}
	for _, s := range m.Symbols {
		if !s.IsTest {
			return false
		}
	}
	return true
}

// Reached returns the nodes the call graph reaches from handler through
// resolved edges, handler included. A reference left ambiguous does not
// count: a flow step built on it would be a guess. Empty when the graph has
// no such node.
func (g *Graph) Reached(handler string) map[string]bool {
	if g.nodes[handler] == nil {
		return map[string]bool{}
	}
	must, _ := g.walk([]string{handler})
	return must
}

// HasNode reports whether fqn is a node of the graph: a symbol, or a root
// node such as a serve handler or a cron job.
func (g *Graph) HasNode(fqn string) bool { return g.nodes[fqn] != nil }

// RuntimeRootFindings raises RUNTIME_ROOT_UNPROPOSED for every runtime root
// that cannot become an entry point, so no root the runtime invokes is
// silent: it is an entry point (declared, detected or proposed) or it is
// named here. Info severity: a proposal is not an error, and neither is a
// root the graph cannot name.
// @enforces entrypoints:INV-5
func RuntimeRootFindings(kg schema.KnowledgeGraph, r *Result) []schema.Violation {
	if r == nil || r.Graph == nil {
		return nil
	}
	var out []schema.Violation
	for _, rt := range r.Graph.RuntimeRoots() {
		if rt.Blocked == "" || rt.CoveredBy(kg.EntryPoints) {
			continue
		}
		var msg, detail string
		switch rt.Blocked {
		case BlockedHandlerMissing:
			msg = fmt.Sprintf("%s %s %s has no handler symbol in the graph (an inline function, or a name the module does not define), so it cannot be proposed as an entry point",
				rt.Kind, rt.Method, rt.Path)
			detail = "name the handler as a function of its own, or declare the entry point in lattice/entry-points/"
		case BlockedKeyComputed:
			msg = fmt.Sprintf("the cron job at %s:%d has a computed name or schedule, so it has no stable key and cannot be proposed as an entry point", rt.File, rt.Line)
			detail = "write the job name and schedule as string literals, or declare the entry point in lattice/entry-points/"
		default:
			continue
		}
		subject := rt.FQN
		if subject == "" {
			subject = fmt.Sprintf("%s:%d", rt.File, rt.Line)
		}
		out = append(out, schema.Violation{
			Code: schema.CodeRuntimeRootUnproposed, Severity: schema.SeverityInfo,
			Subject: subject, Message: msg,
			Location:   &schema.Location{File: rt.File, Line: rt.Line},
			NextAction: &schema.NextAction{Kind: "edit_entry_point", Detail: detail},
		})
	}
	return out
}
