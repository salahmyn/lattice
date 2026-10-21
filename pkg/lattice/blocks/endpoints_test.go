package blocks

import (
	"testing"
)

func endpointInputs() Inputs {
	routes := &Catalogue{Area: "routes", Path: "a-routes.json", Nodes: []CatNode{
		{ID: "routes", Kind: "service", Title: "Routes"},
		{ID: "routes.post", Parent: "routes", Kind: "route", Title: "Create", HTTP: &CatHTTP{Method: "POST", Path: "/checkout/{cart}/orders"}, Detail: "Create an order."},
		{ID: "routes.cities", Parent: "routes", Kind: "route", Title: "Cities", HTTP: &CatHTTP{Method: "GET", Path: "/api/v1/shipping/cities"}},
	}}
	orders := &Catalogue{Area: "orders", Path: "b-orders.json", Nodes: []CatNode{
		{ID: "orders", Kind: "service", Title: "Orders"},
		{ID: "orders.save", Parent: "routes.post", Kind: "action", Title: "Save"},
		{ID: "orders.gate", Parent: "routes.post", Kind: "gate", Title: "Validate"},
		{ID: "orders.save.row", Parent: "orders.save", Kind: "model", Title: "Row"},
		{ID: "orders.cfg", Parent: "orders", Kind: "config", Title: "Setting"},
		{ID: "orders.job", Parent: "orders", Kind: "job", Title: "Job"},
		{ID: "orders.city", Parent: "routes.cities", Kind: "query", Title: "City lookup"},
	}}
	return Inputs{Sources: []SourceInput{{
		Info: SourceInfo{ID: "api", Prefix: "be", Endpoints: &EndpointGrouping{
			SkipAreas: []string{"routes"}, SkipSegments: []string{"checkout", "api", "v1"},
			PathPrefixes: map[string]string{"/api/": "api-"}}},
		Catalogues: []*Catalogue{routes, orders},
	}}}
}

func TestEndpointGrouping(t *testing.T) {
	r := Build(endpointInputs())
	b := blockMap(r)
	want := map[string]string{ // block -> parent
		"grp:orders:config":             "be:orders",
		"grp:orders:shared":             "be:orders",
		"grp:orders:topic:orders":       "be:orders",
		"grp:orders:topic:api-shipping": "be:orders",
		"grp:orders:ep:routes.post":     "grp:orders:topic:orders",
		"grp:orders:ep:routes.cities":   "grp:orders:topic:api-shipping",
		"be:orders.cfg":                 "grp:orders:config",
		"be:orders.job":                 "grp:orders:shared",
		"be:orders.save":                "grp:orders:ep:routes.post",
		"be:orders.gate":                "grp:orders:ep:routes.post",
		"be:orders.city":                "grp:orders:ep:routes.cities",
		"be:orders.save.row":            "be:orders.save",
		"be:routes.post":                "be:routes",
	}
	for id, parent := range want {
		if got, ok := b[id]; !ok || got.Parent != parent {
			t.Errorf("%s parent = %q (present %v) want %q", id, got.Parent, ok, parent)
		}
	}
	if ep := b["grp:orders:ep:routes.post"]; ep.Kind != "endpoint" || ep.Title != "POST /checkout/{cart}/orders" || ep.Attrs["route_id"] != "routes.post" {
		t.Errorf("endpoint block = %+v", ep)
	}
	if tp := b["grp:orders:topic:api-shipping"]; tp.Kind != "topic" || tp.Title != "Api shipping" {
		t.Errorf("topic block = %+v", tp)
	}
	if l := edgeSet(r)["be:orders.gate -flow-> be:orders.save"]; len(l) != 1 || l[0] != "then" {
		t.Errorf("then edge (gate before handler) = %v; all edges %v", l, edgeSet(r))
	}
	if _, ok := b["grp:routes:shared"]; ok {
		t.Error("a skipped area must not be grouped")
	}
}

func TestEndpointGroupingSkipsAreaWithOneGroup(t *testing.T) {
	in := endpointInputs()
	cat := in.Sources[0].Catalogues[1]
	var keep []CatNode
	for _, n := range cat.Nodes { // leave only blocks reached from routes.post
		if n.ID == "orders" || n.Parent == "routes.post" || n.Parent == "orders.save" {
			keep = append(keep, n)
		}
	}
	cat.Nodes = keep
	b := blockMap(Build(in))
	if _, ok := b["grp:orders:ep:routes.post"]; ok {
		t.Error("an area with a single endpoint group and no config group stays flat")
	}
	if b["be:orders.save"].Parent != "be:orders" {
		t.Errorf("save parent = %q", b["be:orders.save"].Parent)
	}
}
