package breaktest

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// siteOf finds the quoted literal lit in src as an edge site with the key.
func siteOf(t *testing.T, src, lit, key string) EdgeSite {
	t.Helper()
	i := strings.Index(src, lit)
	if i < 0 {
		t.Fatalf("%q not in %q", lit, src)
	}
	return EdgeSite{File: "web/a.ts", Line: 1, Start: i, End: i + len(lit), Key: key}
}

func alter(t *testing.T, kind, src, lit, key string, served func(string) bool) (string, error) {
	t.Helper()
	brk, err := alterSite(kind, siteOf(t, src, lit, key), []byte(src), served)
	if err != nil {
		return "", err
	}
	return string(brk.Apply([]byte(src))), nil
}

// An http break appends the suffix to the last path segment, keeping the
// method, the origin and the query.
// @verifies breaktest:INV-19
func TestAlterSiteHTTP(t *testing.T) {
	for _, c := range []struct{ name, src, lit, key, want string }{
		{"path", `fetch('/api/orders')`, `'/api/orders'`, "GET /api/orders", `fetch('/api/orders-lattice-break')`},
		{"double quotes", `fetch("/api/orders")`, `"/api/orders"`, "GET /api/orders", `fetch("/api/orders-lattice-break")`},
		{"template", "fetch(`/api/orders`)", "`/api/orders`", "GET /api/orders", "fetch(`/api/orders-lattice-break`)"},
		{"query kept", `fetch('/api/orders?page=2')`, `'/api/orders?page=2'`, "GET /api/orders", `fetch('/api/orders-lattice-break?page=2')`},
		{"trailing slash", `fetch('/api/orders/')`, `'/api/orders/'`, "GET /api/orders", `fetch('/api/orders-lattice-break/')`},
		{"origin kept", `fetch('https://api.example.com/v1/orders')`, `'https://api.example.com/v1/orders'`, "GET /v1/orders", `fetch('https://api.example.com/v1/orders-lattice-break')`},
		{"method kept", `axios.post('/api/orders')`, `'/api/orders'`, "POST /api/orders", `axios.post('/api/orders-lattice-break')`},
		{"invoke", `supabase.functions.invoke('send-mail')`, `'send-mail'`, "POST /functions/v1/send-mail", `supabase.functions.invoke('send-mail-lattice-break')`},
	} {
		got, err := alter(t, ir.RefHTTP, c.src, c.lit, c.key, nil)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// A path whose last segment a route parameter absorbs is altered earlier in
// the path, where no route still serves it; when every alteration is still
// served, nothing is broken.
// @verifies breaktest:INV-19
func TestAlterSiteHTTPParameterRoute(t *testing.T) {
	src, lit, key := "fetch('/api/orders/42')", "'/api/orders/42'", "GET /api/orders/42"
	served := func(k string) bool { return k == "GET /api/orders/42-lattice-break" } // /api/orders/:id
	got, err := alter(t, ir.RefHTTP, src, lit, key, served)
	if want := "fetch('/api/orders-lattice-break/42')"; err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
	if _, err := alter(t, ir.RefHTTP, src, lit, key, func(string) bool { return true }); err == nil || !strings.Contains(err.Error(), "still served") {
		t.Errorf("every altered path served: err = %v, want an error", err)
	}
}

// The other kinds append the suffix to the name.
// @verifies breaktest:INV-19
func TestAlterSiteNames(t *testing.T) {
	for _, c := range []struct{ kind, src, lit, key, want string }{
		{ir.RefRPC, `db.rpc('refund_order')`, `'refund_order'`, "refund_order", `db.rpc('refund_order-lattice-break')`},
		{ir.RefTable, `db.from('orders')`, `'orders'`, "orders", `db.from('orders-lattice-break')`},
		{ir.RefPublish, `queue.add('emails')`, `'emails'`, "queue:emails", `queue.add('emails-lattice-break')`},
	} {
		got, err := alter(t, c.kind, c.src, c.lit, c.key, nil)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.kind, got, err, c.want)
		}
	}
}

