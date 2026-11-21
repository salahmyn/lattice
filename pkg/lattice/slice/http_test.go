package slice

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func parseFiles(t *testing.T, a adapters.LanguageAdapter, files map[string]string) []ir.Module {
	t.Helper()
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var mods []ir.Module
	for _, p := range paths {
		m, err := a.Parse(context.Background(), p, []byte(files[p]))
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		mods = append(mods, m)
	}
	if r, ok := a.(adapters.Resolver); ok {
		mods = r.Resolve(mods)
	}
	return mods
}

const ordersPolicy = `create policy "reads" on public.orders for select using (true);
`

// httpSlice builds the slice graph of a UI that fetches, API routes and a
// table policy, from the real adapters, with layers by path.
func httpSlice(t *testing.T, api map[string]string) *Graph {
	t.Helper()
	mods := parseFiles(t, typescript.New(), map[string]string{
		"web/orders.ts": "export function load() { return fetch('/api/orders'); }\n",
	})
	mods = append(mods, parseFiles(t, typescript.New(), api)...)
	mods = append(mods, parseFiles(t, postgres.New(nil, nil), map[string]string{"db/orders.sql": ordersPolicy})...)
	rules := []schema.LayerRule{{Glob: "web/**", Layer: schema.LayerUI}, {Glob: "api/**", Layer: schema.LayerAPI}, {Glob: "db/**", Layer: schema.LayerData}}
	var kg schema.KnowledgeGraph
	for _, m := range mods {
		for _, s := range m.Symbols {
			kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: s.FQN, File: s.File, Layer: schema.LayerOfFile(rules, s.File)})
		}
	}
	rr := reach.Compute(reach.Input{Modules: mods,
		Declared: []config.CoverageRoot{{Symbol: "web.orders.load", Reason: "the page"}}})
	return NewGraph(rr, kg, rules)
}

// A UI fetch, the API route handler it joins to, and the SQL table policy the
// handler reaches: the slice carries the promise through ui, api and data, the
// http hop is resolved, and the reading is complete.
// @verifies reachability:INV-11
func TestSliceThroughAnHTTPEdge(t *testing.T) {
	g := httpSlice(t, map[string]string{"api/orders.ts": "function listOrders() { return supabase.from('orders').select(); }\napp.get('/api/orders', listOrders);\n"})
	rec := Compute(g, "orders:INV-1", enf("sql.policy.public.orders.reads"))
	if rec.Reading != ReadingComplete {
		t.Fatalf("reading %q: %s", rec.Reading, rec.Why)
	}
	var ui *Path
	for i := range rec.Paths {
		if rec.Paths[i].Root.FQN == "web.orders.load" {
			ui = &rec.Paths[i]
		}
	}
	if ui == nil {
		t.Fatalf("no path from the UI root: %+v", rec.Paths)
	}
	if !reflect.DeepEqual(ui.CarriedBy, []string{"ui", "api", "data"}) {
		t.Errorf("carried by %v, want [ui api data]", ui.CarriedBy)
	}
	if len(ui.Hops) < 1 || ui.Hops[0].Kind != "http" || ui.Hops[0].Truth != TruthResolved ||
		ui.Hops[0].To != "api.orders.listOrders" || ui.Hops[0].FromLayer != "ui" || ui.Hops[0].ToLayer != "api" {
		t.Errorf("first hop = %+v", ui.Hops)
	}
}

// Two routes serve the request: the join is ambiguous, so the only way to the
// enforcer is an unknown hop of kind http, and the slice is not complete.
// @verifies reachability:INV-10, reachability:INV-11
func TestSliceThroughAnAmbiguousHTTPJoin(t *testing.T) {
	g := httpSlice(t, map[string]string{
		"api/a.ts": "function listA() { return supabase.from('orders').select(); }\napp.get('/api/orders', listA);\n",
		"api/b.ts": "function listB() {}\nrouter.get('/api/orders', listB);\n",
	})
	rec := Compute(g, "orders:INV-1", enf("sql.policy.public.orders.reads"))
	if rec.Reading != ReadingUnknownFromHop || rec.Hop != 1 {
		t.Fatalf("reading %q hop %d: %s", rec.Reading, rec.Hop, rec.Why)
	}
	h := rec.Paths[0].Hops[0]
	if h.Kind != "http" || h.Truth != TruthUnknown || h.To != "api.a.listA" {
		t.Errorf("hop = %+v", h)
	}
}
