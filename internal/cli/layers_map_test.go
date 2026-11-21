package cli

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The map's Layer lens, in Chromium: the fixture's blocks carry the layers the
// kernel computed (extract, then refresh), the lens groups them by layer, a
// data block and an unknown block are drawn in different colours, a block that
// spans several layers is neutral, and the legend names each layer. It skips
// in -short mode and when node, playwright or a browser is missing.
// @verifies graph:INV-6
func TestBlockMapLayerLens(t *testing.T) {
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
	dir := layersFixture(t, "mode: embedded\nlayers:\n  - glob: \"web/**\"\n    layer: ui\n")
	if o, err := run(t, dir, newExtractCommand); err != nil {
		t.Fatalf("extract: %v\n%s", err, o)
	}
	var out, errOut bytes.Buffer
	if err := runRefresh(&IO{Repo: dir, JSON: true, Out: &out, Err: &errOut}); err != nil {
		t.Fatalf("refresh: %v\n%s%s", err, out.String(), errOut.String())
	}
	page := filepath.Join(dir, "map.html")
	cmd := newExportCommand(&IO{Repo: dir, Out: &out, Err: &errOut})
	cmd.SetArgs([]string{"map", page})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("export map: %v\n%s", err, errOut.String())
	}
	res, err := exec.Command(node, filepath.Join("..", "..", "scripts", "blockmap-smoke.js"), page, "--layers").CombinedOutput()
	if err != nil {
		if strings.Contains(string(res), "Executable doesn't exist") {
			t.Skip("playwright has no browser installed")
		}
		t.Fatalf("layer lens smoke test failed: %v\n%s", err, res)
	}
	for _, want := range []string{
		"ok   a data block and an unknown block are drawn in different colours",
		"ok   a block spanning several layers has a neutral colour of its own",
		"ok   the legend lists each layer present",
	} {
		if !strings.Contains(string(res), want) {
			t.Errorf("the smoke run lacks %q:\n%s", want, res)
		}
	}
}
