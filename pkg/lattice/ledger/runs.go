package ledger

// @module-feature ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// runsFile is the last-runs cache relative to the lattice/ directory. It is
// under .cache/, which `lattice init` gitignores: a routine check run is not
// a decision, so recording one never dirties a tracked file.
const runsFile = ".cache/last-runs.json"

// RunsSchema names the last-runs cache format.
const RunsSchema = "lattice.last-runs/1"

// LastRuns is the latest run of each routine check (validate, results
// ingest, runs-clean, backprop, sweep), keyed by check name. The file is
// overwritten on every run, never appended.
type LastRuns struct {
	Schema string           `json:"schema"`
	Runs   map[string]Entry `json:"runs"`
}

// RunsPath returns the last-runs cache path for a lattice/ directory.
func RunsPath(latticeDir string) string {
	return filepath.Join(latticeDir, runsFile)
}

// RecordRun stores e as the latest run of check, replacing the previous
// one. It writes the untracked last-runs cache and never the ledger.
// @enforces ledger:INV-3
func RecordRun(latticeDir, check string, e Entry) error {
	if e.Event == "" {
		e.Event = EventCheckRun
	}
	runs, _ := LoadRuns(latticeDir)
	if runs == nil {
		runs = map[string]Entry{}
	}
	runs[check] = e
	data, err := json.MarshalIndent(LastRuns{Schema: RunsSchema, Runs: runs}, "", "  ")
	if err != nil {
		return err
	}
	path := RunsPath(latticeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadRuns reads the last-runs cache. A missing or unreadable cache reads
// as empty: it is a convenience, never evidence.
func LoadRuns(latticeDir string) (map[string]Entry, error) {
	data, err := os.ReadFile(RunsPath(latticeDir))
	if os.IsNotExist(err) {
		return map[string]Entry{}, nil
	}
	if err != nil {
		return map[string]Entry{}, err
	}
	var lr LastRuns
	if err := json.Unmarshal(data, &lr); err != nil {
		return map[string]Entry{}, err
	}
	if lr.Runs == nil {
		lr.Runs = map[string]Entry{}
	}
	return lr.Runs, nil
}
