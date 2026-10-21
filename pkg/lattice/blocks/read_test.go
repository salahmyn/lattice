package blocks

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func TestReadGitReadsTheRefNotTheWorkingTree(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "docs", "flows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "flows", "a.json"), []byte(`{"v":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-q", "-m", "one")
	// an uncommitted edit must not be read
	if err := os.WriteFile(filepath.Join(repo, "docs", "flows", "a.json"), []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	files, commit, date, err := ReadGit(repo, "HEAD", "docs/flows/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "docs/flows/a.json" || string(files[0].Data) != `{"v":1}` {
		t.Fatalf("files = %+v", files)
	}
	if len(commit) != 40 || date == "" {
		t.Errorf("commit=%q date=%q", commit, date)
	}
	if _, _, _, err := ReadGit(repo, "no-such-ref", "*.json"); err == nil {
		t.Error("unknown ref should fail")
	}
}

func TestReadDir(t *testing.T) {
	files, err := ReadDir(filepath.Join("testdata", "shop", "frontend"), "*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].Path != "cart.json" || files[2].Path != "pay.json" {
		t.Fatalf("files = %v", files)
	}
}
