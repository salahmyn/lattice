package slice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// fixture builds a slice graph by hand: edges as "from kind to", ambiguous
// references as "from kind to1,to2", the layer of each symbol, and every node
// in sql.* a Postgres node.
type fixture struct {
	roots      []reach.Root
	edges      []reach.Edge
	unresolved []reach.Unresolved
	layers     map[string]string
}

func (f *fixture) edge(from, kind, to string) *fixture {
	f.edges = append(f.edges, reach.Edge{From: from, To: to, Kind: kind, Via: "target"})
	return f
}

func (f *fixture) ambiguous(from, kind string, cands ...string) *fixture {
	f.unresolved = append(f.unresolved, reach.Unresolved{From: from, Kind: kind, Why: "ambiguous", Candidates: cands})
	return f
}

func (f *fixture) root(fqn, reason string) *fixture {
	f.roots = append(f.roots, reach.Root{FQN: fqn, Reason: reason})
	return f
}

func (f *fixture) graph() *Graph {
	info := func(fqn string) (reach.NodeInfo, bool) {
		if strings.HasPrefix(fqn, "sql.") {
			return reach.NodeInfo{FQN: fqn, Language: "postgres", File: "db/schema.sql"}, true
		}
		return reach.NodeInfo{FQN: fqn, File: "lib/" + fqn + ".ts", Language: "typescript"}, true
	}
	rules := []schema.LayerRule{{Glob: "lib/**", Layer: schema.LayerService}}
	return newGraph(f.roots, f.edges, f.unresolved, f.layers, rules, info)
}

func enf(fqns ...string) []Enforcer {
	var out []Enforcer
	for _, f := range fqns {
		out = append(out, Enforcer{FQN: f, Reach: "entry"})
	}
	return out
}

// A client function reads a table, and the table's policy is the enforcer:
// the slice lists the client root, the table hop, the layers it is carried
// by, and reads complete. The table node is no symbol, so it is data by the
// adapter fact of its language.
// @verifies reachability:INV-10
func TestSliceThroughATable(t *testing.T) {
	f := &fixture{layers: map[string]string{"web.orders.load": "ui", "sql.policy.public.orders.reads": "data"}}
	f.root("web.orders.load", "entry point ep.http.get_orders").
		root("sql.policy.public.orders.reads", "policy").
		edge("web.orders.load", "table", "sql.table.public.orders").
		edge("sql.table.public.orders", "table", "sql.policy.public.orders.reads")
	rec := Compute(f.graph(), "orders:INV-2", enf("sql.policy.public.orders.reads"))

	if rec.Contract != Contract || rec.Reading != ReadingComplete || rec.Hop != 0 {
		t.Fatalf("record = %+v", rec)
	}
	var client *Path
	for i := range rec.Paths {
		if rec.Paths[i].Root.FQN == "web.orders.load" {
			client = &rec.Paths[i]
		}
	}
	if client == nil {
		t.Fatalf("no path from the client root: %+v", rec.Paths)
	}
	if client.Root.EntryPoint != "ep.http.get_orders" || !reflect.DeepEqual(client.CarriedBy, []string{"ui", "data"}) {
		t.Errorf("root %+v, carried by %v", client.Root, client.CarriedBy)
	}
	want := []Hop{
		{From: "web.orders.load", To: "sql.table.public.orders", Kind: "table", FromLayer: "ui", ToLayer: "data", Truth: TruthResolved},
		{From: "sql.table.public.orders", To: "sql.policy.public.orders.reads", Kind: "table", FromLayer: "data", ToLayer: "data", Truth: TruthResolved},
	}
	if !reflect.DeepEqual(client.Hops, want) {
		t.Errorf("hops = %+v", client.Hops)
	}
	// The policy is a root too: a path of no hops, carried by data alone.
	if len(rec.Paths) != 2 || len(rec.Paths[0].Hops)+len(rec.Paths[1].Hops) != 2 {
		t.Errorf("paths = %+v", rec.Paths)
	}
}

// A node that is no symbol and no Postgres node takes the layer a declared
// rule gives its file, else unknown.
func TestLayerOfAPseudoNode(t *testing.T) {
	g := (&fixture{}).graph()
	if got := g.layerOf("tool"); got != schema.LayerService {
		t.Errorf("a file under lib/** = %q", got)
	}
	g.rules = nil
	if got := g.layerOf("tool"); got != schema.LayerUnknown {
		t.Errorf("no rule = %q", got)
	}
	if got := g.layerOf("sql.table.public.x"); got != schema.LayerData {
		t.Errorf("a Postgres node = %q", got)
	}
}

