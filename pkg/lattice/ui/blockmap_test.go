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
