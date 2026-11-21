package breaktest

// @module-feature breaktest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// MethodEdge is the method of a run that broke a hop (an edge of the reach
// graph) instead of an enforcer: the literal that names the hop's target, at
// its out end, is altered so the key no longer matches.
const MethodEdge = "edge"

// CodeEdgeBreakSurvived is the finding of a survived edge break: no verifier
// of the promise noticed the hop breaking. It is info, like the other
// findings about hops (HTTP_CALL_UNSERVED).
const CodeEdgeBreakSurvived = "EDGE_BREAK_SURVIVED"

// MaxEdgesPerPromise caps the hops one promise has edge-broken in a run; the
// rest are counted in a note on the invocation's stderr.
const MaxEdgesPerPromise = 20

// EdgeSuffix is what an edge break appends to a name: the last path segment
// of a request, the name of an rpc, a table or a channel. It is a fixed text,
// so the same hop always breaks the same way.
const EdgeSuffix = "-lattice-break"

// Hop names the hop an edge run broke, as the reach graph's edge: the symbol
// whose code holds the out end, the node it reaches, and the edge kind.
type Hop struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// EdgeSite is one string literal in source that names a hop's target (see
// reach.Site): a byte span of File, quotes included, and the key the adapter
// read from it.
type EdgeSite struct {
	File  string
	Line  int
	Start int
	End   int
	Key   string
}

// EdgeHop is one hop on a promise's slice paths that an edge break may try.
// A hop with Skip set is reported and never broken.
type EdgeHop struct {
	Hop
	// Sites are the literals that make the hop. A hop with none is skipped:
	// its key is computed, or the adapter does not record where it is.
	Sites []EdgeSite
	// Skip says why the hop is not broken (an unknown hop, an ambiguous
	// reference), "" when it may be.
	Skip string
	// Served reports whether a route of the workspace answers the request
	// key "METHOD /path". It lets an http break choose an alteration the
	// routes cannot absorb (a path parameter matches any segment). Nil means
	// no route knowledge: the last segment is altered.
	Served func(key string) bool
}

// edgeKinds are the hop kinds whose out end is a literal name: a request, a
// database function, a table, a channel.
var edgeKinds = map[string]bool{ir.RefHTTP: true, ir.RefRPC: true, ir.RefTable: true, ir.RefPublish: true}

// EdgeKind reports whether hops of the kind can be edge-broken at all.
func EdgeKind(kind string) bool { return edgeKinds[kind] }

// edgeTargets lists the hops of one promise to break: hops that are skipped
// first reported as such, then each hop with a literal once, by the position
// of its first literal, up to MaxEdgesPerPromise.
// @enforces breaktest:INV-19
func edgeTargets(hops []EdgeHop) (try, skipped []EdgeHop, over int) {
	seenHop := map[Hop]bool{}
	seenAt := map[string]bool{}
	for _, h := range hops {
		if !EdgeKind(h.Kind) || seenHop[h.Hop] {
			continue
		}
		seenHop[h.Hop] = true
		if h.Skip == "" && len(h.Sites) == 0 {
			h.Skip = "the out end is not a literal at a known position in source"
		}
		if h.Skip != "" {
			skipped = append(skipped, h)
			continue
		}
		at := fmt.Sprintf("%s\x00%d", h.Sites[0].File, h.Sites[0].Start)
		if seenAt[at] {
			continue // another hop through the same call
		}
		seenAt[at] = true
		if len(try) == MaxEdgesPerPromise {
			over++
			continue
		}
		try = append(try, h)
	}
	less := func(s []EdgeHop) func(i, j int) bool {
		return func(i, j int) bool {
			a, b := s[i].Hop, s[j].Hop
			if a.From != b.From {
				return a.From < b.From
			}
			if a.To != b.To {
				return a.To < b.To
			}
			return a.Kind < b.Kind
		}
	}
	sort.SliceStable(skipped, less(skipped))
	return try, skipped, over
}