// The only way to the enforcer is an ambiguous reference followed to a
// candidate: the hop is unknown, the reading names it, and it is never
// complete.
// @verifies reachability:INV-10
func TestOnlyPathThroughAnUnknownHop(t *testing.T) {
	f := &fixture{layers: map[string]string{"app.handler": "api", "app.prepare": "ui", "app.A.run": "service"}}
	f.root("app.handler", "entry point ep.http.x").
		edge("app.handler", "call", "app.prepare").
		ambiguous("app.prepare", "call", "app.A.run", "app.B.run")
	rec := Compute(f.graph(), "f:INV-1", enf("app.A.run"))
	if rec.Reading != ReadingUnknownFromHop || rec.Hop != 2 {
		t.Fatalf("reading %q hop %d: %s", rec.Reading, rec.Hop, rec.Why)
	}
	p := rec.Paths[0]
	if len(p.Hops) != 2 || p.Hops[0].Truth != TruthResolved || p.Hops[1].Truth != TruthUnknown || p.Hops[1].Kind != "call" {
		t.Errorf("hops = %+v", p.Hops)
	}
	if !reflect.DeepEqual(p.CarriedBy, []string{"api", "ui", "service"}) {
		t.Errorf("carried by %v", p.CarriedBy)
	}
}

// A reference with no recorded candidate is not followed: a dynamic or
// missing one leaves the enforcer unreached.
func TestUnresolvedWithoutCandidatesIsNotFollowed(t *testing.T) {
	f := &fixture{}
	f.root("a", "main").edge("a", "call", "b")
	f.unresolved = append(f.unresolved,
		reach.Unresolved{From: "b", Kind: "rpc", Why: "dynamic"},
		reach.Unresolved{From: "b", Kind: "call", Why: "missing", Candidates: []string{"e"}})
	rec := Compute(f.graph(), "f:INV-1", enf("e"))
	if rec.Reading != ReadingUnreached || len(rec.Paths) != 0 || !reflect.DeepEqual(rec.Unreached, []string{"e"}) {
		t.Errorf("record = %+v", rec)
	}
}

// Some enforcer with no path reads unreached, before an unknown hop on
// another enforcer's path is looked at.
// @verifies reachability:INV-10
func TestUnreachedBeatsUnknownHop(t *testing.T) {
	f := &fixture{}
	f.root("a", "main").ambiguous("a", "call", "x", "y")
	rec := Compute(f.graph(), "f:INV-1", enf("x", "orphan"))
	if rec.Reading != ReadingUnreached || !reflect.DeepEqual(rec.Unreached, []string{"orphan"}) {
		t.Errorf("record = %+v", rec)
	}
	if len(rec.Paths) != 1 || rec.Paths[0].Hops[0].Truth != TruthUnknown {
		t.Errorf("the reachable enforcer still lists its path: %+v", rec.Paths)
	}
}

// A path over resolved edges wins over a shorter one through an unknown hop,
// so the enforcer reads complete when a root resolves all the way to it.
func TestResolvedPathBeatsShorterUnknownOne(t *testing.T) {
	f := &fixture{}
	f.root("a", "main").
		ambiguous("a", "call", "e", "z").
		edge("a", "call", "m").edge("m", "call", "n").edge("n", "call", "e")
	rec := Compute(f.graph(), "f:INV-1", enf("e"))
	if rec.Reading != ReadingComplete || len(rec.Paths) != 1 || len(rec.Paths[0].Hops) != 3 {
		t.Fatalf("record = %+v", rec)
	}
}

