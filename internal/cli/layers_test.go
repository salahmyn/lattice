package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

const layersFeature = `id: cart
version: 1
status: production
purpose: |
  Totals a cart.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: A cart total is never negative on screen.
  - id: INV-2
    statement: The database rejects a negative total.
  - id: INV-3
    statement: A total is rounded once.
  - id: INV-4
    statement: A total is shown and stored.
`

func layersFixture(t *testing.T, workspaceYAML string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"lattice/config.yaml":         "{}\n",
		"lattice/adapters.yaml":       "adapters:\n  typescript:\n    enabled: true\n  postgres:\n    enabled: true\n",
		"lattice/workspace.yaml":      workspaceYAML,
		"lattice/features/cart.yaml":  layersFeature,
		"web/cart.ts":                 "/** @module-feature cart */\n\n/**\n * @enforces cart:INV-1\n * @enforces cart:INV-4\n */\nexport function showTotal(n: number): string {\n  return n < 0 ? '0' : String(n);\n}\n",
		"db/migrations/0001_cart.sql": "-- @module-feature cart\n\n-- @enforces cart:INV-2\n-- @enforces cart:INV-4\ncreate policy no_negative on public.carts using (total >= 0);\n",
		"lib/round.ts":                "/** @module-feature cart */\n\n/**\n * @enforces cart:INV-3\n */\nexport function round(n: number): number {\n  return Math.round(n);\n}\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A rule that names a layer outside the vocabulary is a VALIDATE_POLICY error.
// @verifies graph:INV-6
func TestValidateReportsUnknownLayerName(t *testing.T) {
	dir := layersFixture(t, "mode: embedded\nlayers:\n  - glob: \"web/**\"\n    layer: frontend\n")
	out, err := run(t, dir, newValidateCommand)
	if err == nil {
		t.Fatalf("validate passed with an unknown layer name:\n%s", out)
	}
	if !strings.Contains(out, "VALIDATE_POLICY") || !strings.Contains(out, "layers[0]") || !strings.Contains(out, "frontend") {
		t.Errorf("want a VALIDATE_POLICY finding naming layers[0] and \"frontend\":\n%s", out)
	}
}

// extract writes the layer and its source onto each symbol, and the derived
// layers onto feature and invariant blocks it carries.
// @verifies graph:INV-6
func TestExtractWritesLayers(t *testing.T) {
	dir := layersFixture(t, "mode: embedded\nlayers:\n  - glob: \"web/**\"\n    layer: ui\n")
	if out, err := run(t, dir, newExtractCommand); err != nil {
		t.Fatalf("extract: %v\n%s", err, out)
	}
	ws, err := openWorkspace(&IO{Repo: dir})
	if err != nil {
		t.Fatal(err)
	}
	kg, err := buildGraph(t.Context(), ws, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, s := range kg.Symbols {
		got[s.File] = [2]string{s.Layer, s.LayerSource}
	}
	if got["web/cart.ts"] != [2]string{schema.LayerUI, "rule"} || got["db/migrations/0001_cart.sql"] != [2]string{schema.LayerData, "adapter"} || got["lib/round.ts"] != [2]string{schema.LayerUnknown, ""} {
		t.Errorf("symbol layers = %v", got)
	}
}
