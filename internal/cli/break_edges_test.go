package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
)

const edgeFeature = `id: orders
version: 1
status: production
purpose: |
  Lists orders and refunds.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: A customer sees only their own orders.
  - id: INV-2
    statement: A customer sees only their own refunds.
`

// edgeFiles is a client that fetches two routes and the API that serves them.
// Each route handler enforces one promise. INV-1 has only a UI test with the
// backend faked (it never looks at the path); INV-2 has a test that runs the
// client against the routes, as a real end-to-end test does.
func edgeFiles() map[string]string {
	return map[string]string{
		".gitignore":                   "lattice/.cache/\nlattice/lattice.json\n",
		"lattice/config.yaml":          edgeConfig(),
		"lattice/adapters.yaml":        "adapters:\n  typescript:\n    enabled: true\n",
		"lattice/workspace.yaml":       "mode: embedded\nlayers:\n  - glob: \"web/**\"\n    layer: ui\n  - glob: \"api/**\"\n    layer: api\n",
		"lattice/features/orders.yaml": edgeFeature,
		"index.html":                   "<!doctype html>\n<script type=\"module\" src=\"/web/main.ts\"></script>\n",
		"web/main.ts":                  "/** @module-feature orders */\n\nimport { loadOrders, loadRefunds } from './client';\n\nloadOrders();\nloadRefunds();\n",
		"web/client.ts":                "/** @module-feature orders */\n\nexport function loadOrders() {\n  return fetch('/api/orders');\n}\n\nexport function loadRefunds() {\n  return fetch('/api/refunds');\n}\n",
		"api/routes.ts":                "/** @module-feature orders */\n\n/** @enforces orders:INV-1 */\nfunction listOrders(req: any, res: any) {\n  return res.json([]);\n}\n\n/** @enforces orders:INV-2 */\nfunction listRefunds(req: any, res: any) {\n  return res.json([]);\n}\n\napp.get('/api/orders', listOrders);\napp.get('/api/refunds', listRefunds);\n",
		"web/orders.ui.test.ts":        "/** @module-feature orders */\n\n/** @verifies orders:INV-1 */\ntest('orders render', () => {});\n",
		"web/refunds.e2e.test.ts":      "/** @module-feature orders */\n\n/** @verifies orders:INV-2 */\ntest('refunds load', () => {});\n",
	}
}

// edgeConfig has two fake runners. The UI one passes whatever the client
// sends, as a test with a faked backend does. The end-to-end one fails when
// a path the client fetches is not one a route serves, as a test that runs
// the client against the real routes does.
func edgeConfig() string {
	e2e := `for p in $(grep -oE "fetch\('[^']*'" web/client.ts | sed "s/fetch('//; s/'//"); do ` +
		`grep -qF "'$p'" api/routes.ts || { echo "not ok 1 - $p is not served"; exit 1; }; done; echo 'ok 1 - routes answer'`
	return "break:\n  runners:\n" +
		"    - name: ui\n      match: [\"web/*.ui.test.ts\"]\n      command: [sh, -c, " + shellYAML("echo 'ok 1 - ui'") + ", sh, \"{files}\"]\n" +
		"    - name: e2e\n      match: [\"web/*.e2e.test.ts\"]\n      command: [sh, -c, " + shellYAML(e2e) + ", sh, \"{files}\"]\n"
}

