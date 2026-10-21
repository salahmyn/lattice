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
	page, err := MapHTML(g)
	if err != nil || strings.Contains(string(page), "Cart </script>") || !strings.Contains(string(page), `Cart \u003c/script\u003e`) {
		t.Errorf("export does not escape the inlined graph")
	}
	if w := httptest.NewRecorder(); true {
		New(ws, Options{}).routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("no store: %d", w.Code)
		}
	}
}
