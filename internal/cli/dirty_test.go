package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// treeState is what the read commands say about the working tree: audit's
// dirty flag (check.GitDirty) and check's head, which is impact's
// uncommitted flag.
type treeState struct{ audit, check bool }

func readTreeState(t *testing.T, ws string) treeState {
	t.Helper()
	out, err := run(t, ws, newAuditCommand)
	if err != nil {
		t.Fatalf("audit: %v\n%s", err, out)
	}
	var a struct {
		Dirty bool `json:"dirty"`
	}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("audit output: %v\n%s", err, out)
	}
	// check exits 1 for a verdict that may not merge; the report is printed
	// all the same.
	out, _ = run(t, ws, newCheckCommand, "--base", "HEAD")
	var c struct {
		Head struct {
			Dirty bool `json:"dirty"`
		} `json:"head"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("check output: %v\n%s", err, out)
	}
	return treeState{audit: a.Dirty, check: c.Head.Dirty}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// audit's dirty flag and check's +uncommitted read the working tree by one
// rule, wherever the workspace sits and whether or not its cache is ignored:
// Lattice's own .ledger/ and .cache/ never make it dirty, an untracked
// source file or report file that git does not ignore always does.
// @verifies check:INV-12
func TestDirtyIsOneRule(t *testing.T) {
	for name, layout := range map[string]struct{ ws, gitignore string }{
		"default layout":                {"lattice", "lattice/.cache/\nlattice/lattice.json\n"},
		"cache not ignored":             {"lattice", "lattice/lattice.json\n"},
		"workspace not named lattice":   {"specs", "specs/.cache/\nspecs/lattice.json\n"},
		"other name, cache not ignored": {"specs", "specs/lattice.json\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, ws := ingestFixture(t, layout.ws, layout.gitignore)
			if got := readTreeState(t, ws); got.audit || got.check {
				t.Fatalf("a clean checkout reads dirty: %+v", got)
			}

			// Lattice's own state: the tracked ledger edited, cache files
			// written (the read commands above write some of their own).
			writeFile(t, filepath.Join(ws, ".ledger", "ledger.jsonl"), "{\"event\":\"x\"}\n")
			writeFile(t, filepath.Join(ws, ".cache", "runs", "last.json"), "{}\n")
			if got := readTreeState(t, ws); got.audit || got.check {
				t.Errorf("Lattice's own .ledger/ and .cache/ read as dirty: %+v", got)
			}

			// A report written into the checkout before lattice runs, as a
			// shell redirect does.
			report := filepath.Join(dir, "report.md")
			writeFile(t, report, "")
			if got := readTreeState(t, ws); !got.audit || !got.check {
				t.Errorf("an untracked report file must read dirty in both: %+v", got)
			}
			if err := os.Remove(report); err != nil {
				t.Fatal(err)
			}

			// An untracked source file: extraction and the tests see it.
			runGit(t, dir, "config", "status.showUntrackedFiles", "no")
			writeFile(t, filepath.Join(dir, "guard", "extra.go"), "package guard\n\nfunc extra() int { return 1 }\n")
			if got := readTreeState(t, ws); !got.audit || !got.check {
				t.Errorf("an untracked source file must read dirty in both: %+v", got)
			}
		})
	}
}

// The coverage reports an ingest reads are its output, not code: they never
// make the ingest dirty, while an untracked source file does.
// @verifies check:INV-12
func TestCoverageIngestDirtyLeavesOutItsReports(t *testing.T) {
	dir, ws := ingestFixture(t, "lattice", "lattice/.cache/\nlattice/lattice.json\n")
	lcov := filepath.Join(dir, "cover", "all.lcov")
	writeFile(t, lcov, "TN:\nSF:"+filepath.Join(dir, "guard", "guard.go")+"\nDA:1,1\nend_of_record\n")
	ingest := func() (bool, []string) {
		t.Helper()
		files, err := os.ReadDir(filepath.Join(ws, ".cache"))
		if err == nil {
			for _, f := range files {
				_ = os.RemoveAll(filepath.Join(ws, ".cache", f.Name()))
			}
		}
		if out, err := run(t, ws, newResultsCommand, "ingest", lcov); err != nil {
			t.Fatalf("ingest: %v\n%s", err, out)
		}
		data, err := os.ReadFile(filepath.Join(ws, ".cache", "results", "coverage.json"))
		if err != nil {
			t.Fatal(err)
		}
		var set struct {
			Dirty      bool     `json:"dirty"`
			DirtyFiles []string `json:"dirty_files"`
		}
		if err := json.Unmarshal(data, &set); err != nil {
			t.Fatal(err)
		}
		return set.Dirty, set.DirtyFiles
	}
	if dirty, files := ingest(); dirty {
		t.Errorf("the ingested report itself made the tree dirty: %v", files)
	}
	writeFile(t, filepath.Join(dir, "guard", "extra.go"), "package guard\n\nfunc extra() int { return 1 }\n")
	if dirty, files := ingest(); !dirty || len(files) != 1 || files[0] != "guard/extra.go" {
		t.Errorf("an untracked source file must be dirty, alone: %v %v", dirty, files)
	}
}
