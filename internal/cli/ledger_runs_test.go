package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/ledger"
)

// Routine check runs never touch the tracked ledger: validate and results
// ingest leave ledger.jsonl byte-identical (old check-run lines included)
// and record their outcome in the untracked last-runs cache, which
// `lattice ledger runs` reads. A flag raise is a state change and is still
// appended.
// @verifies ledger:INV-3
func TestCheckRunsSkipTrackedLedger(t *testing.T) {
	repo := copyTree(t, "../../examples/coverage-prototype")
	ledgerPath := filepath.Join(repo, "lattice", ".ledger", "ledger.jsonl")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := []byte(`{"at":"2026-01-01T00:00:00Z","event":"gate","actor":"owner@example.com","unit":"brd.orders","evidence":"approved"}` + "\n" +
		`{"at":"2026-01-02T00:00:00Z","event":"check-run","actor":"ci","unit":"workspace","evidence":"validate: 0 error(s), 0 warning(s), 0 info, 0 allowlisted"}` + "\n")
	if err := os.WriteFile(ledgerPath, seed, 0o644); err != nil {
		t.Fatal(err)
	}

	abs := func(p string) string { return filepath.Join(repo, p) }
	if out, err := run(t, repo, newResultsCommand, "ingest", abs("coverage/junit.xml")); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	_, _ = run(t, repo, newValidateCommand) // the example fails require_level on purpose
	_, _ = run(t, repo, newValidateCommand) // a second run must not append either

	got, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, seed) {
		t.Fatalf("validate/ingest changed the tracked ledger:\n%s", got)
	}

	// Old check-run lines still read back.
	entries, err := ledger.Load(filepath.Join(repo, "lattice"))
	if err != nil || len(entries) != 2 || entries[1].Kind() != ledger.EventCheckRun {
		t.Fatalf("old ledger should still load: %v %+v", err, entries)
	}

	// The reader moved to the cache: ledger runs shows both checks.
	out, err := run(t, repo, newLedgerCommand, "runs")
	if err != nil {
		t.Fatalf("ledger runs: %v\n%s", err, out)
	}
	var lr ledger.LastRuns
	if err := json.Unmarshal([]byte(out), &lr); err != nil {
		t.Fatalf("ledger runs JSON: %v\n%s", err, out)
	}
	if lr.Schema != ledger.RunsSchema {
		t.Errorf("schema = %q", lr.Schema)
	}
	v, ok := lr.Runs["validate"]
	if !ok || !strings.HasPrefix(v.Evidence, "validate: ") || v.Actor != "test" || v.Event != ledger.EventCheckRun {
		t.Errorf("validate run not cached: %+v", lr.Runs)
	}
	if r, ok := lr.Runs["results-ingest"]; !ok || !strings.HasPrefix(r.Evidence, "results ingest: ") {
		t.Errorf("ingest run not cached: %+v", lr.Runs)
	}
	if len(lr.Runs) != 2 {
		t.Errorf("want one entry per check, got %d: %+v", len(lr.Runs), lr.Runs)
	}

	// A flag is a state change: it is still ledgered.
	if out, err := run(t, repo, newFlagCommand, "raise", "brd.orders/SC-1", "--reason", "wording drifted"); err != nil {
		t.Fatalf("flag raise: %v\n%s", err, out)
	}
	got, _ = os.ReadFile(ledgerPath)
	if !bytes.HasPrefix(got, seed) || bytes.Count(got, []byte("\n")) != 3 ||
		!bytes.Contains(got[len(seed):], []byte(`"event":"flag"`)) {
		t.Errorf("flag raise should append one flag event:\n%s", got)
	}
}
