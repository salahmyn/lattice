package audit

// @module-feature audit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/gitstate"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Note verdicts of a second reader.
const (
	VerdictConfirmedFake = "confirmed_fake"
	VerdictNotFake       = "not_fake"
)

// notesFile is the optional second-reader notes, relative to lattice/.
const notesFile = "audit-notes.yaml"

// Note is a second reader's verdict on a row. It is shown on the row and
// never changes the computed class.
type Note struct {
	Promise string `yaml:"promise" json:"promise"`
	Reader  string `yaml:"reader" json:"reader"`
	Verdict string `yaml:"verdict" json:"verdict"`
	Note    string `yaml:"note,omitempty" json:"note,omitempty"`
}

// LoadNotes reads lattice/audit-notes.yaml: a list of notes, or a map with a
// `notes:` list. Entries with no promise or an unknown verdict are dropped
// and reported in errs.
func LoadNotes(latticeDir string) (notes []Note, present bool, errs []string) {
	data, err := os.ReadFile(filepath.Join(latticeDir, notesFile))
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, err.Error())
		}
		return nil, !os.IsNotExist(err), errs
	}
	var raw []Note
	if err := yaml.Unmarshal(data, &raw); err != nil {
		var wrapped struct {
			Notes []Note `yaml:"notes"`
		}
		if err2 := yaml.Unmarshal(data, &wrapped); err2 != nil {
			return nil, true, []string{fmt.Sprintf("%s: %v", notesFile, err)}
		}
		raw = wrapped.Notes
	}
	for i, n := range raw {
		n.Promise = strings.TrimSpace(n.Promise)
		switch {
		case n.Promise == "":
			errs = append(errs, fmt.Sprintf("%s entry %d has no promise", notesFile, i+1))
		case n.Verdict != VerdictConfirmedFake && n.Verdict != VerdictNotFake:
			errs = append(errs, fmt.Sprintf("%s: %s has verdict %q, want %s or %s",
				notesFile, n.Promise, n.Verdict, VerdictConfirmedFake, VerdictNotFake))
		default:
			notes = append(notes, n)
		}
	}
	return notes, true, errs
}

// Collect gathers the audit's sources from a workspace's lattice/ directory:
// ingested results, coverage, breaks.json and the notes. Missing evidence is
// not an error; it reads unknown. dirty is the working tree's state, which
// the caller knows. The one git read here is whether a results or coverage
// stamp of another commit than the graph's has the same tree apart from
// Lattice's own ledger and cache (gitstate.SameTree); it runs only when the
// two commits are known and differ, and unknown reads not the same.
func Collect(latticeDir string, kg schema.KnowledgeGraph, dirty bool) Sources {
	src := Sources{KG: kg, Dirty: dirty}
	head := kg.GeneratedFromCommit

	src.Results = results.Load(latticeDir).WithTests(kg.Tests)
	src.Stamps.Results = ResultsInput{Present: len(src.Results.Results) > 0, Commit: src.Results.Commit}
	src.ResultsSameTree = sameTree(latticeDir, src.Results.Commit, head)

	if set, ok := coverage.Load(latticeDir); ok {
		src.Coverage = coverage.NewIndex(set, kg)
		src.Stamps.Coverage = CoverageInput{Present: true, Commit: set.Commit, Dirty: set.Dirty, PerTest: len(set.PerTest)}
		src.CoverageSameTree = sameTree(latticeDir, set.Commit, head)
	}

	bf, present, err := LoadBreaks(latticeDir)
	switch {
	case err != nil:
		src.Stamps.Breaks = BreaksInput{Present: present, Ignored: err.Error()}
	case present:
		src.Breaks = bf
		src.Stamps.Breaks = BreaksInput{Present: true, Commit: bf.Commit, Dirty: bf.Dirty, GeneratedAt: bf.GeneratedAt, Runs: len(bf.Runs)}
	}

	src.Confirm = LoadConfirm(latticeDir)

	notes, nPresent, nErrs := LoadNotes(latticeDir)
	src.Notes = notes
	src.Stamps.Notes = NotesInput{Present: nPresent, Count: len(notes), Errors: nErrs}
	return src
}

// sameTree is gitstate.SameTree for a stamp against head, run in the
// checkout that holds latticeDir. Known, equal stamps need no git read and
// stampMatches already counts them.
func sameTree(latticeDir, stamp, head string) bool {
	if stamp == "" || head == "" || stamp == head {
		return false
	}
	return gitstate.SameTree(context.Background(), latticeDir, stamp, head, []string{latticeDir})
}
