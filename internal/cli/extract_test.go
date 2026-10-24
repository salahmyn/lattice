package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// --index-root resolves inside a code root, and anything else is an error
// rather than a silent fall back to indexing from the root.
func TestResolveIndexRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{CodeRoots: []workspace.CodeRoot{{Name: "default", Abs: root, Available: true}}}

	got, err := resolveIndexRoots(ws, []string{"apps/web", filepath.Join(root, "apps")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].dir != filepath.Join(root, "apps", "web") || got[0].codeRoot != root ||
		got[1].dir != filepath.Join(root, "apps") {
		t.Errorf("resolved = %+v", got)
	}
	for _, bad := range []string{"apps/missing", "../outside", t.TempDir()} {
		if _, err := resolveIndexRoots(ws, []string{bad}); err == nil {
			t.Errorf("--index-root %s should be refused", bad)
		}
	}
}

func TestExtractIndexRootNeedsCodeGraph(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lattice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	cmd := newExtractCommand(&IO{Repo: dir, Out: &out, Err: &errOut})
	cmd.SetArgs([]string{"--index-root", "."})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err == nil || !strings.Contains(errOut.String(), "--with-code-graph") {
		t.Errorf("want a --with-code-graph error, got %v: %s", err, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "lattice", "lattice.json")); err == nil {
		t.Error("a refused extract must not write the graph")
	}
}
