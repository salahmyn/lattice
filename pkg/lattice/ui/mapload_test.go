package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// loadStatus is the map's load status element as the page ships it: a polite
// live region, hidden until the page starts fetching the graph.
const loadStatus = `<div id="pane"><div id="gload" class="gload" role="status" aria-live="polite" hidden></div></div>`

func mapServer(t *testing.T) *Server {
	t.Helper()
	ws := &workspace.Workspace{LatticeDir: t.TempDir()}
	st := store.OpenFiles(ws)
	g := store.Graph{Blocks: []schema.Block{{ID: "sys.shop", Kind: "system", Title: "Shop", Truth: "declared"},
		{ID: "web:cart", Kind: "screen", Parent: "sys.shop", Title: "Cart", Truth: "declared"},
		{ID: "api:items", Kind: "route", Parent: "sys.shop", Title: "POST /items", Truth: "wired"}},
		Edges: []schema.BlockEdge{{From: "web:cart", To: "api:items", Kind: "api"}}}
	if err := st.ReplaceGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	return New(ws, Options{Store: st})
}

// The served map carries the load status and its retry path; the exported
// map carries the element hidden, and its graph inline, so it never shows.
// @verifies ui:INV-6
func TestMapLoadStatus(t *testing.T) {
	w := httptest.NewRecorder()
	mapServer(t).routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/map", nil))
	page := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(page, loadStatus) {
		t.Fatalf("/map = %d, lacks the load status %s", w.Code, loadStatus)
	}
	for _, want := range []string{
		`await loadGraph(gload)`, `fetch("api/graph" + location.search)`, // the served page fetches through the status
		`Building the block graph…`, `class="ld-sec"`, `if (s >= 3) hint.hidden = false`, `later loads are cached`, // live status, then the hint
		`el.className = "gload err"`, `HTTP ${r.status}`, `<button type="button" data-retry>Retry</button>`, // the error and its retry
		`gload.remove();`, // gone once the graph is in hand
	} {
		if !strings.Contains(page, want) {
			t.Errorf("/map lacks %s", want)
		}
	}
	export := string(mapExport(t))
	if !strings.Contains(export, loadStatus) || strings.Contains(export, `id="gload" class="gload" role="status" aria-live="polite">`) {
		t.Error("the exported map should carry the load status hidden")
	}
	if strings.Contains(export, "/*__GRAPH__*/") || strings.Contains(export, `<script id="graph" type="application/json"></script>`) {
		t.Error("the exported map has no inline graph, so it would fetch and show the status")
	}
}

// TestMapLoadStatusBrowser opens the served map in Chromium with
// scripts/blockmap-smoke.js --served, /api/graph intercepted: slow, it must
// show a live status with the elapsed seconds and, after about 3 s, the
// first-load hint; failing, an error naming the cause with a retry that
// loads it; and once the map (or the grid fallback) draws, no status. It
// skips in -short mode and when node or the playwright package is missing;
// it never installs browsers.
// @verifies ui:INV-6
func TestMapLoadStatusBrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: no browser")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	if err := exec.Command(node, "-e", "require('playwright')").Run(); err != nil {
		t.Skip("the playwright package is not installed")
	}
	ts := httptest.NewServer(mapServer(t).Handler())
	defer ts.Close()
	out, err := exec.Command(node, filepath.Join("..", "..", "..", "scripts", "blockmap-smoke.js"), "--served", ts.URL).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Executable doesn't exist") {
			t.Skip("playwright has no browser installed")
		}
		t.Fatalf("smoke test failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"ok   served, slow graph: a polite live status says the graph is being built",
		"ok   served, slow graph: the elapsed seconds update",
		"ok   served, slow graph: after about 3 s the status says the first load builds the graph and later loads are cached",
		"ok   served, slow graph: the status is gone once the map draws",
		"ok   served, HTTP 500: the status turns into an error naming the status and the cause, with a retry",
		"ok   served, network failure: retry fetches again and the error names the failure",
		"ok   served: retry after a failure loads the graph, and the status is gone once the map draws",
		"ok   served, no layout library: the grid fallback draws and the status is gone",
		"all passed",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("smoke output lacks %q:\n%s", want, out)
		}
	}
	t.Logf("%s", out)
}
