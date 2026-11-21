package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/ui"
)

// nestedFeatureWorkspace writes a workspace whose features mostly live in
// nested directories under dotted ids: four top-level features, twelve
// sub-features (one two levels down, one under a prefix no feature
// declares), and an entry point whose flow names a nested feature and one
// that does not exist. It returns the repo and the promises it declares.
func nestedFeatureWorkspace(t *testing.T) (string, []string) {
	t.Helper()
	repo := t.TempDir()
	lat := filepath.Join(repo, "lattice")
	writeFile(t, filepath.Join(lat, "workspace.yaml"), "mode: embedded\n")
	writeFile(t, filepath.Join(lat, "config.yaml"), "mutation_testing:\n  enabled: false\n")
	var promises []string
	feature := func(rel, id string, invariants int) {
		var b strings.Builder
		fmt.Fprintf(&b, "id: %s\nversion: 1\nstatus: production\npurpose: The %s feature.\nowners:\n  business: sales\n  engineering: shop\n", id, id)
		if invariants > 0 {
			b.WriteString("invariants:\n")
		}
		for i := 1; i <= invariants; i++ {
			fmt.Fprintf(&b, "  - id: INV-%d\n    statement: Rule %d of %s holds.\n", i, i, id)
			promises = append(promises, fmt.Sprintf("%s:INV-%d", id, i))
		}
		writeFile(t, filepath.Join(lat, "features", filepath.FromSlash(rel)), b.String())
	}
	feature("orders.yaml", "orders", 0)
	feature("wallet.yaml", "wallet", 0)
	feature("catalog.yaml", "catalog", 0)
	feature("accounts.yaml", "accounts", 1)
	feature("orders/checkout.yaml", "orders.checkout", 2)
	feature("orders/refunds.yaml", "orders.refunds", 2)
	feature("orders/refunds/partial.yaml", "orders.refunds.partial", 1)
	feature("wallet/topup.yaml", "wallet.topup", 2)
	feature("wallet/withdraw.yaml", "wallet.withdraw", 1)
	feature("wallet/fees.yaml", "wallet.fees", 2)
	feature("catalog/search.yaml", "catalog.search", 1)
	feature("catalog/pricing.yaml", "catalog.pricing", 2)
	feature("catalog/stock.yaml", "catalog.stock", 1)
	feature("accounts/login.yaml", "accounts.login", 2)
	feature("accounts/profile.yaml", "accounts.profile", 1)
	feature("reports/daily.yaml", "reports.daily", 1)
	writeFile(t, filepath.Join(lat, "entry-points", "ep.http.refund.yaml"), `id: ep.http.refund
version: 1
status: production
kind: http_route
purpose: Refund an order.
trigger:
  method: POST
  path: /refunds
handler:
  symbol: app.refund
  file: app.go
flow:
  - feature: orders.refunds
  - feature: orders.missing
`)
	sort.Strings(promises)
	return repo, promises
}

// exportMap runs lattice export map on repo and returns the page's data.
func exportMap(t *testing.T, repo string, args ...string) (ui.MapData, string) {
	t.Helper()
	page := filepath.Join(t.TempDir(), "map.html")
	out, err := run(t, repo, newExportCommand, append([]string{"map", page}, args...)...)
	if err != nil {
		t.Fatalf("export map: %v\n%s", err, out)
	}
	html, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	const open = `<script id="graph" type="application/json">`
	_, rest, ok := bytes.Cut(html, []byte(open))
	data, _, ok2 := bytes.Cut(rest, []byte("</script>"))
	if !ok || !ok2 {
		t.Fatalf("no inlined graph in the page")
	}
	var d ui.MapData
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("inlined graph: %v", err)
	}
	return d, out
}

// A workspace that never ran refresh exports a map with every feature and
// invariant on it: export map builds the block graph, saves it, and every
// audited promise has a block, nested under its feature's parent.
// @verifies blocks:INV-4
// @verifies cli:INV-5
// @verifies cli:INV-6
func TestExportMapPlacesEveryPromise(t *testing.T) {
	repo, promises := nestedFeatureWorkspace(t)
	d, out := exportMap(t, repo)

	if d.Refresh == nil || !d.Refresh.Rebuilt || !d.Refresh.Saved || d.Refresh.Note != "" {
		t.Fatalf("refresh = %+v, want rebuilt and saved\n%s", d.Refresh, out)
	}
	if !d.Assurance.Available {
		t.Fatalf("audit not available: %s", d.Assurance.Note)
	}
	if n := len(d.Assurance.Unplaced); n != 0 {
		t.Errorf("%d audited promises have no block on the map, want 0: %v", n, d.Assurance.Unplaced)
	}
	if len(d.Assurance.Promises) != len(promises) {
		t.Errorf("%d promises on the lens, want %d", len(d.Assurance.Promises), len(promises))
	}
	for _, p := range promises {
		lp, ok := d.Assurance.Promises[p]
		if !ok || lp.Block == "" || lp.Missing {
			t.Errorf("promise %s: %+v, want a block", p, lp)
		}
	}
	parents := map[string]string{}
	for _, b := range d.Blocks {
		parents[b.ID] = b.Parent
	}
	for id, want := range map[string]string{
		"feat:orders.refunds":         "feat:orders",
		"feat:orders.refunds.partial": "feat:orders.refunds",
		"feat:wallet.fees":            "feat:wallet",
		"feat:reports.daily":          "feat-group:reports",
		"inv:orders.refunds.INV-2":    "feat:orders.refunds",
		"feat:orders":                 "",
	} {
		if got, ok := parents[id]; !ok || got != want {
			t.Errorf("%s parent = %q (present %v), want %q", id, got, ok, want)
		}
	}
	// a composite holds the promises of the features inside it
	if n := d.Assurance.Nodes["feat:orders"]; len(n.Promises) != 5 || n.Style == ui.StyleNone {
		t.Errorf("feat:orders holds %v (%s), want the 5 promises of its sub-features", n.Promises, n.Style)
	}

	// the graph was saved: a second export finds it current and writes nothing
	before, err := os.ReadFile(filepath.Join(repo, "lattice", "lattice.json"))
	if err != nil {
		t.Fatalf("export map did not save the block graph: %v", err)
	}
	d2, _ := exportMap(t, repo)
	if d2.Refresh == nil || d2.Refresh.Rebuilt || d2.Refresh.Note != "" || len(d2.Blocks) != len(d.Blocks) {
		t.Errorf("second export: refresh %+v, %d blocks (first %d)", d2.Refresh, len(d2.Blocks), len(d.Blocks))
	}
	if after, _ := os.ReadFile(filepath.Join(repo, "lattice", "lattice.json")); !bytes.Equal(before, after) {
		t.Errorf("a current graph was written again")
	}

	// --no-refresh draws the stored graph as it is
	if d3, _ := exportMap(t, repo, "--no-refresh"); d3.Refresh != nil || len(d3.Blocks) != len(d.Blocks) {
		t.Errorf("--no-refresh: refresh %+v, %d blocks", d3.Refresh, len(d3.Blocks))
	}
}

