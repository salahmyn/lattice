package gitstate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Dirty lists modified, deleted and untracked paths relative to the
// checkout's top, sorted, without Lattice's own state; from a subdirectory
// it lists only the paths under it.
// @verifies check:INV-12
func TestDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, ".gitignore", "*.log\n")
	write(t, dir, "svc/a.go", "package svc\n")
	write(t, dir, "svc/b.go", "package svc\n")
	write(t, dir, "svc/specs/config.yaml", "{}\n")
	write(t, dir, "svc/specs/.ledger/ledger.jsonl", "")
	write(t, dir, "tools/t.go", "package tools\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")

	write(t, dir, "svc/a.go", "package svc // edited\n")
	if err := os.Remove(filepath.Join(dir, "svc", "b.go")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "svc/new.go", "package svc\n")
	write(t, dir, "svc/run.log", "ignored\n")
	write(t, dir, "svc/specs/.ledger/ledger.jsonl", "{}\n")
	write(t, dir, "svc/specs/.cache/x.json", "{}\n")
	write(t, dir, "other/lattice/.cache/y.json", "{}\n")
	write(t, dir, "tools/t.go", "package tools // edited\n")

	ctx := context.Background()
	ws := filepath.Join(dir, "svc", "specs")
	got, err := Dirty(ctx, dir, []string{ws})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"svc/a.go", "svc/b.go", "svc/new.go", "tools/t.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dirty(top) = %v, want %v", got, want)
	}
	got, err = Dirty(ctx, filepath.Join(dir, "svc"), []string{ws})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"svc/a.go", "svc/b.go", "svc/new.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dirty(svc) = %v, want %v", got, want)
	}
	// Without the workspace named, only the lattice/ convention is known.
	got, _ = Dirty(ctx, dir, nil)
	if want := []string{"svc/a.go", "svc/b.go", "svc/new.go", "svc/specs/.cache/x.json", "svc/specs/.ledger/ledger.jsonl", "tools/t.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dirty(top, no workspace) = %v, want %v", got, want)
	}
}

func TestAbbrev(t *testing.T) {
	if got := Abbrev([]string{"a", "b"}, 2); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Abbrev at the limit = %v", got)
	}
	in := []string{"a", "b", "c", "d"}
	if got := Abbrev(in, 2); !reflect.DeepEqual(got, []string{"a", "b", "and 2 more"}) {
		t.Errorf("Abbrev = %v", got)
	}
	if !reflect.DeepEqual(in, []string{"a", "b", "c", "d"}) {
		t.Errorf("Abbrev changed its input: %v", in)
	}
}
