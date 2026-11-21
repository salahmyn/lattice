package reach

import (
	"os"
	"path/filepath"
	"testing"
)

// The cached call graph reads back what Save wrote, and a missing file or one
// of another version is no graph.
func TestLoadCache(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LoadCache(dir); ok {
		t.Fatal("no file is no cache")
	}
	r := &Result{Graph: &Graph{Edges: []Edge{{From: "a", To: "b", Kind: "call"}}}}
	if err := Save(dir, r); err != nil {
		t.Fatal(err)
	}
	c, ok := LoadCache(dir)
	if !ok || len(c.Edges) != 1 || c.Edges[0].To != "b" {
		t.Fatalf("cache = %+v (%v)", c, ok)
	}
	p := filepath.Join(dir, filepath.FromSlash(CacheFile))
	if err := os.WriteFile(p, []byte(`{"version":99,"edges":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadCache(dir); ok {
		t.Error("another version must not be read")
	}
}
