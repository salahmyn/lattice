package reach

// @module-feature reachability

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// The reasons an http reference stays unresolved (Unresolved.Why).
const (
	WhyNoRoute = "no_route" // a literal request that no extracted route serves
)

// anyMethod on an end means it answers every method.
const anyMethod = "ANY"

// functionRe finds the edge function a module's file is: the platform serves
// supabase/functions/<name>/index.<ext> at /functions/v1/<name>. A directory
// that starts with an underscore holds shared code, not a function.
var functionRe = regexp.MustCompile(`(?:^|/)supabase/functions/([^/_][^/]*)/index\.(?:ts|tsx|js|jsx|mts|mjs)$`)

// httpEnd is one route the code serves: the in end of an http join.
type httpEnd struct {
	method  string   // upper-case, or anyMethod
	segs    []string // path segments, parameters as paramSeg
	handler string   // FQN of the node that runs when it is called
}

// paramSeg stands for a path parameter in an end's or a call's segments.
const paramSeg = "\x00param"

// httpEnds holds every route end of the workspace.
type httpEnds []httpEnd

// pathSegments splits a path into segments and turns each parameter form
// into paramSeg: :id, {id}, {id?} and [id]. ok is false for a path with a
// catch-all ([...rest], [[...rest]], *, :rest*), which stands for any number
// of segments and so cannot be matched by equality.
func pathSegments(p string) (segs []string, ok bool) {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	for _, s := range strings.Split(p, "/") {
		switch {
		case s == "":
			continue
		case s == "*", strings.HasPrefix(s, "[...") || strings.HasPrefix(s, "[[..."), strings.HasPrefix(s, ":") && strings.HasSuffix(s, "*"):
			return nil, false
		case strings.HasPrefix(s, ":"), strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"),
			strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
			segs = append(segs, paramSeg)
		default:
			segs = append(segs, s)
		}
	}
	return segs, true
}

// collectHTTPEnds gathers the routes a workspace serves, from code only:
// the routes an adapter detected, the http_route entry points a person
// declared (a proposal is derived from the same roots and adds nothing), and
// each edge function's serve handler, at the path the platform gives it. A
// route whose handler is not a node of the graph is left out: there is
// nothing to draw an edge to.
func (g *Graph) collectHTTPEnds(eps []schema.EntryPoint) httpEnds {
	var ends httpEnds
	add := func(method, path, handler string) {
		segs, ok := pathSegments(path)
		n := g.nodes[handler]
		if !ok || n == nil || n.isTest || method == "" || path == "" {
			return
		}
		method = strings.ToUpper(method)
		if method == "*" {
			method = anyMethod
		}
		ends = append(ends, httpEnd{method: method, segs: segs, handler: handler})
	}
	for mi := range g.modules {
		m := &g.modules[mi]
		if isTestFile(m) {
			continue
		}
		for _, s := range m.Surfaces {
			if s.Type == string(schema.SurfaceHTTP) && s.Detected {
				add(s.Method, s.Path, s.Symbol)
			}
		}
		if fnPath := EdgeFunctionPath(m.File); fnPath != "" {
			for _, n := range m.Nodes {
				if n.Kind == ir.NodeServe && n.Root == "serve" && !n.IsTest {
					add(anyMethod, fnPath, n.FQN)
				}
			}
		}
	}
	for _, ep := range eps {
		if ep.Kind == schema.EntryPointKindHTTP && ep.DetectedBy == "" {
			add(ep.Trigger.Method, ep.Trigger.Path, ep.Handler.Symbol)
		}
	}
	return ends
}

// match returns the handlers of every end the key "METHOD /path" equals, once
// each, sorted. A literal segment equals an equal literal segment or a
// parameter; a parameter equals a parameter or a literal. Two literal segments
// must be equal and the segment counts must agree: nothing is matched by
// prefix.
// @enforces reachability:INV-11
func (e httpEnds) match(key string) []string {
	method, path, ok := strings.Cut(key, " ")
	if !ok {
		return nil
	}
	call, ok := pathSegments(path)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, end := range e {
		if end.method != anyMethod && end.method != method || len(end.segs) != len(call) || seen[end.handler] {
			continue
		}
		equal := true
		for i, s := range end.segs {
			if s != call[i] && s != paramSeg {
				equal = false
				break
			}
		}
		if equal {
			seen[end.handler] = true
			out = append(out, end.handler)
		}
	}
	sort.Strings(out)
	return out
}

