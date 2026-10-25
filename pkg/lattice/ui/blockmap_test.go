package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// @verifies brd.adoption:US-1
func TestBlockMapServesTheStore(t *testing.T) {
	dir := t.TempDir()
	ws := &workspace.Workspace{LatticeDir: dir}
	st := store.OpenFiles(ws)
	g := store.Graph{Blocks: []schema.Block{
		{ID: "web:cart", Kind: "screen", Title: "Cart </script>", Truth: "declared"},
		{ID: "api:items", Kind: "route", Title: "POST /items", Truth: "wired"}},
		Edges: []schema.BlockEdge{{From: "web:cart", To: "api:items", Kind: "api"}}}
	if err := st.ReplaceGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	srv := New(ws, Options{Store: st})
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if w := get("/map"); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Block map</title>") || strings.Contains(w.Body.String(), "/*__GRAPH__*/") {
		t.Errorf("/map = %d", w.Code)
	}
	var got store.Graph
	if w := get("/api/graph"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Blocks) != 2 {
		t.Errorf("/api/graph = %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/search?q=cart"); w.Code != 200 || !strings.Contains(w.Body.String(), "web:cart") {
		t.Errorf("/api/search = %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/relations/web:cart?dir=out"); w.Code != 200 || !strings.Contains(w.Body.String(), "api:items") {
		t.Errorf("/api/relations = %d %s", w.Code, w.Body.String())
	}
	page, err := MapHTML(g, "")
	if err != nil || strings.Contains(string(page), "Cart </script>") || !strings.Contains(string(page), `Cart \u003c/script\u003e`) {
		t.Errorf("export does not escape the inlined graph")
	}
	if page, _ := MapHTML(g, "Shop <map>"); !strings.Contains(string(page), "<title>Shop &lt;map&gt;</title>") || !strings.Contains(string(page), "<h1>Shop &lt;map&gt; ") {
		t.Errorf("title not applied")
	}
	if w := httptest.NewRecorder(); true {
		New(ws, Options{}).routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("no store: %d", w.Code)
		}
	}
}

// The feature, BRD and entry point pages link to the map exactly when the
// stored block graph has a block that stands for them.
func TestViewOnMapLinks(t *testing.T) {
	ws := &workspace.Workspace{LatticeDir: t.TempDir()}
	st := store.OpenFiles(ws)
	if err := st.ReplaceGraph(context.Background(), store.Graph{Blocks: []schema.Block{
		{ID: "feat:refund", Kind: "composite", Title: "refund", BRD: "brd.refunds", Truth: "declared"},
		{ID: "feat:audit", Kind: "composite", Title: "audit", BRD: "brd.refunds", Truth: "declared"},
		{ID: "ep:post-refund", Kind: "boundary-in", Title: "POST /refunds", Truth: "wired"},
		{ID: "web:cart", Kind: "screen", Title: "Cart", Truth: "declared"},
	}}); err != nil {
		t.Fatal(err)
	}
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{ID: "refund"}, {ID: "orders"}, {ID: "audit"}},
		BRDs: []schema.BRD{{ID: "brd.refunds", Title: "Refunds", Status: schema.BRDDraft, ImplementsVia: []string{"refund"}},
			{ID: "brd.cart", Title: "Cart", Status: schema.BRDDraft, Blocks: []string{"web:cart"}},
			{ID: "brd.none", Title: "None", Status: schema.BRDDraft, ImplementsVia: []string{"orders"}}},
		EntryPoints: []schema.EntryPoint{{ID: "post-refund", Kind: schema.EntryPointKindHTTP}, {ID: "cron-sweep", Kind: schema.EntryPointKindCron}},
	}
	build := func(context.Context) (schema.KnowledgeGraph, error) { return kg, nil }
	for _, c := range []struct {
		name  string
		opts  Options
		path  string
		links []string // the focused block ids, in order; none means no link
	}{
		{"feature with a block", Options{Store: st}, "/features/refund", []string{"feat%3Arefund"}},
		{"feature without a block", Options{Store: st}, "/features/orders", nil},
		{"BRD: implementing features first", Options{Store: st}, "/brds/brd.refunds", []string{"feat%3Arefund", "feat%3Aaudit"}},
		{"BRD: blocks it names", Options{Store: st}, "/brds/brd.cart", []string{"web%3Acart"}},
		{"BRD without a block", Options{Store: st}, "/brds/brd.none", nil},
		{"entry point with a block", Options{Store: st}, "/entry-points/post-refund", []string{"ep%3Apost-refund"}},
		{"entry point without a block", Options{Store: st}, "/entry-points/cron-sweep", nil},
		{"no store", Options{}, "/features/refund", nil},
	} {
		c.opts.GraphBuilder = build
		w := httptest.NewRecorder()
		New(ws, c.opts).routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.path, nil))
		body := w.Body.String()
		if w.Code != 200 {
			t.Errorf("%s: %s = %d %s", c.name, c.path, w.Code, body)
			continue
		}
		if n := strings.Count(body, "/map#tab=focus&amp;id="); n != len(c.links) || strings.Contains(body, "data-map-links") != (len(c.links) > 0) {
			t.Errorf("%s: %d map links, want %d", c.name, n, len(c.links))
			continue
		}
		at := 0
		for _, id := range c.links {
			i := strings.Index(body[at:], "/map#tab=focus&amp;id="+id+`"`)
			if i < 0 {
				t.Errorf("%s: no link to %s in order", c.name, id)
				break
			}
			at += i + 1
		}
	}
}