// alterSite is the break of one literal: an insertion of EdgeSuffix inside
// the string literal at the site, in src as the worktree holds it. It reads
// the literal back and checks it is the one the key was read from, and that
// the altered key no longer matches (for a request, that no route serves
// it); anything else is an error and the hop is not broken.
//
//   - http: the suffix ends the last path segment whose alteration leaves a
//     key no route serves, looking from the last segment to the first, so
//     GET /api/orders becomes GET /api/orders-lattice-break and
//     GET /api/orders/42, which a route /api/orders/:id also serves for any
//     last segment, becomes GET /api/orders-lattice-break/42. The method, the
//     origin and the query are kept. For functions.invoke('name') it is the
//     name.
//   - rpc, table: the suffix ends the name.
//   - publish: the suffix ends the channel name.
//
// A literal with an escape sequence or a ${} substitution is not altered.
// @enforces breaktest:INV-19
func alterSite(kind string, s EdgeSite, src []byte, served func(string) bool) (Break, error) {
	if s.Start < 0 || s.End > len(src) || s.End-s.Start < 2 {
		return Break{}, fmt.Errorf("%s:%d: the recorded literal is outside the file", s.File, s.Line)
	}
	lit := src[s.Start:s.End]
	q := lit[0]
	if (q != '\'' && q != '"' && q != '`') || lit[len(lit)-1] != q {
		return Break{}, fmt.Errorf("%s:%d: the recorded span is not a string literal", s.File, s.Line)
	}
	content := string(lit[1 : len(lit)-1])
	if strings.ContainsRune(content, '\\') || q == '`' && strings.Contains(content, "${") {
		return Break{}, fmt.Errorf("%s:%d: the literal has an escape or a substitution", s.File, s.Line)
	}
	at := -1
	switch kind {
	case ir.RefRPC, ir.RefTable:
		if content != s.Key {
			return Break{}, fmt.Errorf("%s:%d: the literal %q is not the name %q the reference was read from", s.File, s.Line, content, s.Key)
		}
		at = len(content)
	case ir.RefPublish:
		if !strings.HasSuffix(s.Key, ":"+content) {
			return Break{}, fmt.Errorf("%s:%d: the literal %q is not the channel %q the reference was read from", s.File, s.Line, content, s.Key)
		}
		at = len(content)
	case ir.RefHTTP:
		var err error
		if at, err = httpAlteration(content, s, served); err != nil {
			return Break{}, err
		}
	default:
		return Break{}, fmt.Errorf("%s:%d: %s hops have no literal out end", s.File, s.Line, kind)
	}
	return Break{Offset: s.Start + 1 + at, Text: EdgeSuffix}, nil
}

// httpAlteration picks where in the literal the suffix goes, as an offset
// into content.
func httpAlteration(content string, s EdgeSite, served func(string) bool) (int, error) {
	method, _, ok := strings.Cut(s.Key, " ")
	if !ok {
		return 0, fmt.Errorf("%s:%d: %q is not a request key", s.File, s.Line, s.Key)
	}
	start, end := 0, len(content)
	if ir.HTTPKey(method, content) != s.Key {
		// functions.invoke('name'): the literal is the function's name.
		if ir.HTTPKey(method, "/functions/v1/"+strings.Trim(content, "/")) != s.Key {
			return 0, fmt.Errorf("%s:%d: the literal %q is not the request %q the reference was read from", s.File, s.Line, content, s.Key)
		}
		for end > 0 && content[end-1] == '/' {
			end--
		}
		return end, nil
	}
	start = len(ir.HTTPOrigin(content))
	if i := strings.IndexAny(content[start:], "?#"); i >= 0 {
		end = start + i
	}
	for end > start && content[end-1] == '/' {
		end--
	}
	// Segment ends, from the last to the first.
	var ends []int
	for i := start; i < end; i++ {
		if content[i] == '/' && i > start && content[i-1] != '/' {
			ends = append(ends, i)
		}
	}
	if end > start && content[end-1] != '/' {
		ends = append(ends, end)
	}
	for i := len(ends) - 1; i >= 0; i-- {
		altered := content[:ends[i]] + EdgeSuffix + content[ends[i]:]
		if served == nil || !served(ir.HTTPKey(method, altered)) {
			return ends[i], nil
		}
	}
	if len(ends) == 0 {
		return 0, fmt.Errorf("%s:%d: the path of %q has no segment to alter", s.File, s.Line, s.Key)
	}
	return 0, fmt.Errorf("%s:%d: every altered path of %q is still served by a route", s.File, s.Line, s.Key)
}