// tree returns the content of every file under dir but the skipped ones.
func tree(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		for _, s := range skip {
			if rel == s {
				return nil
			}
		}
		data, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// When the block graph cannot be rebuilt, export map draws the stored graph,
// says why on the page, and writes nothing; when it can, the store's graph
// file is the only thing it writes.
// @verifies cli:INV-6
func TestExportMapKeepsTheStoredGraphWhenRefreshFails(t *testing.T) {
	repo, _ := nestedFeatureWorkspace(t)
	lat := filepath.Join(repo, "lattice")
	before := tree(t, repo)
	d, _ := exportMap(t, repo)
	if d.Refresh == nil || !d.Refresh.Saved {
		t.Fatalf("refresh = %+v", d.Refresh)
	}
	after := tree(t, repo, filepath.Join("lattice", "lattice.json"))
	for p := range after {
		if before[p] != after[p] {
			t.Errorf("export map wrote %s", p)
		}
	}
	stored, err := os.ReadFile(filepath.Join(lat, "lattice.json"))
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(lat, "workspace.yaml"), "mode: embedded\nsources:\n  - id: shop\n    clone: ../no-such-clone\n    catalogue: \"*.json\"\n")
	d, out := exportMap(t, repo)
	if d.Refresh == nil || d.Refresh.Rebuilt || !strings.Contains(d.Refresh.Note, "SOURCE_MISSING") || !strings.Contains(d.Refresh.Note, "lattice refresh") {
		t.Errorf("refresh = %+v, want a note naming SOURCE_MISSING and the fix", d.Refresh)
	}
	if !strings.Contains(out, "SOURCE_MISSING") {
		t.Errorf("output does not say why the graph was not rebuilt:\n%s", out)
	}
	var ids []string
	for _, b := range d.Blocks {
		ids = append(ids, b.ID)
	}
	if !contains(ids, "feat:orders.refunds.partial") {
		t.Errorf("the stored graph was not drawn: %d blocks", len(d.Blocks))
	}
	if now, _ := os.ReadFile(filepath.Join(lat, "lattice.json")); !bytes.Equal(stored, now) {
		t.Errorf("a failed rebuild changed the stored graph")
	}
}

// refresh and validate judge an entry point's flow steps over the same
// features: a step naming a nested feature resolves in both, and a step
// naming no feature is PHANTOM_FLOW in validate, with a next action, and
// the one step refresh warns about.
// @verifies cli:INV-5
func TestRefreshAndValidateAgreeOnFlowSteps(t *testing.T) {
	repo, _ := nestedFeatureWorkspace(t)
	out, err := run(t, repo, newRefreshCommand)
	if err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	var rep refreshReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("refresh report: %v\n%s", err, out)
	}
	var warned []string
	for _, w := range rep.Warnings {
		if strings.Contains(w, "flow step") {
			warned = append(warned, w)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0], `"orders.missing"`) || !strings.Contains(warned[0], schema.CodePhantomFlow) {
		t.Errorf("refresh flow warnings = %v, want one for orders.missing naming %s", warned, schema.CodePhantomFlow)
	}

	out, _ = run(t, repo, newValidateCommand)
	var vr struct {
		Violations []schema.Violation `json:"violations"`
	}
	if err := json.Unmarshal([]byte(out), &vr); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	var phantom []schema.Violation
	for _, v := range vr.Violations {
		if v.Code == schema.CodePhantomFlow {
			phantom = append(phantom, v)
		}
	}
	if len(phantom) != 1 || phantom[0].FeatureID != "orders.missing" || phantom[0].NextAction == nil || phantom[0].NextAction.Ref != "ep.http.refund" {
		t.Errorf("validate PHANTOM_FLOW = %+v, want one for orders.missing with a next action", phantom)
	}
}
