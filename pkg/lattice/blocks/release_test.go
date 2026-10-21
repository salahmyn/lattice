package blocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestParseRelease(t *testing.T) {
	r, err := ParseRelease([]byte(`{"repo":"acme/web","version":"v2","commit":"abc","deployedAt":"2026-01-02T00:00:00Z","extra":1}`))
	if err != nil || r.Version != "v2" || r.Commit != "abc" || r.DeployedAt != "2026-01-02T00:00:00Z" || r.Repo != "acme/web" {
		t.Fatalf("release = %+v, %v", r, err)
	}
	for _, bad := range []string{`[]`, `{"repo":"x"}`, `not json`} {
		if _, err := ParseRelease([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

func TestParseSnapshot(t *testing.T) {
	s, err := ParseSnapshot([]byte(`{"repo":"acme/api","branch":"docs","commit":"def","commitDate":"2026-01-01T00:00:00Z","note":"x"}`))
	if err != nil || s.Repo != "acme/api" || s.Branch != "docs" || s.Commit != "def" || s.CommitDate != "2026-01-01T00:00:00Z" || s.State != schema.FreshSnapshot {
		t.Fatalf("snapshot = %+v, %v", s, err)
	}
	if _, err := ParseSnapshot([]byte(`{"repo":"x"}`)); err == nil {
		t.Error("a snapshot without a commit should be an error")
	}
}

func TestFreshness(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q")
	var commits []string
	for i := 0; i < 3; i++ {
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
	head := commits[1]
	cases := []struct {
		rel          *schema.Release
		state        string
		behind, ahea int
	}{
		{nil, schema.FreshNoRelease, 0, 0},
		{&schema.Release{Version: commits[1][:7]}, schema.FreshCurrent, 0, 0},
		{&schema.Release{Version: "v", Commit: commits[0]}, schema.FreshAhead, 0, 1},
		{&schema.Release{Version: "v", Commit: commits[2]}, schema.FreshBehind, 1, 0},
		{&schema.Release{Version: "v", Commit: "0123456789abcdef0123456789abcdef01234567"}, schema.FreshUnknown, 0, 0},
		{&schema.Release{Version: "--all"}, schema.FreshUnknown, 0, 0},
	}
	for i, c := range cases {
		state, behind, ahead := Freshness(repo, head, c.rel)
		if state != c.state || behind != c.behind || ahead != c.ahea {
			t.Errorf("case %d: %s %d %d want %s %d %d", i, state, behind, ahead, c.state, c.behind, c.ahea)
		}
	}
	if r := cases[1].rel; r.Commit != commits[1] {
		t.Errorf("a release resolved from its version should carry the full commit, got %q", r.Commit)
	}
}
