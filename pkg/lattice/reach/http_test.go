package reach_test

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A route the server registers, served at GET /api/orders and
// GET /api/orders/:id.
const ordersServer = `import express from 'express';
const app = express();
function listOrders(req, res) {}
function getOrder(req, res) {}
app.get('/api/orders', listOrders);
app.get('/api/orders/:id', getOrder);
`

func httpGraph(t *testing.T, files map[string]string, eps ...schema.EntryPoint) *reach.Graph {
	t.Helper()
	return reach.Build(parse(t, typescript.New(), files), reach.Options{EntryPoints: eps})
}

// httpRefs returns "from name why" for each unresolved http reference.
func httpUnresolved(g *reach.Graph) []string {
	var out []string
	for _, u := range g.Unresolved {
		if u.Kind == "http" {
			out = append(out, u.From+" ["+u.Name+"] "+u.Why)
		}
	}
	return out
}

// A literal request whose key equals the key of a detected route is an http
// edge to the route's handler, and a literal client segment matches a
// parameter segment.
// @verifies reachability:INV-11
func TestHTTPEdgeFromALiteralCall(t *testing.T) {
	g := httpGraph(t, map[string]string{
		"api/server.ts": ordersServer,
		"web/orders.ts": `export function list() { return fetch('/api/orders'); }
export function one() { return fetch('/api/orders/42', { method: 'GET' }); }
export function trailing() { return fetch('/api/orders/?page=2#top'); }
`,
	})
	got := edges(g, "http")
	want := []string{
		"web.orders.list -> api.server.listOrders",
		"web.orders.one -> api.server.getOrder",
		"web.orders.trailing -> api.server.listOrders",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("http edges:%s\nwant:%s", joined(got), joined(want))
	}
	if h := httpUnresolved(g); len(h) != 0 {
		t.Errorf("nothing should stay unresolved:%s", joined(h))
	}
}

// A computed URL never makes an edge: it is unresolved (dynamic) and one
// HTTP_CALL_DYNAMIC per file counts it. A literal that no route serves is
// HTTP_CALL_UNSERVED; a literal to another origin is a call to another
// service, which raises nothing.
// @verifies reachability:INV-11
func TestHTTPDynamicAndUnserved(t *testing.T) {
	mods := parse(t, typescript.New(), map[string]string{
		"api/server.ts": ordersServer,
		"web/calls.ts": "const base = '';\n" +
			"export function a() { return fetch(`${base}/api/orders`); }\n" +
			"export function b(url: string) { return fetch(url); }\n" +
			"export function c() { return fetch(base + '/api/orders'); }\n" +
			"export function d() { return fetch('/api/orders', { method: pick() }); }\n" +
			"export function e() { return fetch('api/orders'); }\n" + // relative to the page: no key
			"export function f() { return fetch('/api/nothing'); }\n" +
			"export function g() { return fetch('/api/orders', { method: 'POST' }); }\n" +
			"export function h() { return fetch('https://pay.example.com/v1/charge'); }\n",
		"web/more.ts": "export function z(u: string) { return fetch(u); }\n",
	})
	g := reach.Build(mods, reach.Options{})
	if got := edges(g, "http"); len(got) != 0 {
		t.Errorf("no call here is both literal and served:%s", joined(got))
	}
	byFrom := map[string]string{}
	for _, u := range g.Unresolved {
		if u.Kind == "http" {
			byFrom[u.From] = u.Why
		}
	}
	for from, why := range map[string]string{
		"web.calls.a": "dynamic", "web.calls.b": "dynamic", "web.calls.c": "dynamic", "web.calls.d": "dynamic",
		"web.calls.e": "dynamic", "web.calls.f": "no_route", "web.calls.g": "no_route", "web.calls.h": "", // h is external: no reference at all
		"web.more.z": "dynamic",
	} {
		if byFrom[from] != why {
			t.Errorf("%s: %q, want %q", from, byFrom[from], why)
		}
	}
	if g.Stats.NoRoute != 2 {
		t.Errorf("NoRoute = %d, want 2", g.Stats.NoRoute)
	}
	// An http call left out of the RPC finding.
	r := reach.Classify(g, nil)
	for _, v := range reach.Findings(schema.KnowledgeGraph{}, r, nil) {
		if v.Code == schema.CodeRPCUnresolved {
			t.Errorf("a computed request URL is not an RPC: %+v", v)
		}
	}
	var unserved, dynamic []schema.Violation
	for _, v := range reach.HTTPFindings(r) {
		if v.Severity != schema.SeverityInfo {
			t.Errorf("%s is %s, want info", v.Code, v.Severity)
		}
		switch v.Code {
		case schema.CodeHTTPCallUnserved:
			unserved = append(unserved, v)
		case schema.CodeHTTPCallDynamic:
			dynamic = append(dynamic, v)
		}
	}
	if len(unserved) != 2 { // f and g; h is an absolute URL on another origin, so external
		t.Errorf("HTTP_CALL_UNSERVED: %d, want 2: %+v", len(unserved), unserved)
	}
	if len(unserved) > 0 && !strings.Contains(unserved[0].Message, "GET /api/nothing") {
		t.Errorf("the finding names the key: %q", unserved[0].Message)
	}
	if len(dynamic) != 2 { // one per file
		t.Fatalf("HTTP_CALL_DYNAMIC: %d, want 2 (one per file): %+v", len(dynamic), dynamic)
	}
	for _, v := range dynamic {
		if v.Subject == "web/calls.ts" && !strings.Contains(v.Message, "5 request(s)") {
			t.Errorf("the count and lines: %q", v.Message)
		}
		if v.Subject == "web/calls.ts" && !strings.Contains(v.Message, "and 2 more") {
			t.Errorf("only the first lines are listed: %q", v.Message)
		}
	}
}

