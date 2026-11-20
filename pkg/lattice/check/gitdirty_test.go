package check

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// GitDirty counts untracked source files git does not ignore, never the
// workspace's own .ledger/ and .cache/ (here a workspace not named lattice),
// and names the paths sorted, ten at most and then how many more.
// @verifies check:INV-12
func TestGitDirtyOneRule(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	ws := filepath.Join(dir, "spec")
	gitIn(t, dir, "init", "-q")
	put(t, dir, ".gitignore", "spec/lattice.json\nbuild/\n")
	put(t, dir, "spec/config.yaml", "{}\n")
	put(t, dir, "spec/.ledger/ledger.jsonl", "")
	put(t, dir, "spec/.cache/audit/old.json", "{}\n")
	put(t, dir, "fees/fees.go", "package fees\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")

	if d, files := GitDirty(dir, ws); d || len(files) != 0 {
		t.Fatalf("a clean tree reads dirty: %v", files)
	}

	put(t, dir, "spec/.ledger/ledger.jsonl", "{}\n")
	put(t, dir, "spec/.cache/audit/old.json", "{\"changed\":true}\n")
	put(t, dir, "spec/.cache/results/results.json", "{}\n")
	put(t, dir, "build/out.bin", "ignored")
	if d, files := GitDirty(dir, ws); d {
		t.Errorf("Lattice's own state or an ignored file read as dirty: %v", files)
	}

	gitIn(t, dir, "config", "status.showUntrackedFiles", "no")
	put(t, dir, "fees/extra.go", "package fees\n")
	if d, files := GitDirty(dir, ws); !d || !reflect.DeepEqual(files, []string{"fees/extra.go"}) {
		t.Errorf("an untracked source file: dirty %v, files %v", d, files)
	}

	for i := 0; i < 12; i++ {
		put(t, dir, fmt.Sprintf("fees/gen%02d.go", i), "package fees\n")
	}
	_, files := GitDirty(dir, ws)
	if len(files) != 11 || files[0] != "fees/extra.go" || files[1] != "fees/gen00.go" || files[10] != "and 3 more" {
		t.Errorf("files = %v, want ten sorted paths and \"and 3 more\"", files)
	}
}
