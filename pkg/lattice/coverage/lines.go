package coverage

// @module-feature coverage

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// BodyLines are the instrumented body lines of the symbol in the report, in
// ascending order (see Of for what the body is).
func (c SymbolCov) BodyLines() []int { return sortedCopy(c.bodyLines) }

// HitLines are the body lines that ran at least once, in ascending order.
func (c SymbolCov) HitLines() []int { return sortedCopy(c.hitLines) }

func sortedCopy(in []int) []int {
	out := append([]int(nil), in...)
	sort.Ints(out)
	return out
}

// Remap rewrites the report's file keys through resolve (report path ->
// graph path), merging files that resolve to the same path.
func (r Report) Remap(resolve func(string) string) Report { return r.remap(resolve) }

// PathMapper joins the file paths of a coverage report to the graph's
// repo-relative paths, the way an ingest does.
type PathMapper struct{ r *resolver }

// NewPathMapper returns the mapper for a graph.
func NewPathMapper(kg schema.KnowledgeGraph) *PathMapper { return &PathMapper{r: newResolver(kg)} }

// Resolve maps one report path to a graph path. root is the prefix the
// report's relative paths start from, "" for none. A path that joins no
// graph file is returned as it is.
func (m *PathMapper) Resolve(p, root string) string { return m.r.resolve(p, root) }

// Known reports whether file is a file of the graph.
func (m *PathMapper) Known(file string) bool { return m.r.files[file] }

// SuiteReport reads the whole-suite reports a workspace's coverage: block
// names, maps them onto the graph's files and merges them into one report.
// base is the directory that holds lattice/. A report that cannot be read or
// parsed is skipped and named in problems, so a missing file never reads as
// a file with no covered lines. The second result says which files the
// reports' include sets cover, for Of. Per-test directories are not read.
func SuiteReport(base string, kg schema.KnowledgeGraph, cfg config.Coverage) (rep Report, included Included, problems []string) {
	rep = NewReport("")
	res := newResolver(kg)
	var incs []Included
	for _, c := range cfg.Reports {
		data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(c.Path)))
		if err != nil {
			problems = append(problems, c.Path+": "+err.Error())
			continue
		}
		parsed, err := Parse(data, Format(c.Format))
		if err != nil {
			problems = append(problems, c.Path+": "+err.Error())
			continue
		}
		root := c.Root
		rep.Merge(parsed.Report.Remap(func(p string) string { return res.resolve(p, root) }))
		incs = append(incs, includeSet(root, c.Include))
	}
	return rep, anyOf(incs), problems
}