// Test code that fetches raises no finding.
func TestHTTPFindingsSkipTests(t *testing.T) {
	g := httpGraph(t, map[string]string{
		"web/calls.test.ts": "import { it } from 'vitest';\nit('x', () => fetch('/api/nothing'));\nit('y', (u) => fetch(u));\n",
	})
	if got := reach.HTTPFindings(reach.Classify(g, nil)); len(got) != 0 {
		t.Errorf("a test's request raises nothing: %+v", got)
	}
}

// Two routes with the key a call names make the join ambiguous: no edge, the
// reference recorded with both handlers as candidates, never a guess.
// @verifies reachability:INV-11
func TestHTTPAmbiguousJoin(t *testing.T) {
	g := httpGraph(t, map[string]string{
		"api/a.ts":    "function listA() {}\napp.get('/api/orders', listA);\n",
		"api/b.ts":    "function listB() {}\nrouter.get('/api/orders', listB);\n",
		"web/call.ts": "export function load() { return fetch('/api/orders'); }\n",
	})
	if got := edges(g, "http"); len(got) != 0 {
		t.Errorf("an ambiguous join makes no edge:%s", joined(got))
	}
	var found bool
	for _, u := range g.Unresolved {
		if u.Kind == "http" && u.Why == "ambiguous" && strings.Join(u.Candidates, ",") == "api.a.listA,api.b.listB" {
			found = true
		}
	}
	if !found || g.Stats.Ambiguous != 1 {
		t.Errorf("want one ambiguous http reference with both handlers:%s", joined(httpUnresolved(g)))
	}
}

