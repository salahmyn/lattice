package mutation

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// FileScore is the killed/survived tally for one source file.
type FileScore struct {
	Killed   int
	Survived int
}

// Score returns the mutation score (killed / detectable) as a percentage.
func (f FileScore) Score() float64 {
	total := f.Killed + f.Survived
	if total == 0 {
		return 0
	}
	return float64(f.Killed) / float64(total) * 100
}

// strykerReport is the relevant subset of Stryker's mutation-report JSON.
type strykerReport struct {
	Files map[string]struct {
		Mutants []struct {
			Status string `json:"status"`
		} `json:"mutants"`
	} `json:"files"`
}

// ParseStrykerJSON parses a Stryker mutation-report JSON into per-file scores.
// A mutant counts as killed when Stryker reports Killed or Timeout.
func ParseStrykerJSON(data []byte) (map[string]FileScore, error) {
	var rep strykerReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, err
	}
	out := map[string]FileScore{}
	for path, f := range rep.Files {
		fs := FileScore{}
		for _, m := range f.Mutants {
			switch strings.ToLower(m.Status) {
			case "killed", "timeout":
				fs.Killed++
			case "survived", "nocoverage", "no_coverage":
				fs.Survived++
			}
		}
		out[filepath.ToSlash(path)] = fs
	}
	return out, nil
}

// AggregatePerInvariant maps per-file scores onto invariants using the
// file -> "feature:INV" mapping. An invariant's score is the average of the
// scores of the files whose symbols enforce it.
func AggregatePerInvariant(fileScores map[string]FileScore, fileToInvariants map[string][]string) map[string]float64 {
	type acc struct {
		sum   float64
		count int
	}
	tally := map[string]*acc{}
	for file, fs := range fileScores {
		for _, inv := range fileToInvariants[filepath.ToSlash(file)] {
			a := tally[inv]
			if a == nil {
				a = &acc{}
				tally[inv] = a
			}
			a.sum += fs.Score()
			a.count++
		}
	}
	out := map[string]float64{}
	for inv, a := range tally {
		if a.count > 0 {
			out[inv] = a.sum / float64(a.count)
		}
	}
	return out
}
