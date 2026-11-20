package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func initJSON(t *testing.T, dir string) initResult {
	t.Helper()
	out, err := run(t, dir, newInitCommand, "--no-wizard")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	var res initResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("init JSON: %v\n%s", err, out)
	}
	return res
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// init ignores the cache and the derived graph: it creates .gitignore when
// there is none, appends only the missing lines to an existing one (the
// user's lines stay byte for byte), and a re-run adds nothing.
// @verifies cli:INV-2
func TestInitGitignore(t *testing.T) {
	t.Run("no gitignore", func(t *testing.T) {
		dir := t.TempDir()
		res := initJSON(t, dir)
		got := readFile(t, filepath.Join(dir, ".gitignore"))
		for _, want := range []string{"\nlattice/.cache/\n", "\nlattice/lattice.json\n"} {
			if !strings.Contains(got, want) {
				t.Errorf(".gitignore lacks %q:\n%s", want, got)
			}
		}
		if !contains(res.Created, ".gitignore") || len(res.GitignoreAdded) != 0 {
			t.Errorf("created=%v added=%v", res.Created, res.GitignoreAdded)
		}
		again := initJSON(t, dir)
		if !again.AlreadyInit || len(again.GitignoreAdded) != 0 || readFile(t, filepath.Join(dir, ".gitignore")) != got {
			t.Errorf("re-run changed .gitignore: %+v", again)
		}
	})

	t.Run("existing gitignore lacking the lines", func(t *testing.T) {
		dir := t.TempDir()
		user := "# mine\nnode_modules/\n/dist" // no trailing newline
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(user), 0o644); err != nil {
			t.Fatal(err)
		}
		res := initJSON(t, dir)
		got := readFile(t, filepath.Join(dir, ".gitignore"))
		if !strings.HasPrefix(got, user+"\n") {
			t.Fatalf("user lines were rewritten:\n%s", got)
		}
		tail := got[len(user):]
		if !strings.Contains(tail, "\n# Lattice") || !strings.HasSuffix(tail, "\nlattice/.cache/\nlattice/lattice.json\n") {
			t.Errorf("appended block:\n%q", tail)
		}
		if want := []string{"lattice/.cache/", "lattice/lattice.json"}; !reflect.DeepEqual(res.GitignoreAdded, want) {
			t.Errorf("added = %v, want %v", res.GitignoreAdded, want)
		}
		if contains(res.Created, ".gitignore") {
			t.Error("an existing .gitignore is not created")
		}
		again := initJSON(t, dir)
		if len(again.GitignoreAdded) != 0 || readFile(t, filepath.Join(dir, ".gitignore")) != got {
			t.Errorf("re-run changed .gitignore: %+v", again)
		}
	})

	t.Run("one line already there in another spelling", func(t *testing.T) {
		dir := t.TempDir()
		user := "/lattice/.cache\n"
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(user), 0o644); err != nil {
			t.Fatal(err)
		}
		res := initJSON(t, dir)
		if want := []string{"lattice/lattice.json"}; !reflect.DeepEqual(res.GitignoreAdded, want) {
			t.Errorf("added = %v, want %v", res.GitignoreAdded, want)
		}
		if got := readFile(t, filepath.Join(dir, ".gitignore")); !strings.HasPrefix(got, user) || strings.Count(got, ".cache") != 1 {
			t.Errorf(".gitignore:\n%s", got)
		}
	})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func trackedCodes(t *testing.T, repo string) []string {
	t.Helper()
	out, _ := run(t, repo, newValidateCommand)
	var rep validateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate JSON: %v\n%s", err, out)
	}
	var codes []string
	for _, v := range rep.Violations {
		if v.Code == "GRAPH_TRACKED" || v.Code == "CACHE_TRACKED" {
			codes = append(codes, v.Code+" "+v.Subject)
		}
	}
	return codes
}

// validate warns when git tracks the derived graph or cache files, and is
// silent once they are untracked and ignored, or outside a repository.
// @verifies validation:INV-13
func TestValidateWarnsTrackedDerived(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := copyTree(t, "../../examples/coverage-prototype")
	if codes := trackedCodes(t, repo); len(codes) != 0 { // not a repository yet
		t.Fatalf("outside git: %v", codes)
	}
	if _, err := os.Stat(filepath.Join(repo, "lattice", "lattice.json")); err != nil {
		t.Fatalf("validate should have written the graph: %v", err)
	}
	cached := filepath.Join(repo, "lattice", ".cache", "results", "x.json")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "add", "-A", "-f")
	runGit(t, repo, "commit", "-q", "-m", "tracks derived files")

	codes := trackedCodes(t, repo)
	want := []string{"CACHE_TRACKED lattice/.cache/", "GRAPH_TRACKED lattice/lattice.json"}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("tracked: %v, want %v", codes, want)
	}

	runGit(t, repo, "rm", "-q", "-r", "--cached", "lattice/lattice.json", "lattice/.cache")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("lattice/.cache/\nlattice/lattice.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if codes := trackedCodes(t, repo); len(codes) != 0 {
		t.Errorf("ignored: %v", codes)
	}
}