// Parameter forms match each other (:id, {id}, [id]); two literal segments
// must be equal; segment counts agree; the method must match, ANY matches
// every method; a catch-all route is never matched.
// @verifies reachability:INV-11
func TestHTTPPathNormalisation(t *testing.T) {
	route := func(path, handler string) schema.EntryPoint {
		return schema.EntryPoint{ID: "ep." + handler, Kind: schema.EntryPointKindHTTP,
			Trigger: schema.Trigger{Method: "get", Path: path}, Handler: schema.Handler{Symbol: "api.h." + handler}}
	}
	files := map[string]string{
		"api/h.ts": "export function colon() {}\nexport function brace() {}\nexport function bracket() {}\nexport function any() {}\nexport function rest() {}\n",
		"web/c.ts": "export function p1() { return fetch('/orders/42'); }\n" +
			"export function p2() { return fetch('/shops/9/orders/42'); }\n" +
			"export function p3() { return fetch('/carts/7'); }\n" +
			"export function p4() { return fetch('/carts/7/lines'); }\n" + // longer than the route
			"export function p5() { return fetch('/files/a/b'); }\n" + // under a catch-all
			"export function p6() { return fetch('/orders/{id}'); }\n" + // a parameter on the call side
			"export function p7() { return fetch('/shops/9/orders'); }\n", // shorter than the route
	}
	eps := []schema.EntryPoint{
		route("/orders/:id", "colon"), route("/shops/{shop}/orders/{id}", "brace"),
		route("/carts/[id]", "bracket"), route("/files/[...path]", "rest"),
	}
	g := httpGraph(t, files, eps...)
	got := strings.Join(edges(g, "http"), "|")
	want := strings.Join([]string{
		"web.c.p1 -> api.h.colon", "web.c.p2 -> api.h.brace", "web.c.p3 -> api.h.bracket", "web.c.p6 -> api.h.colon",
	}, "|")
	if got != want {
		t.Errorf("http edges:\n got %s\nwant %s", got, want)
	}
}

// An edge function is served at /functions/v1/<name> for any method, and
// functions.invoke names it; axios calls and a config call key the same way.
// @verifies reachability:INV-11
func TestHTTPInvokeAndAxios(t *testing.T) {
	g := httpGraph(t, map[string]string{
		"supabase/functions/charge/index.ts":  "import { serve } from 'std';\nserve(async (req) => new Response('ok'));\n",
		"supabase/functions/_shared/index.ts": "import { serve } from 'std';\nserve(async () => new Response('x'));\n",
		"web/pay.ts": "export function pay() { return supabase.functions.invoke('charge', { body: {} }); }\n" +
			"export function get() { return supabase.functions.invoke('charge', { method: 'GET' }); }\n" +
			"export function nope() { return supabase.functions.invoke('refund'); }\n" +
			"export function dyn(n: string) { return supabase.functions.invoke(n); }\n" +
			"export function ax() { return axios.post('/functions/v1/charge', {}); }\n" +
			"export function cfg() { return axios({ url: '/functions/v1/charge', method: 'put' }); }\n" +
			"export function cfgDyn(m: string) { return axios({ url: '/functions/v1/charge', method: m }); }\n" +
			"export function win() { return window.fetch('/functions/v1/charge', { method: 'POST' }); }\n",
	})
	serve := "supabase.functions.charge.index.<serve>"
	got := strings.Join(edges(g, "http"), "|")
	want := strings.Join([]string{
		"web.pay.ax -> " + serve, "web.pay.cfg -> " + serve, "web.pay.get -> " + serve,
		"web.pay.pay -> " + serve, "web.pay.win -> " + serve,
	}, "|")
	if got != want {
		t.Errorf("http edges:\n got %s\nwant %s", got, want)
	}
	why := map[string]string{}
	for _, u := range g.Unresolved {
		if u.Kind == "http" {
			why[u.From] = u.Why
		}
	}
	if why["web.pay.nope"] != "no_route" || why["web.pay.dyn"] != "dynamic" || why["web.pay.cfgDyn"] != "dynamic" {
		t.Errorf("unresolved: %v", why)
	}
}

// The reach cache and rpc finding are unaffected by http refs: a call to a
// function's own route is only an edge.
func TestHTTPRefKeepsNoSelfEdge(t *testing.T) {
	g := httpGraph(t, map[string]string{
		"api/server.ts": "function listOrders() { return fetch('/api/orders'); }\napp.get('/api/orders', listOrders);\n",
	})
	if got := edges(g, "http"); len(got) != 0 {
		t.Errorf("a handler that calls its own route adds no self edge:%s", joined(got))
	}
}

