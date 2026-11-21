package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// cacheRunner exits 125 (the break could not be applied) while marker.txt
// says "broken", exits 1 while the flag file named by $1 exists (a down
// environment), and otherwise fails only once the guard is broken.
const cacheRunner = `flag=$1; shift
if grep -q broken marker.txt; then echo 'cannot apply: the marker is broken'; exit 125; fi
if [ -e "$flag" ]; then echo 'FAIL environment is down'; exit 1; fi
if grep -q 'return undefined' src/guard.js; then echo 'FAIL positive rejects zero'; exit 1; fi
`

// cacheFixture is a committed repo whose two promises run under one fake
// whole-file runner. inputs are the runner's declared inputs, none when
// empty. The flag file, once created, makes every baseline fail.
func cacheFixture(t *testing.T, inputs ...string) (dir, flag string) {
	t.Helper()
	for _, tool := range []string{"git", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir, flag = t.TempDir(), filepath.Join(t.TempDir(), "down")
	cfg := "break:\n  runners:\n    - name: file-runner\n      match: [\"tests/**\"]\n      command: [\"sh\", \"run.sh\", \"" + flag + "\", \"{files}\"]\n"
	if len(inputs) > 0 {
		cfg += "      inputs: [\"" + strings.Join(inputs, "\", \"") + "\"]\n"
	}
	files := map[string]string{
		".gitignore":                   "lattice/.cache/\nlattice/lattice.json\n",
		"package.json":                 "{\"type\":\"module\"}\n",
		"src/guard.js":                 sharedGuard,
		"tests/guard.test.js":          sharedTests,
		"run.sh":                       cacheRunner,
		"marker.txt":                   "fine\n",
		"data/seed.sql":                "select 1;\n",
		"docs/note.txt":                "one\n",
		"lattice/config.yaml":          cfg,
		"lattice/features/orders.yaml": sharedFeature,
	}
	for name, body := range files {
		writeFixtureFile(t, dir, name, body)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fixture")
	return dir, flag
}

func writeFixtureFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitFile commits a change to one file.
func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	writeFixtureFile(t, dir, name, body)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "change "+name)
}

// breakRuns breaks the fixture and returns its two promises' runs.
func breakRuns(t *testing.T, dir string, args ...string) map[string]breaktest.Run {
	t.Helper()
	res := runBreakExit(t, dir, true, args...)
	if res.code != 0 {
		t.Fatalf("break %v: exit %d\n%s%s", args, res.code, res.out, res.err)
	}
	return byPromise(res.doc.Report)
}

var cachePromises = []string{"orders:INV-1", "orders:INV-2"}

func wantFresh(t *testing.T, runs map[string]breaktest.Run, outcome string) {
	t.Helper()
	for _, p := range cachePromises {
		if r := runs[p]; r.Outcome != outcome || r.Cached {
			t.Errorf("%s: want a fresh %s run, got %+v", p, outcome, r)
		}
	}
}

func wantCached(t *testing.T, runs map[string]breaktest.Run) {
	t.Helper()
	for _, p := range cachePromises {
		if r := runs[p]; r.Outcome != breaktest.Killed || !r.Cached {
			t.Errorf("%s: want killed (cached), got %+v", p, r)
		}
	}
}

// A commit that changes only a file the runner reads, outside the enforcer
// and the verifiers, can stop every baseline from running. The kill measured
// on the earlier tree must not be reused on it.
// @verifies breaktest:INV-16
func TestBreakCacheIsNotReusedOnAnotherTree(t *testing.T) {
	dir, _ := cacheFixture(t)
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)

	commitFile(t, dir, "marker.txt", "broken\n")
	runs := breakRuns(t, dir)
	for _, p := range cachePromises {
		r := runs[p]
		if r.Cached || r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "did not apply (runner exit 125)") {
			t.Errorf("%s: want unknown with the exit 125 reason, got %+v", p, r)
		}
	}
}

// The same commit is the same tree: its outcomes are reused.
// @verifies breaktest:INV-16
func TestBreakCacheIsReusedOnTheSameTree(t *testing.T) {
	dir, _ := cacheFixture(t)
	first := breakRuns(t, dir)
	wantFresh(t, first, breaktest.Killed)
	wantCached(t, breakRuns(t, dir))
	if h := first["orders:INV-1"].InputsHash; !strings.HasPrefix(h, "tree:") {
		t.Errorf("inputs_hash = %q, want the tree's", h)
	}

	// An empty commit has the same tree and reuses too.
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "empty")
	wantCached(t, breakRuns(t, dir))
}

