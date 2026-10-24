package scip

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// cwdIndexer is an adapter whose "indexer" copies index.scip from the
// directory it runs in, so a test sees where it ran.
type cwdIndexer struct{}

func (cwdIndexer) Name() string             { return "fake" }
func (cwdIndexer) FileExtensions() []string { return []string{".fake"} }
func (cwdIndexer) CanParse(p string) bool   { return filepath.Ext(p) == ".fake" }
func (cwdIndexer) MutationRunnerCommand(string, []string) ([]string, error) {
	return nil, adapters.ErrMutationRunnerNotConfigured
}
func (cwdIndexer) Parse(context.Context, string, []byte) (ir.Module, error) { return ir.Module{}, nil }
func (cwdIndexer) RenderAnnotationSuggestion(ir.Symbol, []adapters.AnnotationSuggestion) (string, error) {
	return "", nil
}
func (cwdIndexer) SCIPIndexerCommand(_, out string) ([]string, error) {
	return []string{"sh", "-c", `cp index.scip "$0"`, out}, nil
}

func writeFixtureIndex(t *testing.T, dir, doc, symbol string) {
	t.Helper()
	idx := &scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file://" + dir},
		Documents: []*scippb.Document{{RelativePath: doc, Occurrences: []*scippb.Occurrence{
			{Range: []int32{0, 0, 1}, Symbol: symbol, SymbolRoles: int32(scippb.SymbolRole_Definition)},
		}}},
	}
	data, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Indexers run in each index root, and the merged index reads with paths
// relative to the code root, as an index run from the root would.
// @verifies scip:INV-2
func TestOrchestrateRoots(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	code := t.TempDir()
	out := filepath.Join(code, "lattice", ".cache", "scip")
	web := filepath.Join(code, "apps", "web")
	api := filepath.Join(code, "apps", "api")
	empty := filepath.Join(code, "apps", "empty") // no index.scip: its run fails
	writeFixtureIndex(t, web, "src/a.ts", "scip-typescript npm web 1.0 src/`a.ts`/Checkout().")
	writeFixtureIndex(t, api, "src/b.ts", "scip-typescript npm api 1.0 src/`b.ts`/Refund().")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	reg := adapters.NewRegistry(cwdIndexer{})
	res := OrchestrateRoots(context.Background(), code, []string{web, api, empty, t.TempDir()}, out, reg, 0)

	var ok, failed, outside int
	for _, r := range res {
		switch {
		case r.OK:
			ok++
		case r.Language == "" && r.Error != "":
			outside++
		case r.Error != "":
			failed++
		}
	}
	if ok != 2 || failed != 1 || outside != 1 {
		t.Errorf("results = %+v; want 2 ok, 1 failed run, 1 root outside the code root", res)
	}

	idx, err := readIndex(filepath.Join(out, "fake.scip"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, d := range idx.Documents {
		paths = append(paths, d.RelativePath)
	}
	sort.Strings(paths)
	if !reflect.DeepEqual(paths, []string{"apps/api/src/b.ts", "apps/web/src/a.ts"}) {
		t.Errorf("document paths = %v, want them relative to the code root", paths)
	}
	if idx.Metadata.GetProjectRoot() != "file://"+filepath.ToSlash(code) {
		t.Errorf("project root = %q", idx.Metadata.GetProjectRoot())
	}
	left, _ := filepath.Glob(filepath.Join(out, "*.part.scip"))
	if len(left) != 0 {
		t.Errorf("per-root parts left behind: %v", left)
	}

	c, err := Load(filepath.Join(out, "fake.scip"))
	if err != nil {
		t.Fatal(err)
	}
	if br := c.BlastRadius("web.Checkout"); !br.Resolved || !reflect.DeepEqual(br.Files, []string{"apps/web/src/a.ts"}) {
		t.Errorf("blast radius = %+v", br)
	}
}
