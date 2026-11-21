package audit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// A stamp of another commit matches head when that commit has head's tree
// apart from Lattice's own ledger and cache; coverage only when it was
// ingested from a clean tree. Without that fact it does not match.
// @verifies audit:INV-9
func TestMatchesHeadSameTree(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	stamps := Inputs{
		Results:  ResultsInput{Present: true, Commit: "older"},
		Coverage: CoverageInput{Present: true, Commit: "older"},
	}
	for _, tc := range []struct {
		name                    string
		results, coverage, dirt bool
		wantRes, wantCov        bool
	}{
		{"unknown tree", false, false, false, false, false},
		{"same tree", true, true, false, true, true},
		{"same tree, dirty coverage", true, true, true, true, false},
	} {
		st := stamps
		st.Coverage.Dirty = tc.dirt
		rep := Compute(Sources{KG: kg, Results: pass("f.TestOne"), Stamps: st,
			ResultsSameTree: tc.results, CoverageSameTree: tc.coverage})
		if rep.Inputs.Results.MatchesHead != tc.wantRes || rep.Inputs.Coverage.MatchesHead != tc.wantCov {
			t.Errorf("%s: matches_head results=%v coverage=%v, want %v %v", tc.name,
				rep.Inputs.Results.MatchesHead, rep.Inputs.Coverage.MatchesHead, tc.wantRes, tc.wantCov)
		}
	}
}

// Without a same-tree fact, a stamp matches head only when the two agree or
// either is unknown; a stamp of another commit does not.
// @verifies audit:INV-9
func TestMatchesHeadByStamp(t *testing.T) {
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	for _, tc := range []struct {
		name, stamp, head string
		want             bool
	}{
		{"same commit", "abc123", "abc123", true},
		{"unstamped results", "", "abc123", true},
		{"graph with no commit", "older", "", true},
		{"another commit", "older", "abc123", false},
	} {
		g := kg
		g.GeneratedFromCommit = tc.head
		rep := Compute(Sources{KG: g, Results: pass("f.TestOne"), Stamps: Inputs{
			Results:  ResultsInput{Present: true, Commit: tc.stamp},
			Coverage: CoverageInput{Present: true, Commit: tc.stamp},
		}})
		if rep.Inputs.Results.MatchesHead != tc.want || rep.Inputs.Coverage.MatchesHead != tc.want {
			t.Errorf("%s: matches_head results=%v coverage=%v, want %v", tc.name,
				rep.Inputs.Results.MatchesHead, rep.Inputs.Coverage.MatchesHead, tc.want)
		}
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Collect reads from git whether the results' and coverage's commit has
// head's tree apart from Lattice's own state: an empty commit keeps them
// matching head, a code change does not.
// @verifies audit:INV-9
func TestCollectSameTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	latticeDir := filepath.Join(repo, "lattice")
	if err := os.MkdirAll(filepath.Join(latticeDir, ".cache", "results"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{".gitignore": "lattice/.cache/\n", "src/fees.go": "package src\n"} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	base := gitIn(t, repo, "rev-parse", "HEAD")

	set := pass("f.TestOne")
	set.Commit = base
	if err := results.Save(latticeDir, set); err != nil {
		t.Fatal(err)
	}
	writeCoverage := func(dirty bool) {
		t.Helper()
		data, _ := json.Marshal(coverage.Set{Commit: base, Dirty: dirty})
		if err := os.WriteFile(filepath.Join(latticeDir, ".cache", "results", "coverage.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCoverage(false)
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})

	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "empty")
	kg.GeneratedFromCommit = gitIn(t, repo, "rev-parse", "HEAD")
	in := Compute(Collect(latticeDir, kg, false)).Inputs
	if in.Results.Commit != base || !in.Results.MatchesHead || !in.Coverage.MatchesHead {
		t.Errorf("empty commit: inputs = %+v, want results and coverage matching head", in)
	}
	writeCoverage(true)
	if in := Compute(Collect(latticeDir, kg, false)).Inputs; !in.Results.MatchesHead || in.Coverage.MatchesHead {
		t.Errorf("dirty coverage: inputs = %+v, want only results matching head", in)
	}
	writeCoverage(false)

	if err := os.WriteFile(filepath.Join(repo, "src", "fees.go"), []byte("package src // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "commit", "-q", "-am", "code")
	kg.GeneratedFromCommit = gitIn(t, repo, "rev-parse", "HEAD")
	if in := Compute(Collect(latticeDir, kg, false)).Inputs; in.Results.MatchesHead || in.Coverage.MatchesHead {
		t.Errorf("code commit: inputs = %+v, want neither matching head", in)
	}
}