// With every runner declaring inputs, a commit that changes none of them
// reuses the outcomes, and one that changes an input, the enforcer or a
// verifier does not.
// @verifies breaktest:INV-16
func TestBreakCacheWithDeclaredInputs(t *testing.T) {
	dir, _ := cacheFixture(t, "data/**")
	first := breakRuns(t, dir)
	wantFresh(t, first, breaktest.Killed)
	if h := first["orders:INV-1"].InputsHash; !strings.HasPrefix(h, "inputs:") {
		t.Errorf("inputs_hash = %q, want one over the declared inputs", h)
	}

	commitFile(t, dir, "docs/note.txt", "two\n")
	wantCached(t, breakRuns(t, dir))

	commitFile(t, dir, "data/seed.sql", "select 2;\n")
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)
	wantCached(t, breakRuns(t, dir))

	// The enforcer's and the verifiers' files are inputs too, even when the
	// change is outside the span their hashes cover.
	commitFile(t, dir, "src/guard.js", sharedGuard+"\nexport const unrelated = 1;\n")
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)
	commitFile(t, dir, "tests/guard.test.js", sharedTests+"\nexport const unrelated = 1;\n")
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)
}

// A declared glob that is not valid is not trusted: the tree is the key.
// @verifies breaktest:INV-16
func TestBreakCacheFallsBackToTheTreeOnABadGlob(t *testing.T) {
	dir, _ := cacheFixture(t, "data/[**")
	first := breakRuns(t, dir)
	wantFresh(t, first, breaktest.Killed)
	if h := first["orders:INV-1"].InputsHash; !strings.HasPrefix(h, "tree:") {
		t.Errorf("inputs_hash = %q, want the tree's", h)
	}
	commitFile(t, dir, "docs/note.txt", "two\n")
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)
}

// An outcome from a report written before inputs_hash existed is never
// reused: it names no tree.
// @verifies breaktest:INV-16
func TestBreakCacheIgnoresOldFormatEntries(t *testing.T) {
	dir, _ := cacheFixture(t)
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)
	lat := filepath.Join(dir, "lattice")
	rep := breaktest.Read(lat)
	if len(rep.Runs) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	for i := range rep.Runs {
		rep.Runs[i].InputsHash = ""
	}
	if err := breaktest.Write(lat, rep); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(breaktest.Path(lat)); strings.Contains(string(data), "inputs_hash") {
		t.Fatalf("the old-format report still has the field:\n%s", data)
	}
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)
}

// A cached kill is not reused over a baseline that failed at this tree, even
// when the declared inputs say nothing changed; running the baseline again
// after the environment is back reads the kill fresh.
// @verifies breaktest:INV-16
func TestBreakCacheIsNotReusedOverAFailedBaseline(t *testing.T) {
	dir, flag := cacheFixture(t, "data/**")
	lat := filepath.Join(dir, "lattice")
	wantFresh(t, breakRuns(t, dir), breaktest.Killed)
	saved := breaktest.Read(lat)
	restore := func(drop ...string) {
		t.Helper()
		rep := saved
		rep.Runs = nil
		for _, r := range saved.Runs {
			keep := true
			for _, d := range drop {
				keep = keep && r.Promise != d
			}
			if keep {
				rep.Runs = append(rep.Runs, r)
			}
		}
		if err := breaktest.Write(lat, rep); err != nil {
			t.Fatal(err)
		}
	}

	// A commit outside the inputs; the environment is down.
	commitFile(t, dir, "docs/note.txt", "two\n")
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Within one run: the first promise has no cached outcome, its baseline
	// fails, and the second promise's cached kill is not taken over it.
	restore("orders:INV-1")
	runs := breakRuns(t, dir)
	for _, p := range cachePromises {
		if r := runs[p]; r.Cached || r.Outcome != breaktest.Unknown || r.Baseline != breaktest.BaselineFail {
			t.Errorf("same run, %s: want unknown with a failed baseline, got %+v", p, r)
		}
	}

	// Across runs: the failed baseline is kept for the tree.
	restore()
	runs = breakRuns(t, dir)
	for _, p := range cachePromises {
		if r := runs[p]; r.Cached || r.Outcome != breaktest.Unknown || r.Baseline != breaktest.BaselineFail {
			t.Errorf("later run, %s: want unknown with a failed baseline, got %+v", p, r)
		}
	}

	// The environment is back: the first promise's baseline runs again and
	// passes, so the kill is measured, not reused; that clears the failure,
	// and the second promise, which shares the invocation, reuses its own.
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	restore()
	runs = breakRuns(t, dir)
	if r := runs["orders:INV-1"]; r.Cached || r.Outcome != breaktest.Killed {
		t.Errorf("recovered, INV-1: want a fresh kill, got %+v", r)
	}
	if r := runs["orders:INV-2"]; !r.Cached || r.Outcome != breaktest.Killed {
		t.Errorf("recovered, INV-2: want its cached kill, got %+v", r)
	}
	wantCached(t, breakRuns(t, dir))
}
