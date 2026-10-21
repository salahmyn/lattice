package blocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestCheckPin(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q", "-b", "main")
	var commits []string
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(filepath.Join(repo, "f"), []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, repo, "git", "add", ".")
		run(t, repo, "git", "commit", "-q", "-m", "c")
		out, err := runGit(repo, "rev-parse", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, strings.TrimSpace(string(out)))
	}
	run(t, repo, "git", "branch", "side", commits[0])
	run(t, repo, "git", "checkout", "-q", "side")
	if err := os.WriteFile(filepath.Join(repo, "g"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-q", "-m", "side")
	cases := []struct {
		pin   schema.Pin
		clone string
		state string
		moved int
	}{
		{schema.Pin{Role: "kit", Ref: "main", Commit: commits[0]}, repo, "main moved 1 commit past the pin", 1},
		{schema.Pin{Role: "kit", Ref: "main", Commit: commits[1]}, repo, "at the pin", 0},
		{schema.Pin{Role: "kit", Commit: commits[0]}, repo, "at the pin", 0},
		{schema.Pin{Role: "kit", Ref: "main", Commit: "0123456789abcdef0123456789abcdef01234567"}, repo, "commit missing", 0},
		{schema.Pin{Role: "kit", Ref: "main", Commit: commits[0]}, filepath.Join(repo, "nope"), "not checked", 0},
		{schema.Pin{Role: "kit", Ref: "main"}, repo, "not checked", 0},
		{schema.Pin{Role: "kit", Ref: "--all", Commit: commits[0]}, repo, "not checked", 0},
		{schema.Pin{Role: "kit", Ref: "gone", Commit: commits[0]}, repo, "gone missing", 0},
		{schema.Pin{Role: "kit", Ref: "side", Commit: commits[1]}, repo, "side no longer contains the pin", 0},
	}
	for i, c := range cases {
		got := CheckPin(c.pin, c.clone)
		if got.State != c.state || got.Moved != c.moved {
			t.Errorf("case %d: %+v", i, got)
		}
	}
	if got := CheckPin(cases[0].pin, repo); got.Tip != commits[1] || got.CommitDate == "" || got.Pushed {
		t.Errorf("moved pin = %+v", got)
	}
}