// HTTPServes reports whether a route the workspace serves answers the key
// "METHOD /path": the join a request resolves by. A consumer that alters a
// request's path asks it whether the altered request would still be served
// (a path parameter matches any literal segment).
func (g *Graph) HTTPServes(key string) bool { return len(g.http.match(key)) > 0 }

// resolveHTTP joins one request to the route that serves it, by key. Only a
// path-only URL, an edge function call and an absolute URL on a declared
// origin take part: an absolute URL on any other origin is external, makes
// no edge and no finding, since nothing extracted says the origin is ours. A
// computed key makes no edge ("dynamic"), a key no route serves none
// (no_route), and a key several routes serve is ambiguous, with every
// handler as a candidate.
// @enforces reachability:INV-11
func (b *builder) resolveHTTP(r ir.Ref, resolved func(string, string), unresolved func(string, []string), external func()) {
	if r.Qualifier != "" && r.Name != "" && !b.ownOrigin(r.Qualifier) {
		external() // an absolute URL on an origin nothing declares ours: another service
		return
	}
	if r.Name == "" {
		unresolved("dynamic", nil)
		return
	}
	switch hs := b.http.match(r.Name); len(hs) {
	case 0:
		unresolved(WhyNoRoute, nil)
	case 1:
		resolved(hs[0], "route")
	default:
		unresolved("ambiguous", hs)
	}
}

// ownOrigin reports whether the workspace declares the origin as its own.
func (b *builder) ownOrigin(origin string) bool {
	for _, o := range b.origins {
		if ir.SameHTTPOrigin(o, origin) {
			return true
		}
	}
	return false
}

// firstLines is how many call sites an aggregated finding lists.
const firstLines = 3

// HTTPFindings raises the findings of the http join, at info:
//
//   - HTTP_CALL_UNSERVED, one per literal request that no extracted route
//     serves (a dangling out end). A request to an origin the workspace does
//     not declare its own is external and never reaches here.
//   - HTTP_CALL_DYNAMIC, one per file: the count of its requests whose URL or
//     method is computed, and the first lines, so a large codebase is not
//     flooded.
//
// A route nothing calls raises nothing: it may be public API. Test code is
// left out.
// @enforces reachability:INV-11
func HTTPFindings(r *Result) []schema.Violation {
	if r == nil || r.Graph == nil {
		return nil
	}
	var out []schema.Violation
	byFile := map[string][]Unresolved{}
	var files []string
	for _, u := range r.Graph.Unresolved {
		if u.Kind != ir.RefHTTP {
			continue
		}
		if n := r.Graph.nodes[u.From]; n != nil && n.isTest {
			continue
		}
		switch {
		case u.Why == WhyNoRoute:
			out = append(out, schema.Violation{
				Code: schema.CodeHTTPCallUnserved, Severity: schema.SeverityInfo,
				Subject:  fmt.Sprintf("%s:%d", u.File, u.Line),
				Message:  fmt.Sprintf("%s sends %s, and no route extracted from code serves it (evidence: static call graph, literal request key)", u.From, u.Name),
				Location: &schema.Location{File: u.File, Line: u.Line},
				NextAction: &schema.NextAction{Kind: "edit_code",
					Detail: "fix the path or the method, or declare the route's entry point in lattice/entry-points/ if the server registers it in a way the adapters do not detect"},
			})
		case u.Why == "dynamic":
			if byFile[u.File] == nil {
				files = append(files, u.File)
			}
			byFile[u.File] = append(byFile[u.File], u)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		us := byFile[f]
		lines := make([]string, 0, firstLines)
		for _, u := range us {
			if len(lines) < firstLines {
				lines = append(lines, fmt.Sprint(u.Line))
			}
		}
		more := ""
		if len(us) > len(lines) {
			more = fmt.Sprintf(" and %d more", len(us)-len(lines))
		}
		out = append(out, schema.Violation{
			Code: schema.CodeHTTPCallDynamic, Severity: schema.SeverityInfo,
			Subject:  f,
			Message:  fmt.Sprintf("%s sends %d request(s) whose URL or method is computed, so which route they call is unknown (lines %s%s; evidence: static call graph)", f, len(us), strings.Join(lines, ", "), more),
			Location: &schema.Location{File: f, Line: us[0].Line},
			NextAction: &schema.NextAction{Kind: "edit_code",
				Detail: "write the path and the method as literals where the call is fixed; a computed URL never makes an edge"},
		})
	}
	return out
}