// An absolute URL joins nothing unless its origin is declared the workspace's
// own: a route of the same path is not evidence that another origin is ours.
// The call makes no edge, is not unresolved and raises no finding; with the
// origin declared (http.origins) it is a path like any other.
// @verifies reachability:INV-11
func TestHTTPAbsoluteURLNeedsADeclaredOrigin(t *testing.T) {
	files := map[string]string{
		"api/server.ts": ordersServer,
		"web/calls.ts": "export function other() { return fetch('https://api.example.com/api/orders'); }\n" +
			"export function mine() { return fetch('https://app.example.com/api/orders/'); }\n" +
			"export function gone() { return fetch('https://app.example.com/api/nothing'); }\n",
	}
	mods := parse(t, typescript.New(), files)

	g := reach.Build(mods, reach.Options{})
	if got := edges(g, "http"); len(got) != 0 {
		t.Errorf("no origin is declared, so no absolute URL joins:%s", joined(got))
	}
	if h := httpUnresolved(g); len(h) != 0 {
		t.Errorf("an external call is no reference left unresolved:%s", joined(h))
	}
	if got := reach.HTTPFindings(reach.Classify(g, nil)); len(got) != 0 {
		t.Errorf("an external call raises no finding: %+v", got)
	}

	g = reach.Build(mods, reach.Options{Origins: []string{"https://APP.example.com", "not an origin"}})
	if got := strings.Join(edges(g, "http"), "|"); got != "web.calls.mine -> api.server.listOrders" {
		t.Errorf("the declared origin joins as a path; the other stays external: %q", got)
	}
	if h := strings.Join(httpUnresolved(g), "|"); h != "web.calls.gone [GET /api/nothing] no_route" {
		t.Errorf("unresolved: %q", h)
	}
	if got := reach.HTTPFindings(reach.Classify(g, nil)); len(got) != 1 || got[0].Code != schema.CodeHTTPCallUnserved {
		t.Errorf("a declared origin's unserved call is HTTP_CALL_UNSERVED: %+v", got)
	}
}

// A request built from a declared env or setting origin and a literal path
// joins the edge function that path names, from TypeScript and from a cron
// job's SQL body alike; an undeclared origin stays dynamic.
// @verifies reachability:INV-11
func TestDeclaredOriginsJoinEdgeFunctions(t *testing.T) {
	files := map[string]string{
		"supabase/functions/add/index.ts": "export function handler(req: Request) { return new Response('ok'); }\nDeno.serve(handler);\n",
	}
	mods := parse(t, typescript.New(), files)
	ts := typescript.New()
	ts.SetHTTPOrigins([]string{"env:VITE_API"})
	mods = append(mods, parse(t, ts, map[string]string{
		"web/add.ts": "export function add() { return fetch(`${import.meta.env.VITE_API}/functions/v1/add`, { method: 'POST' }); }\n" +
			"export function other() { return fetch(`${import.meta.env.UNDECLARED}/functions/v1/add`); }\n",
	})...)
	pg := postgres.New(nil, nil)
	pg.SetHTTPOrigins([]string{"setting:app.api_url"})
	mods = append(mods, parse(t, pg, map[string]string{
		"db/migrations/001_cron.sql": "select cron.schedule('add-nightly', '0 3 * * *', $$\n" +
			"  select net.http_post(url := current_setting('app.api_url') || '/functions/v1/add');\n$$);\n" +
			"select cron.schedule('other-nightly', '0 4 * * *', $$ select net.http_post(url := current_setting('app.hidden') || '/functions/v1/add') $$);\n",
	})...)
	g := reach.Build(mods, reach.Options{})
	got := edges(g, "http")
	want := []string{
		"sql.cron.add_nightly -> supabase.functions.add.index.<serve>",
		"web.add.add -> supabase.functions.add.index.<serve>",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("http edges:%s\nwant:%s", joined(got), joined(want))
	}
	if h := httpUnresolved(g); len(h) != 2 {
		t.Errorf("the two undeclared origins stay dynamic:%s", joined(h))
	}
}

// edgeFunctionLayout is an edge function the way a real project lays it out:
// index.ts serves a handler built in handler.ts, and a client invokes it by
// name.
func edgeFunctionLayout(name string) map[string]string {
	return map[string]string{
		"supabase/functions/" + name + "/index.ts":   "import { createHandler } from './handler.ts';\nDeno.serve(createHandler({}));\n",
		"supabase/functions/" + name + "/handler.ts": "export function createHandler(deps: unknown) { return async (req: Request) => new Response('ok'); }\n",
	}
}

func declaredRoute(name string) schema.EntryPoint {
	return schema.EntryPoint{ID: "ep." + name, Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
		Trigger: schema.Trigger{Method: "POST", Path: "/functions/v1/" + name},
		Handler: schema.Handler{Symbol: "supabase.functions." + name + ".handler.createHandler"}}
}

