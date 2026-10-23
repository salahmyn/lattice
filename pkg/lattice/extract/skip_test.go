package extract

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestSkipDirRootWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /repo/.git/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if skipDir(root, root, filepath.Base(root), "") {
		t.Fatal("a workspace rooted in a worktree must still be scanned")
	}
}