// @verifies brd.adoption:US-1
func TestViewsServeAndExport(t *testing.T) {
	ws := &workspace.Workspace{LatticeDir: t.TempDir()}
	st := store.OpenFiles(ws)
	ctx := context.Background()
	if err := st.ReplaceGraph(ctx, store.Graph{Blocks: []schema.Block{{ID: "web:cart", Kind: "screen", Title: "Cart", Attrs: map[string]string{"status": "built"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(ctx, store.Record{Kind: "brd", ID: "brd.sale", Body: []byte("id: brd.sale\nversion: 1\nstatus: draft\ntitle: Sale\nbusiness_problem: x\nblocks: [\"web:cart\"]\n")}); err != nil {
		t.Fatal(err)
	}
	srv := New(ws, Options{Store: st})
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/business", nil))
	var d BusinessData
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil || len(d.View.Features) != 1 || d.View.Features[0].Built != "built" {
		t.Fatalf("/api/business = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/views", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Block views</title>") {
		t.Errorf("/views = %d", w.Code)
	}
	page, err := ViewsHTML(d, "Shop views")
	if err != nil || !strings.Contains(string(page), "<title>Shop views</title>") || strings.Contains(string(page), businessSlot) {
		t.Errorf("export: %v", err)
	}
}

func TestSaveDraft(t *testing.T) {
	ws := &workspace.Workspace{LatticeDir: t.TempDir()}
	st := store.OpenFiles(ws)
	srv := New(ws, Options{Store: st})
	post := func(body, ctype, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/brds", strings.NewReader(body))
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, r)
		return w
	}
	good := `{"version": "", "brd": {"id": "brd.draft.members", "title": "Members", "status": "draft", "business_problem": "x", "blocks": [], "flags": [], "open_questions": [],
	  "user_scenarios": [{"id": "S1", "narrative": "A member checks out", "given": ["a member"], "when": ["checkout"], "then": ["free shipping"], "blocks": []}]}}`
	if w := post(good, "application/json", "http://"+"example.com"); w.Code != http.StatusOK {
		t.Fatalf("save = %d %s", w.Code, w.Body.String())
	}
	r, err := st.Get(context.Background(), "brd", "brd.draft.members")
	if err != nil || !strings.Contains(string(r.Body), "status: draft") {
		t.Fatalf("stored = %v %s", err, r.Body)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"stale (exists, no version)": post(good, "application/json", ""),
		"form post":                  post(good, "text/plain", ""),
		"cross-site":                 post(good, "application/json", "https://evil.test"),
		"not a draft id":             post(strings.Replace(good, "brd.draft.members", "brd.members", 1), "application/json", ""),
		"no then":                    post(strings.Replace(good, `"then": ["free shipping"], `, "", 1), "application/json", ""),
	} {
		if w.Code == http.StatusOK {
			t.Errorf("%s: accepted", name)
		}
	}
}

// @verifies ui:INV-1
func TestLoopbackRefusesForeignHost(t *testing.T) {
	srv := New(&workspace.Workspace{LatticeDir: t.TempDir()}, Options{Host: "127.0.0.1"})
	for host, want := range map[string]int{"evil.test:7070": http.StatusForbidden, "127.0.0.1:7070": http.StatusServiceUnavailable, "localhost:7070": http.StatusServiceUnavailable, "[::1]:7070": http.StatusServiceUnavailable} {
		r := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
		r.Host = host
		w := httptest.NewRecorder()
		srv.withMiddleware(srv.routes()).ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %s: %d, want %d", host, w.Code, want)
		}
	}
}