// An edge function is one endpoint: the declared route whose handler the
// serve call builds and the serve node itself are a single end, so a client
// call resolves to it instead of staying ambiguous.
// @verifies reachability:INV-11
func TestHTTPEdgeFunctionWithDeclaredRouteIsOneEnd(t *testing.T) {
	files := edgeFunctionLayout("x")
	files["web/call.ts"] = "export function run() { return supabase.functions.invoke('x'); }\n"
	g := httpGraph(t, files, declaredRoute("x"))
	got := edges(g, "http")
	if len(got) != 1 {
		t.Errorf("want one resolved http edge, got:%s", joined(got))
	}
	if h := httpUnresolved(g); len(h) != 0 {
		t.Errorf("nothing should stay unresolved:%s", joined(h))
	}
}

// Two different handlers on one key stay ambiguous: a route another module
// registers at the function's path, and two declared routes that name
// different handlers for it, are not the function's own end.
// @verifies reachability:INV-11
func TestHTTPDistinctHandlersOnOnePathStayAmbiguous(t *testing.T) {
	ambiguous := func(g *reach.Graph) bool {
		for _, u := range g.Unresolved {
			if u.Kind == "http" && u.Why == "ambiguous" {
				return true
			}
		}
		return false
	}
	call := "export function run() { return supabase.functions.invoke('x'); }\n"

	files := edgeFunctionLayout("x")
	files["web/call.ts"] = call
	files["api/other.ts"] = "function other() {}\napp.post('/functions/v1/x', other);\n"
	if g := httpGraph(t, files, declaredRoute("x")); !ambiguous(g) {
		t.Errorf("a route in another module on the same path keeps the join ambiguous:%s", joined(edges(g, "http")))
	}

	files = edgeFunctionLayout("x")
	files["web/call.ts"] = call
	files["other/elsewhere.ts"] = "export function elsewhere() { return 1; }\n"
	second := declaredRoute("x")
	second.ID = "ep.x.second"
	second.Handler.Symbol = "other.elsewhere.elsewhere"
	if g := httpGraph(t, files, declaredRoute("x"), second); !ambiguous(g) {
		t.Errorf("two declared handlers on one key stay ambiguous:%s", joined(edges(g, "http")))
	}
}

const vaultFunction = "create function public.call_x() returns void language plpgsql as $$\ndeclare v_url text;\nbegin\n" +
	"  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'x_url';\n" +
	"  perform net.http_post(url := v_url);\nend $$;\n"

// A function that reads an edge function's URL whole from a declared Vault
// secret joins the function: one resolved http edge to its serve node. The
// same read with no declaration stays dynamic, and the finding names the
// secret to declare.
// @verifies reachability:INV-11
func TestHTTPVaultSecretJoinsEdgeFunction(t *testing.T) {
	build := func(secrets map[string]string) *reach.Graph {
		mods := parse(t, typescript.New(), edgeFunctionLayout("x"))
		pg := postgres.New(nil, nil)
		pg.SetHTTPSecrets(secrets)
		mods = append(mods, parse(t, pg, map[string]string{"db/migrations/001_fn.sql": vaultFunction})...)
		return reach.Build(mods, reach.Options{})
	}
	g := build(map[string]string{"x_url": "/functions/v1/x"})
	want := []string{"sql.public.call_x -> supabase.functions.x.index.<serve>"}
	if got := edges(g, "http"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("http edges:%s\nwant:%s", joined(got), joined(want))
	}

	g = build(nil)
	if got := edges(g, "http"); len(got) != 0 {
		t.Errorf("an undeclared secret makes no edge:%s", joined(got))
	}
	var msg, detail string
	for _, v := range reach.HTTPFindings(reach.Classify(g, nil)) {
		if v.Code == schema.CodeHTTPCallDynamic {
			msg, detail = v.Message, v.NextAction.Detail
		}
	}
	if !strings.Contains(msg, "x_url") || !strings.Contains(detail, "http.secrets") {
		t.Errorf("the finding must name the secret to declare:\n  %s\n  %s", msg, detail)
	}
}