// A span that is not exactly the literal the key was read from, a literal
// with a substitution or an escape, and a kind with no literal out end are
// never altered: the run reads unknown, it does not guess.
// @verifies breaktest:INV-19
func TestAlterSiteRefusesWhatItCannotAlterExactly(t *testing.T) {
	for _, c := range []struct{ name, kind, src, lit, key, why string }{
		{"computed", ir.RefHTTP, "fetch(`${base}/orders`)", "`${base}/orders`", "GET /orders", "substitution"},
		{"escape", ir.RefHTTP, `fetch('/api/it\'s')`, `'/api/it\'s'`, "GET /api/it's", "escape"},
		{"another literal", ir.RefHTTP, `fetch('/api/refunds')`, `'/api/refunds'`, "GET /api/orders", "not the request"},
		{"not a literal", ir.RefHTTP, `fetch(base)`, `base`, "GET /api/orders", "not a string literal"},
		{"stale rpc", ir.RefRPC, `db.rpc('other')`, `'other'`, "refund_order", "not the name"},
		{"stale channel", ir.RefPublish, `queue.add('sms')`, `'sms'`, "queue:emails", "not the channel"},
		{"root path", ir.RefHTTP, `fetch('/')`, `'/'`, "GET /", "no segment"},
		{"call", ir.RefCall, `f('x')`, `'x'`, "x", "no literal out end"},
	} {
		if _, err := alter(t, c.kind, c.src, c.lit, c.key, nil); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: err = %v, want one containing %q", c.name, err, c.why)
		}
	}
	// A span past the end of the file (the file changed since the graph).
	if _, err := alterSite(ir.RefRPC, EdgeSite{File: "a.ts", Start: 10, End: 20, Key: "x"}, []byte("db.rpc('x')"), nil); err == nil {
		t.Error("a span outside the file was altered")
	}
}

// Hops are broken once by the position of their literal, up to the cap; an
// unknown hop and a hop with no literal at a known position are listed as
// skipped, and a kind with no literal out end is not a candidate at all.
// @verifies breaktest:INV-19
func TestEdgeTargets(t *testing.T) {
	site := func(file string, at int) []EdgeSite {
		return []EdgeSite{{File: file, Start: at, End: at + 5, Key: "GET /a"}}
	}
	hop := func(from, to, kind string, sites []EdgeSite, skip string) EdgeHop {
		return EdgeHop{Hop: Hop{From: from, To: to, Kind: kind}, Sites: sites, Skip: skip}
	}
	try, skipped, over := edgeTargets([]EdgeHop{
		hop("a", "r1", "http", site("w.ts", 10), ""),
		hop("a", "r1", "http", site("w.ts", 10), ""), // the same hop again
		hop("a", "r2", "http", site("w.ts", 10), ""), // another hop through the same call
		hop("b", "t", "table", site("w.ts", 40), ""),
		hop("c", "u", "http", nil, ""),                // no position recorded
		hop("d", "v", "rpc", site("w.ts", 70), "unk"), // an unknown hop
		hop("e", "f", "call", nil, ""),                // not keyed by a literal
	})
	if len(try) != 2 || try[0].To != "r1" || try[1].To != "t" || over != 0 {
		t.Errorf("try = %+v over %d", try, over)
	}
	if len(skipped) != 2 || !strings.Contains(skipped[0].Skip, "not a literal") || skipped[1].Skip != "unk" {
		t.Errorf("skipped = %+v", skipped)
	}

	var many []EdgeHop
	for i := 0; i < MaxEdgesPerPromise+3; i++ {
		many = append(many, hop("x", "y"+string(rune('a'+i)), "http", site("w.ts", 10*(i+1)), ""))
	}
	if try, _, over := edgeTargets(many); len(try) != MaxEdgesPerPromise || over != 3 {
		t.Errorf("cap: %d tried, %d over", len(try), over)
	}
}

// An edge run reads its cache key from the hop and the out-end file, so a
// different hop through the same file is a different run.
// @verifies breaktest:INV-16
func TestEdgeCacheKeyIncludesHop(t *testing.T) {
	a := Run{EnforcerHash: "h", Method: MethodEdge, Hop: &Hop{From: "f", To: "r1", Kind: "http"}}
	b := a
	b.Hop = &Hop{From: "f", To: "r2", Kind: "http"}
	c := a
	c.EnforcerHash = "other file"
	if a.cacheKey() == b.cacheKey() || a.cacheKey() == c.cacheKey() {
		t.Errorf("the cache key ignores the hop or the out-end file: %v %v %v", a.cacheKey(), b.cacheKey(), c.cacheKey())
	}
}
