package impact

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Uncommitted reads the working tree by the one rule check and break read:
// an untracked file git does not ignore makes it true, a report file in the
// checkout's root included; Lattice's own .ledger/ and .cache/ never do,
// whatever the workspace directory is called, and the changed files leave
// them out too.
// @verifies check:INV-12
func TestUncommittedIsOneRule(t *testing.T) {
	for _, wsDir := range []string{"lattice", "spec"} {
		t.Run(wsDir, func(t *testing.T) {
			r := newRepo(t)
			if wsDir != "lattice" {
				if err := os.Rename(filepath.Join(r.dir, "lattice"), filepath.Join(r.dir, wsDir)); err != nil {
					t.Fatal(err)
				}
			}
			r.write(".gitignore", wsDir+"/lattice.json\n") // the cache is not ignored
			r.write(wsDir+"/.ledger/ledger.jsonl", "")
			r.write("fees/fees.go", simpleGo("fees", "Charge", "fees:INV-1"))
			r.commit("base")

			uncommitted := func() (bool, []string) {
				t.Helper()
				ws, err := workspace.Open(filepath.Join(r.dir, wsDir))
				if err != nil {
					t.Fatal(err)
				}
				adCfg, err := config.LoadAdapters(ws.LatticeDir)
				if err != nil {
					t.Fatal(err)
				}
				rep, err := Compute(context.Background(), Input{Workspace: ws, Registry: all.Registry(adCfg), Since: "HEAD"})
				if err != nil {
					t.Fatal(err)
				}
				var paths []string
				for _, f := range rep.ChangedFiles {
					paths = append(paths, f.path())
				}
				return rep.Uncommitted, paths
			}
			if u, files := uncommitted(); u || len(files) != 0 {
				t.Fatalf("a clean tree: uncommitted %v, changed %v", u, files)
			}

			r.write(wsDir+"/.ledger/ledger.jsonl", "{\"event\":\"x\"}\n")
			r.write(wsDir+"/.cache/runs/last.json", "{}\n")
			if u, _ := uncommitted(); u {
				t.Error("Lattice's own .ledger/ and .cache/ made the tree uncommitted")
			}
			if _, files := uncommitted(); len(files) != 1 || files[0] != wsDir+"/.ledger/ledger.jsonl" {
				// The tracked ledger differs from base, so the diff lists it;
				// an untracked cache file is not part of the change.
				t.Errorf("changed files = %v, want only the tracked ledger", files)
			}

			r.write("lattice-check.md", "")
			if u, _ := uncommitted(); !u {
				t.Error("an untracked report file in the root must make the tree uncommitted")
			}
			if err := os.Remove(filepath.Join(r.dir, "lattice-check.md")); err != nil {
				t.Fatal(err)
			}

			r.git("config", "status.showUntrackedFiles", "no")
			r.write("fees/extra.go", simpleGo("fees", "Extra", "fees:INV-2"))
			if u, _ := uncommitted(); !u {
				t.Error("an untracked source file must make the tree uncommitted")
			}
		})
	}
}
