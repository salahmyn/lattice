package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// exampleMap refreshes a copy of examples/block-map and exports its map,
// returning the report and the exported file's path.
func exampleMap(t *testing.T) (refreshReport, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.CopyFS(repo, os.DirFS(filepath.Join("..", "..", "examples", "block-map"))); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, "lattice", "lattice.json")) // a local refresh may have left one
	var out, errOut bytes.Buffer
	if err := runRefresh(&IO{Repo: repo, JSON: true, Out: &out, Err: &errOut}); err != nil {
		t.Fatalf("refresh: %v\n%s%s", err, out.String(), errOut.String())
	}
	var rep refreshReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("refresh report: %v\n%s", err, out.String())
	}
	page := filepath.Join(repo, "map.html")
	cmd := newExportCommand(&IO{Repo: repo, Out: &out, Err: &errOut})
	cmd.SetArgs([]string{"map", page, "--title", "Demo bookshop"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("export map: %v\n%s", err, errOut.String())
	}
	return rep, page
}

// The demo workspace refreshes without a warning into a graph of the size the
// docs promise, and exports as one page with the explorer and the graph in it.
func TestBlockMapExample(t *testing.T) {
	rep, page := exampleMap(t)
	if len(rep.Warnings) > 0 {
		t.Errorf("refresh warnings: %v", rep.Warnings)
	}
	if rep.Blocks < 100 || rep.Blocks > 200 || rep.Flows < 2 {
		t.Errorf("demo graph: %d blocks, %d flows; want 100-200 blocks and at least 2 flows", rep.Blocks, rep.Flows)
	}
	for _, k := range []string{"flow", "api", "event", "server_api", "uses", "configures", "replaces"} {
		if rep.Edges[k] == 0 {
			t.Errorf("no %s edges: %v", k, rep.Edges)
		}
	}
	html, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<title>Demo bookshop</title>", `data-tab="focus"`, `data-tab="discover"`, `"id":"sys.checkout"`, `"owner":"team-payments"`, `"initiative":"pay-sheet"`} {
		if !bytes.Contains(html, []byte(want)) {
			t.Errorf("exported map lacks %s", want)
		}
	}
}

// TestBlockMapBrowserSmoke drives the exported demo map in Chromium with
// scripts/blockmap-smoke.js. It skips in -short mode and when node or the
// playwright package is missing; it never installs browsers. LATTICE_ELK_JS,
// a local copy of elkjs 0.9.3, lets the page lay out with ELK instead of its
// offline grid.
func TestBlockMapBrowserSmoke(t *testing.T) {
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
	_, page := exampleMap(t)
	args := []string{filepath.Join("..", "..", "scripts", "blockmap-smoke.js"), page}
	if elk := os.Getenv("LATTICE_ELK_JS"); elk != "" {
		args = append(args, "--elk", elk)
	}
	out, err := exec.Command(node, args...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Executable doesn't exist") {
			t.Skip("playwright has no browser installed")
		}
		t.Fatalf("smoke test failed: %v\n%s", err, out)
	}
	// the demo must exercise these checks, not skip them
	for _, want := range []string{
		"ok   Diagram: after Fit with the minimap on, no box is under the minimap",
		"ok   Focus: after Fit with the minimap on, no card is under the minimap",
		"ok   system context at 2 hop(s): each block once",
		"ok   a block linked both ways is marked ⇄ both ways",
		"ok   Discover names the proposal-only blocks: 159 of 159 blocks · 8 more only in proposals",
		"ok   clicking it selects the proposal (159 -> 167 rows)",
		"ok   no console errors",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("smoke output lacks %q", want)
		}
	}
	t.Logf("%s", out)
}
