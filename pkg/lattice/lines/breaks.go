package lines

// @module-feature audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// breaksSchema is the tag of breaks.json, which the lines read as a file, as
// the audit does, and never import the package that writes it.
const breaksSchema = "lattice.breaks/1"

// LoadBreaks reads the runs of lattice/.cache/results/breaks.json that
// describe lines. why says why none can be used: the file is of another
// schema or another commit than head, or was made on a dirty tree, since
// the lines of a break mean nothing on another tree. "" with no runs means
// there is no file.
func LoadBreaks(latticeDir, head string) (runs []BreakRun, why string) {
	data, err := os.ReadFile(filepath.Join(latticeDir, ".cache", "results", "breaks.json"))
	if err != nil {
		return nil, ""
	}
	var f struct {
		Schema string     `json:"schema"`
		Commit string     `json:"commit"`
		Dirty  bool       `json:"dirty"`
		Runs   []BreakRun `json:"runs"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "breaks.json: " + err.Error()
	}
	switch {
	case f.Schema != breaksSchema:
		return nil, fmt.Sprintf("breaks.json schema %q is not %s", f.Schema, breaksSchema)
	case f.Dirty:
		return nil, "breaks.json was made on a dirty tree"
	case f.Commit == "" || head == "" || f.Commit != head:
		return nil, fmt.Sprintf("breaks.json is for commit %s, the graph is at %s", short(f.Commit), short(head))
	}
	return f.Runs, ""
}