func edgeFixture(t *testing.T, mutate func(files map[string]string)) string {
	t.Helper()
	for _, tool := range []string{"git", "sh", "grep", "sed"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	files := edgeFiles()
	if mutate != nil {
		mutate(files)
	}
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fixture")
	return dir
}

// edgeRuns picks the edge runs of a report by promise.
func edgeRuns(rep breaktest.Report) map[string][]breaktest.Run {
	out := map[string][]breaktest.Run{}
	for _, r := range rep.Runs {
		if r.Method == breaktest.MethodEdge {
			out[r.Promise] = append(out[r.Promise], r)
		}
	}
	return out
}

// A literal request, alone, is a hop whose out end can be broken. A verifier
// that runs the client against the real route notices the path no longer
// matches and kills the break; a UI test with the backend faked passes with
// the path altered, so its edge break survives and EDGE_BREAK_SURVIVED names
// the hop. The enforcer runs are unchanged, and the working tree is not edited.
// @verifies breaktest:INV-19
func TestBreakEdgesHTTPHop(t *testing.T) {
	dir := edgeFixture(t, nil)
	rep, out, err := runBreak(t, dir, "--edges")
	if err != nil {
		t.Fatalf("break --edges: %v\n%s", err, out)
	}
	edges := edgeRuns(rep)

	ui := edges["orders:INV-1"]
	if len(ui) != 1 {
		t.Fatalf("orders:INV-1 edge runs = %+v", edges["orders:INV-1"])
	}
	r := ui[0]
	if r.Hop == nil || r.Hop.Kind != "http" || r.Hop.From != "web.client.loadOrders" || r.Hop.To != "api.routes.listOrders" {
		t.Errorf("hop = %+v", r.Hop)
	}
	if r.Outcome != breaktest.Survived || r.Baseline != "pass" || r.Finding != breaktest.CodeEdgeBreakSurvived || r.EnforcerFile != "web/client.ts" {
		t.Errorf("UI-only verifier: outcome %s baseline %s finding %q file %q (%s), want survived", r.Outcome, r.Baseline, r.Finding, r.EnforcerFile, r.Reason)
	}

	e2e := edges["orders:INV-2"]
	if len(e2e) != 1 {
		t.Fatalf("orders:INV-2 edge runs = %+v", edges["orders:INV-2"])
	}
	if r := e2e[0]; r.Outcome != breaktest.Killed || r.Finding != "" || len(r.FailedTests) == 0 && r.Reason == "" {
		t.Errorf("end-to-end verifier: outcome %s finding %q (%s), want killed", r.Outcome, r.Finding, r.Reason)
	}

	// The enforcer runs are there too, with the same outcomes as without --edges.
	for _, p := range []string{"orders:INV-1", "orders:INV-2"} {
		found := false
		for _, run := range rep.Runs {
			if run.Promise == p && run.Method == breaktest.MethodEarlyReturn {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no enforcer run beside the edge run", p)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "web", "client.ts")); !strings.Contains(string(got), "fetch('/api/orders')") || strings.Contains(string(got), "lattice-break") {
		t.Errorf("the working tree was edited:\n%s", got)
	}
	if st := runGit(t, dir, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Errorf("the working tree is dirty after the run:\n%s", st)
	}
}

// Without --edges (and without break.edges), no edge run is made.
// @verifies breaktest:INV-19
func TestBreakEdgesOffByDefault(t *testing.T) {
	dir := edgeFixture(t, nil)
	rep, out, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if e := edgeRuns(rep); len(e) != 0 {
		t.Errorf("edge runs without --edges: %+v", e)
	}
	if strings.Contains(out, `"hop"`) {
		t.Errorf("the report names a hop without --edges:\n%s", out)
	}
}

// break.edges: true in config.yaml turns them on without the flag.
// @verifies breaktest:INV-19
func TestBreakEdgesFromConfig(t *testing.T) {
	dir := edgeFixture(t, func(files map[string]string) {
		files["lattice/config.yaml"] = strings.Replace(edgeConfig(), "break:\n", "break:\n  edges: true\n", 1)
	})
	rep, out, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if e := edgeRuns(rep); len(e["orders:INV-1"]) != 1 || len(e["orders:INV-2"]) != 1 {
		t.Errorf("edge runs from config = %+v", e)
	}
}

// A hop two routes serve is unknown (an ambiguous reference followed to one of
// its candidates): it is listed with the reason it is not broken, and no
// verifier runs for it.
// @verifies breaktest:INV-19
func TestBreakEdgesSkipUnknownHop(t *testing.T) {
	dir := edgeFixture(t, func(files map[string]string) {
		files["api/legacy.ts"] = "/** @module-feature orders */\n\nfunction legacyOrders() {}\nrouter.get('/api/orders', legacyOrders);\n"
	})
	rep, out, err := runBreak(t, dir, "--edges", "--promise", "orders:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	edges := edgeRuns(rep)["orders:INV-1"]
	if len(edges) != 1 {
		t.Fatalf("edge runs = %+v", edges)
	}
	r := edges[0]
	if r.Outcome != breaktest.Unknown || !strings.HasPrefix(r.Reason, "skipped: ") || !strings.Contains(r.Reason, "ambiguous") || r.Baseline != "" || len(r.RanVerifiers) != 0 {
		t.Errorf("unknown hop: outcome %s baseline %q reason %q, want skipped and nothing run", r.Outcome, r.Baseline, r.Reason)
	}
}

// A request whose URL is computed is no hop: nothing is broken, because the
// literal that would be altered does not exist.
// @verifies breaktest:INV-19
func TestBreakEdgesNeverBreakComputedURL(t *testing.T) {
	dir := edgeFixture(t, func(files map[string]string) {
		files["web/client.ts"] = "/** @module-feature orders */\n\nconst base = '/api';\n\nexport function loadOrders() {\n  return fetch(base + '/orders');\n}\n\nexport function loadRefunds() {\n  return fetch(`${base}/refunds`);\n}\n"
		files["web/refunds.e2e.test.ts"] = "/** @module-feature orders */\n\n/** @verifies orders:INV-2 */\ntest('refunds load', () => {});\n"
	})
	rep, out, err := runBreak(t, dir, "--edges")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if e := edgeRuns(rep); len(e) != 0 {
		t.Errorf("a computed URL was broken: %+v", e)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "web", "client.ts")); strings.Contains(string(got), "lattice-break") {
		t.Error("the working tree was edited")
	}
}

// A killed edge break marks the hop break_tested in the slice, with the
// verifiers that failed; a survived one marks nothing.
// @verifies breaktest:INV-19
func TestSliceShowsBreakTestedHop(t *testing.T) {
	dir := edgeFixture(t, nil)
	if _, out, err := runBreak(t, dir, "--edges"); err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	hopOf := func(promise string) slice.Hop {
		t.Helper()
		out, err := run(t, dir, newSliceCommand, promise)
		if err != nil {
			t.Fatalf("slice %s: %v\n%s", promise, err, out)
		}
		var rec slice.Record
		if err := json.Unmarshal([]byte(out), &rec); err != nil {
			t.Fatalf("slice output: %v\n%s", err, out)
		}
		for _, p := range rec.Paths {
			for _, h := range p.Hops {
				if h.Kind == "http" {
					return h
				}
			}
		}
		t.Fatalf("%s: no http hop in %+v", promise, rec.Paths)
		return slice.Hop{}
	}
	if h := hopOf("orders:INV-2"); !h.BreakTested || len(h.BreakTestedBy) == 0 {
		t.Errorf("INV-2 hop after a killed edge break = %+v", h)
	}
	if h := hopOf("orders:INV-1"); h.BreakTested || len(h.BreakTestedBy) != 0 {
		t.Errorf("INV-1 hop after a survived edge break = %+v", h)
	}
}

// An edge run is reused only for the same hop and the same out-end file: the
// second run of the same tree takes the outcomes from the first, and editing
// the out-end file (so a new commit) runs it again.
// @verifies breaktest:INV-19
func TestBreakEdgesCache(t *testing.T) {
	dir := edgeFixture(t, nil)
	if _, out, err := runBreak(t, dir, "--edges"); err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	rep, out, err := runBreak(t, dir, "--edges")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	for p, rs := range edgeRuns(rep) {
		for _, r := range rs {
			if !r.Cached {
				t.Errorf("%s: the edge run of an unchanged tree was not reused: %+v", p, r)
			}
		}
	}
	// A different out-end file: the cache key differs.
	client := filepath.Join(dir, "web", "client.ts")
	src, _ := os.ReadFile(client)
	if err := os.WriteFile(client, append(src, []byte("\n// a note\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-am", "edit the client")
	rep, out, err = runBreak(t, dir, "--edges")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	for p, rs := range edgeRuns(rep) {
		for _, r := range rs {
			if r.Cached {
				t.Errorf("%s: an edge run was reused after its out-end file changed: %+v", p, r)
			}
		}
	}
}

// The table names the hop of an edge run, and a survived one ends the output
// with the EDGE_BREAK_SURVIVED finding.
// @verifies breaktest:INV-19
func TestBreakEdgesTable(t *testing.T) {
	dir := edgeFixture(t, nil)
	res := runBreakExit(t, dir, false, "--edges")
	if res.code != 0 {
		t.Fatalf("exit %d\n%s%s", res.code, res.out, res.err)
	}
	for _, want := range []string{
		"web.client.loadOrders -http-> api.rou", // the ENFORCER column is cut at 34 columns
		"edge",
		"EDGE_BREAK_SURVIVED (info): no verifier noticed 1 hop(s) breaking",
		"orders:INV-1: web.client.loadOrders -http-> api.routes.listOrders",
	} {
		if !strings.Contains(res.out, want) {
			t.Errorf("output lacks %q:\n%s", want, res.out)
		}
	}
}