// Another root that reaches the enforcer only through an unknown hop is
// listed, marked unknown, and keeps the slice from reading complete, even
// though a resolved path exists from the first root.
// @verifies reachability:INV-10
func TestAnotherRootsUnknownPathKeepsItFromComplete(t *testing.T) {
	f := &fixture{}
	f.root("a", "main").root("b", "main").edge("a", "call", "e").ambiguous("b", "call", "e", "z")
	rec := Compute(f.graph(), "f:INV-1", enf("e"))
	if rec.Reading != ReadingUnknownFromHop || rec.Hop != 1 || len(rec.Paths) != 2 {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Paths[0].Root.FQN != "b" {
		t.Errorf("a path with an unknown hop is listed first: %+v", rec.Paths)
	}
}

func TestReadingsWithoutAGraph(t *testing.T) {
	f := &fixture{}
	f.root("a", "main").edge("a", "call", "e")
	for name, c := range map[string]struct {
		g    *Graph
		enf  []Enforcer
		want string
	}{
		"no enforcer":             {f.graph(), nil, ReadingNoEnforcer},
		"no enforcer, no graph":   {nil, nil, ReadingNoEnforcer},
		"no reach graph":          {nil, enf("e"), ReadingUnknown},
		"a graph with no roots":   {(&fixture{}).edge("a", "call", "e").graph(), enf("e"), ReadingUnknown},
		"the enforcer is a root":  {(&fixture{}).root("e", "policy").graph(), enf("e"), ReadingComplete},
		"a root reaches it":       {f.graph(), enf("e"), ReadingComplete},
		"it is not in the graph":  {f.graph(), enf("ghost"), ReadingUnreached},
		"two enforcers, one lost": {f.graph(), enf("e", "ghost"), ReadingUnreached},
	} {
		if got := Compute(c.g, "f:INV-1", c.enf).Reading; got != c.want {
			t.Errorf("%s: reading %q, want %q", name, got, c.want)
		}
	}
}

// The path is the same on every run and whatever order the edges come in:
// breadth first, neighbours sorted, so the diamond a->{b,c}->e takes b.
func TestShortestPathIsDeterministic(t *testing.T) {
	build := func(edges [][3]string) string {
		f := &fixture{}
		f.root("a", "main")
		for _, e := range edges {
			f.edge(e[0], e[1], e[2])
		}
		b, _ := json.Marshal(Compute(f.graph(), "f:INV-1", enf("e")))
		return string(b)
	}
	one := build([][3]string{{"a", "call", "b"}, {"a", "call", "c"}, {"b", "call", "e"}, {"c", "call", "e"}})
	two := build([][3]string{{"c", "call", "e"}, {"b", "call", "e"}, {"a", "call", "c"}, {"a", "call", "b"}})
	if one != two {
		t.Errorf("order of edges changed the slice:\n%s\n%s", one, two)
	}
	if !strings.Contains(one, `"to":"b"`) || strings.Contains(one, `"to":"c"`) {
		t.Errorf("the diamond must take b:\n%s", one)
	}
}

// At most MaxPaths are listed and the rest counted, and the reading is
// decided from every path: the unknown one, listed first, survives the cut.
// @verifies reachability:INV-10
func TestPathsAreBounded(t *testing.T) {
	f := &fixture{}
	for i := 0; i < 60; i++ {
		r := fmt.Sprintf("r%02d", i)
		f.root(r, "main")
		if i == 59 {
			f.ambiguous(r, "call", "e", "z")
		} else {
			f.edge(r, "call", "e")
		}
	}
	rec := Compute(f.graph(), "f:INV-1", enf("e"))
	if len(rec.Paths) != MaxPaths || rec.Truncated != 10 {
		t.Fatalf("%d paths, %d truncated", len(rec.Paths), rec.Truncated)
	}
	if rec.Reading != ReadingUnknownFromHop || rec.Paths[0].Root.FQN != "r59" {
		t.Errorf("reading %q, first path %+v", rec.Reading, rec.Paths[0])
	}
}

// The shape of lattice.slice/1 is frozen by a golden file. Fields may only be
// added. Run with UPDATE_GOLDEN=1 to rewrite it after a deliberate change.
func TestSliceContractGolden(t *testing.T) {
	f := &fixture{layers: map[string]string{"web.orders.load": "ui", "sql.policy.public.orders.reads": "data"}}
	f.root("web.orders.load", "entry point ep.http.get_orders").
		edge("web.orders.load", "table", "sql.table.public.orders").
		edge("sql.table.public.orders", "table", "sql.policy.public.orders.reads").
		root("web.other", "entry file web/main.ts").
		edge("web.other", "call", "lib.helper").
		ambiguous("lib.helper", "call", "sql.policy.public.orders.reads", "lib.other")
	rec := Compute(f.graph(), "orders:INV-2", []Enforcer{{FQN: "sql.policy.public.orders.reads", Layer: "data", Reach: "entry"}})
	rec.Commit = "abc123"
	got, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", "slice.golden.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1)", err)
	}
	if string(got) != string(want) {
		t.Errorf("lattice.slice/1 differs from %s (UPDATE_GOLDEN=1 rewrites it):\n%s", golden, got)
	}
}

func TestWriteText(t *testing.T) {
	f := &fixture{layers: map[string]string{"web.load": "ui", "sql.policy.p": "data"}}
	f.root("web.load", "entry point ep.http.get").ambiguous("web.load", "call", "sql.policy.p", "other")
	var buf bytes.Buffer
	WriteText(&buf, Compute(f.graph(), "orders:INV-2", []Enforcer{{FQN: "sql.policy.p", Layer: "data", Reach: "entry"}}))
	out := buf.String()
	for _, want := range []string{
		"slice orders:INV-2", "reading: unknown_from_hop (hop 1)", "sql.policy.p  [data]  reach: entry",
		"web.load [ui] -call?-> sql.policy.p [data]", "root: entry point ep.http.get; carried by ui -> data; 1 hop", "kind? marks an unknown hop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
}
