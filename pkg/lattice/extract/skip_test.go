package extract

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// @verifies extraction:INV-1
func TestSkipDir(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, gitFile string) string {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if gitFile != "" {
			if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(gitFile), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	src := mk("src", "")
	claude := mk(".claude", "")
	wt := mk("tmp/feature-x", "gitdir: /repo/.git/worktrees/feature-x\n")
	sub := mk("third_party/lib", "gitdir: ../../.git/modules/lib\n")
	latticeDir := mk("lattice", "")

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"root is never skipped", root, false},
		{"plain source dir", src, false},
		{".claude agent state", claude, true},
		{"linked git worktree", wt, true},
		{"submodule is still scanned", sub, false},
		{"lattice dir", latticeDir, true},
	}
	for _, c := range cases {
		if got := skipDir(root, c.dir, filepath.Base(c.dir), latticeDir); got != c.want {
			t.Errorf("%s: skipDir(%q) = %v, want %v", c.name, c.dir, got, c.want)
		}
	}
}

// @verifies extraction:INV-1
func TestSkipDirRootWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /repo/.git/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if skipDir(root, root, filepath.Base(root), "") {
		t.Fatal("a workspace rooted in a worktree must still be scanned")
	}
}

// @verifies extraction:INV-2
func TestScanFilter(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	write(".gitignore", "out/\n*.gen.ts\n")
	write("pkg/.gitignore", "local.ts\n")
	write("src/app.ts", "")
	write("src/schema.gen.ts", "")
	write("src/forced.gen.ts", "")
	write("out/bundle.ts", "")
	write("pkg/local.ts", "")
	write("pkg/shared.ts", "")
	write("legacy/old.ts", "")
	git("add", "-f", "src/forced.gen.ts") // tracked despite the pattern

	f := newScanFilter(root, []string{"legacy/**"})
	for rel, want := range map[string]bool{
		"src/app.ts":        false,
		"src/schema.gen.ts": true,  // root .gitignore
		"src/forced.gen.ts": false, // tracked files are always scanned
		"pkg/local.ts":      true,  // nested .gitignore
		"pkg/shared.ts":     false,
		"legacy/old.ts":     true, // workspace exclude glob
	} {
		if got := f.skipFile(rel); got != want {
			t.Errorf("skipFile(%q) = %v, want %v", rel, got, want)
		}
	}
	if !f.skipDir("out") || f.skipDir("src") {
		t.Errorf("skipDir: out=%v src=%v, want true false", f.skipDir("out"), f.skipDir("src"))
	}

	// Outside a git work tree nothing is ignored, but excludes still apply.
	plain := newScanFilter(t.TempDir(), []string{"legacy/**"})
	if plain.skipFile("src/schema.gen.ts") || !plain.skipFile("legacy/old.ts") {
		t.Error("non-git root: only the exclude glob should apply")
	}
}

// Scans answers for one path what the walk decides for the tree: an
// adapter must own it, and no pruned directory or exclude glob may cover it.
// @verifies extraction:INV-1, extraction:INV-2
func TestScans(t *testing.T) {
	root := t.TempDir()
	w := &workspace.Workspace{LatticeDir: filepath.Join(root, "lattice"), Exclude: []string{"legacy/**"}}
	cr := workspace.CodeRoot{Name: "default", Abs: root, Available: true}
	reg := adapters.NewRegistry(golang.New())
	for rel, want := range map[string]bool{
		"pkg/a.go":               true,
		"pkg/a.txt":              false, // no adapter
		"vendor/x/a.go":          false, // pruned directory
		"web/node_modules/a.go":  false,
		"lattice/features/a.go":  false, // the workspace itself
		"legacy/a.go":            false, // exclude glob
		".claude/worktrees/a.go": false,
	} {
		if got := Scans(w, reg, cr, rel); got != want {
			t.Errorf("Scans(%q) = %v, want %v", rel, got, want)
		}
	}
}
