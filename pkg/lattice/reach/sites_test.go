package reach_test

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/reach"
)

// An edge keeps where the literals that made it are: one site per call, so a
// function that sends the same request twice has both, in file order. An edge
// made by a computed key has none.
// @verifies reachability:INV-11
func TestEdgeSitesRecordTheLiterals(t *testing.T) {
	web := "export function load() {\n  fetch('/api/orders');\n  return fetch(\"/api/orders\");\n}\n" +
		"export function one() { return fetch('/api/orders/7'); }\n"
	g := httpGraph(t, map[string]string{"api/server.ts": ordersServer, "web/c.ts": web})
	find := func(from, to string) reach.Edge {
		t.Helper()
		for _, e := range g.Edges {
			if e.From == from && e.To == to && e.Kind == "http" {
				return e
			}
		}
		t.Fatalf("no http edge %s -> %s in %v", from, to, edges(g, "http"))
		return reach.Edge{}
	}
	e := find("web.c.load", "api.server.listOrders")
	if len(e.Sites) != 2 {
		t.Fatalf("sites = %+v, want both calls", e.Sites)
	}
	for i, want := range []string{"'/api/orders'", `"/api/orders"`} {
		s := e.Sites[i]
		if got := web[s.Start:s.End]; got != want || s.File != "web/c.ts" || s.Key != "GET /api/orders" {
			t.Errorf("site %d = %+v reads %q, want %q", i, s, got, want)
		}
	}
	if e := find("web.c.one", "api.server.getOrder"); len(e.Sites) != 1 || web[e.Sites[0].Start:e.Sites[0].End] != "'/api/orders/7'" {
		t.Errorf("one = %+v", e.Sites)
	}
	// A parameter route absorbs a literal last segment; a route the request
	// does not match is not served.
	if !g.HTTPServes("GET /api/orders/42-lattice-break") || g.HTTPServes("GET /api/orders-lattice-break/42") {
		t.Error("HTTPServes does not follow the route join")
	}
}

// An rpc edge has the site of the name; a publish edge written in SQL has
// none, because the postgres adapter does not record where its literal is, so
// it cannot be altered at its out end.
// @verifies reachability:INV-11
func TestRPCEdgeSiteAndSQLPublishHasNone(t *testing.T) {
	g, _ := channelGraph(t, emailsSQL, map[string]string{"api/server.ts": emailsAPI})
	for _, e := range g.Edges {
		switch {
		case e.Kind == "rpc" && e.To == "sql.public.enqueue_email":
			if len(e.Sites) != 1 || emailsAPI[e.Sites[0].Start:e.Sites[0].End] != "'enqueue_email'" || e.Sites[0].Key != "enqueue_email" {
				t.Errorf("rpc edge sites = %+v", e.Sites)
			}
		case e.Kind == "publish":
			if len(e.Sites) != 0 {
				t.Errorf("a publish edge from SQL has sites: %+v", e)
			}
		}
	}
}
