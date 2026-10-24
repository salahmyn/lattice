package scip

import (
	"reflect"
	"sort"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

// occ is a tiny helper to construct SCIP Occurrence values without
// the protobuf-builder verbosity.
func occ(line int32, sym string, isDef bool) *scippb.Occurrence {
	roles := int32(0)
	if isDef {
		roles = int32(scippb.SymbolRole_Definition)
	}
	return &scippb.Occurrence{
		Range:       []int32{line, 0, line, 1},
		Symbol:      sym,
		SymbolRoles: roles,
	}
}

// TestCallGraphMostRecentDef proves the "most recent definition wins"
// heuristic correctly attributes a reference to the function it sits
// inside, even when several functions are defined in the same file.
// @verifies scip:INV-1
func TestCallGraphMostRecentDef(t *testing.T) {
	doc := &scippb.Document{
		RelativePath: "ctrl.php",
		Occurrences: []*scippb.Occurrence{
			occ(0, "scip-php php . . . App.Ctrl#store().", true),      // def at line 1
			occ(5, "scip-php php . . . App.Service#charge().", false), // ref in store's body
			occ(15, "scip-php php . . . App.Ctrl#show().", true),      // def at line 16
			occ(18, "scip-php php . . . App.Service#read().", false),  // ref in show's body
		},
	}
	c := &Corpus{indexes: []*scippb.Index{{Documents: []*scippb.Document{doc}}}}

	g := c.BuildCallGraph()
	if got := g.CalleesOf("scip-php php . . . App.Ctrl#store()."); !reflect.DeepEqual(got, []string{"scip-php php . . . App.Service#charge()."}) {
		t.Errorf("store callees = %v", got)
	}
	if got := g.CalleesOf("scip-php php . . . App.Ctrl#show()."); !reflect.DeepEqual(got, []string{"scip-php php . . . App.Service#read()."}) {
		t.Errorf("show callees = %v", got)
	}
}

// With enclosing ranges, a reference goes to the innermost definition
// that contains it: top-level code after a function is credited to no
// one, and a reference after a nested definition goes back to its outer
// one. A definition with no range keeps the most-recent rule.
// @verifies scip:INV-1
func TestCallGraphEnclosingRange(t *testing.T) {
	def := func(start, end int32, sym string) *scippb.Occurrence {
		o := occ(start, sym, true)
		o.EnclosingRange = []int32{start, 0, end, 1}
		return o
	}
	const (
		a, b, inner, c = "ts . a().", "ts . b().", "ts . b().inner().", "ts . c()."
		x, y, z, w, v  = "ts . x().", "ts . y().", "ts . z().", "ts . w().", "ts . v()."
	)
	doc := &scippb.Document{RelativePath: "app.ts", Occurrences: []*scippb.Occurrence{
		def(0, 2, a),
		occ(1, x, false), // in a
		occ(4, y, false), // top-level code after a
		def(10, 20, b),
		def(12, 14, inner),
		occ(13, z, false), // in inner
		occ(16, w, false), // in b, after inner ended
		occ(30, v, true),  // c below has no range: the fallback still applies
		occ(25, c, true),
		occ(28, v, false), // credited to c
	}}
	g := (&Corpus{indexes: []*scippb.Index{{Documents: []*scippb.Document{doc}}}}).BuildCallGraph()
	for caller, want := range map[string][]string{a: {x}, inner: {z}, b: {w}, c: {v}} {
		if got := g.CalleesOf(caller); !reflect.DeepEqual(got, want) {
			t.Errorf("%s callees = %v, want %v", caller, got, want)
		}
	}
	for _, callees := range g.callees {
		if callees[y] {
			t.Errorf("top-level reference %s was credited to a function: %v", y, g.callees)
		}
	}
}

// TestCallGraphTransitive proves BFS visits every reachable symbol
// without revisiting and respects the depth cap so a deep service
// chain can't blow up.
func TestCallGraphTransitive(t *testing.T) {
	// Build a chain: A calls B, B calls C, C calls D.
	docs := []*scippb.Document{
		{RelativePath: "a.php", Occurrences: []*scippb.Occurrence{
			occ(0, "A", true), occ(1, "B", false),
		}},
		{RelativePath: "b.php", Occurrences: []*scippb.Occurrence{
			occ(0, "B", true), occ(1, "C", false),
		}},
		{RelativePath: "c.php", Occurrences: []*scippb.Occurrence{
			occ(0, "C", true), occ(1, "D", false),
		}},
	}
	c := &Corpus{indexes: []*scippb.Index{{Documents: docs}}}
	g := c.BuildCallGraph()

	got := g.TransitiveCallees("A", 4)
	sort.Strings(got)
	want := []string{"B", "C", "D"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("transitive = %v, want %v", got, want)
	}

	// Depth-1 walk visits only the direct callees.
	if got := g.TransitiveCallees("A", 1); !reflect.DeepEqual(got, []string{"B"}) {
		t.Errorf("depth-1 = %v, want [B]", got)
	}
}

// TestResolveSymbolBySuffix proves a Lattice IR FQN with backslashes
// joins to a SCIP descriptor with dots — the cross-format match every
// flow trace lookup depends on.
func TestResolveSymbolBySuffix(t *testing.T) {
	doc := &scippb.Document{
		Occurrences: []*scippb.Occurrence{
			occ(0, "scip-php php . . . App.Http.Foo#store().", true),
			occ(10, "scip-php php . . . App.Http.Bar#index().", true),
		},
	}
	c := &Corpus{indexes: []*scippb.Index{{Documents: []*scippb.Document{doc}}}}

	if got := c.ResolveSymbol(`App\Http\Foo::store`); got != "scip-php php . . . App.Http.Foo#store()." {
		t.Errorf("resolve = %q", got)
	}
	if got := c.ResolveSymbol(`App\Http\Bar::index`); got != "scip-php php . . . App.Http.Bar#index()." {
		t.Errorf("resolve = %q", got)
	}
	if got := c.ResolveSymbol(`Totally\Unknown::method`); got != "" {
		t.Errorf("unknown should be empty, got %q", got)
	}
}
