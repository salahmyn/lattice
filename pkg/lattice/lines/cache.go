// Package lines answers, for every block of code, what it is for and how far
// the verifiers of that intent reach into its lines.
//
// `lattice break --lines` records, for each promise, which lines its own
// verifiers ran: the verifier group's invocation is run with coverage on and
// the report is kept in lattice/.cache/results/verifier-lines.json. `lattice
// audit --lines` joins that file with the call graph and the suite coverage
// into one level per instrumented line. A line with no evidence reads
// unknown, never covered.
package lines

// @module-feature audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// FileSchema is the version tag of verifier-lines.json.
const FileSchema = "lattice.verifier-lines/1"

// Path is where verifier-lines.json lives under the lattice directory,
// beside break-baselines.json.
func Path(latticeDir string) string {
	return filepath.Join(latticeDir, ".cache", "results", "verifier-lines.json")
}

// File is verifier-lines.json: the lines each promise's verifiers ran, kept
// for one tree. Tree and Stamp are the baselines' (the HEAD tree, and what
// else a baseline depends on), so a file of another tree is never read as
// this one's.
type File struct {
	Schema string `json:"schema"`
	Tree   string `json:"tree"`
	Stamp  string `json:"stamp"`
	// Invocations are the coverage of each baseline invocation, by the
	// invocation key `lattice break` names it by. Promises whose verifiers
	// share one command share one entry.
	Invocations map[string]Invocation `json:"invocations"`
	// Promises lists, per promise (<feature>:INV-n), the invocations that ran
	// its verifiers, one per runner group.
	Promises map[string][]Group `json:"promises"`
}

// Invocation is the coverage one invocation wrote.
type Invocation struct {
	// Name is the runner, the language of a template, or go.
	Name string `json:"name"`
	// Files maps a workspace file to the lines the run instrumented and the
	// ones it ran. Only workspace files are kept.
	Files map[string]FileLines `json:"files,omitempty"`
	// Unknown is why the invocation has no line data: the runner has no
	// {coverage}, no report was written, or it could not be parsed.
	Unknown string `json:"unknown,omitempty"`
}

// FileLines is one file's coverage in one invocation, as line ranges
// ("1-4, 7").
type FileLines struct {
	Instrumented string `json:"instrumented"`
	Hit          string `json:"hit,omitempty"`
	// Decl is set for a format that never marks a declaration line as run on
	// load (Go blocks), so a one-line function's own line is evidence.
	Decl bool `json:"decl,omitempty"`
}

// Group is one runner group of one promise: the invocation that ran its
// verifiers, and what else ran in it.
type Group struct {
	// Key is the invocation's key; empty when the group was never run.
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
	// Tests are the verifiers of the promise that ran in the invocation.
	Tests []string `json:"tests,omitempty"`
	// Shared says the invocation also ran a test that is not a verifier of
	// the promise, so a line it hit may have been run by that test alone.
	// Without {junit} nothing says which tests ran, and it is true.
	Shared bool `json:"shared"`
	// Others names up to five of the other tests, OthersTotal counts them.
	Others      []string `json:"other_tests,omitempty"`
	OthersTotal int      `json:"other_total,omitempty"`
	// Unknown is why the group has no line data of its own: pgTAP reports
	// calls per function, the baseline did not pass, or the like. The
	// invocation's own Unknown applies too.
	Unknown string `json:"unknown,omitempty"`
}

// Load reads verifier-lines.json. ok is false when there is none, or it is
// of another schema or unreadable (err says which of the latter two).
func Load(latticeDir string) (f *File, ok bool, err error) {
	data, rerr := os.ReadFile(Path(latticeDir))
	if os.IsNotExist(rerr) {
		return nil, false, nil
	}
	if rerr != nil {
		return nil, false, rerr
	}
	var out File
	if jerr := json.Unmarshal(data, &out); jerr != nil {
		return nil, false, fmt.Errorf("verifier-lines.json: %w", jerr)
	}
	if out.Schema != FileSchema {
		return nil, false, fmt.Errorf("verifier-lines.json: schema %q is not %s", out.Schema, FileSchema)
	}
	return &out, true, nil
}

// New returns an empty file for a tree.
func New(tree, stamp string) *File {
	return &File{Schema: FileSchema, Tree: tree, Stamp: stamp,
		Invocations: map[string]Invocation{}, Promises: map[string][]Group{}}
}

// Write saves the file.
func (f *File) Write(latticeDir string) error {
	f.Schema = FileSchema
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	p := Path(latticeDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// Compact returns the sorted, de-duplicated lines of a set as ranges:
// {1,2,3,7} is "1-3, 7".
func Compact(lines []int) string { return compact(lines, "-") }

// compact joins runs of consecutive lines with sep.
func compact(lines []int, sep string) string {
	ls := append([]int(nil), lines...)
	sort.Ints(ls)
	var parts []string
	for i := 0; i < len(ls); {
		j := i
		for j+1 < len(ls) && (ls[j+1] == ls[j]+1 || ls[j+1] == ls[j]) {
			j++
		}
		if ls[i] == ls[j] {
			parts = append(parts, strconv.Itoa(ls[i]))
		} else {
			parts = append(parts, strconv.Itoa(ls[i])+sep+strconv.Itoa(ls[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// Expand reads the ranges Compact writes. A malformed part is skipped.
func Expand(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ReplaceAll(part, "–", "-"))
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				continue
			}
		}
		for l := a; l <= b; l++ {
			out = append(out, l)
		}
	}
	return out
}

// RanSymbol reports whether this file's hit lines include a line of the
// symbol's own body, by the rule the line levels use (the lines after its
// declaration, or its one line when the format never marks a declaration as
// run). known is false when the data says nothing about the symbol: its
// lines are not among the instrumented ones, or it has no extent.
func (fl FileLines) RanSymbol(s schema.GraphSymbol) (ran, known bool) {
	if s.File == "" || s.EndLine <= 0 {
		return false, false
	}
	cov := coverage.Of(fileDataOf(fl).report(s.File), s, nil)
	if !cov.Known || cov.Lines == 0 {
		return false, false
	}
	return len(cov.HitLines()) > 0, true
}
