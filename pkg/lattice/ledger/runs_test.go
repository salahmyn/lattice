package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

// The last-runs cache keeps one entry per check, overwritten in place, and
// a missing cache reads as empty.
// @verifies ledger:INV-3
func TestRecordRunOverwrites(t *testing.T) {
	dir := t.TempDir()
	if runs, err := LoadRuns(dir); err != nil || len(runs) != 0 {
		t.Fatalf("missing cache: %v %v", runs, err)
	}
	for _, ev := range []string{"first", "second"} {
		if err := RecordRun(dir, "validate", Entry{Unit: "workspace", Evidence: ev}); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordRun(dir, "sweep", Entry{Unit: "workspace", Evidence: "s"}); err != nil {
		t.Fatal(err)
	}
	runs, err := LoadRuns(dir)
	if err != nil || len(runs) != 2 || runs["validate"].Evidence != "second" || runs["validate"].Event != EventCheckRun {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ledger", "jsonl")); !os.IsNotExist(err) {
		t.Errorf("RecordRun must not create the ledger: %v", err)
	}
}
