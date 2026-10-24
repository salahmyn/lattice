package coverage

// @module-feature coverage

import (
	"path"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// SymbolCov is one symbol's coverage in one report.
type SymbolCov struct {
	// Known is false when the report has no data for the symbol: its file
	// is absent and outside the report's include set.
	Known bool `json:"-"`
	// Lines and Hit count the instrumented body lines and those that ran.
	Lines int `json:"lines"`
	Hit   int `json:"hit"`
	// Executed is true when the symbol ran at least once.
	Executed bool `json:"executed"`
	// body and hit line numbers, kept for enforcer roll-ups.
	bodyLines, hitLines []int
}

// Included reports whether a file the report does not list was inside its
// instrumented set, so it ran zero lines rather than having no data.
type Included func(file string) bool

// Of maps a report onto a symbol by its line extent.
//
//   - The body is the instrumented lines in (Line, EndLine]. The declaration
//     line is not evidence: V8, istanbul and coverage.py mark it run when
//     the module loads. A one-line symbol has no body lines, except in
//     formats that never mark declarations on load (Go blocks).
//   - A function record at the declaration line decides Executed when the
//     report has one. A symbol with none of its own (a class) executed when
//     a function record inside its extent ran; otherwise when a body line ran.
//   - A file absent from the report has no data (Known false), unless the
//     report's run measured it (Universe) or it is inside the report's
//     include set: then every body line counts as unhit.
//   - Symbol-level evidence (Postgres call counts) decides by FQN.
//
// @enforces coverage:INV-2, coverage:INV-3
func Of(r Report, s schema.GraphSymbol, included Included) SymbolCov {
	if n, ok := r.Symbols[s.FQN]; ok {
		return SymbolCov{Known: true, Executed: n > 0}
	}
	from, to := s.Line+1, s.EndLine
	f := r.Files[s.File]
	if f == nil {
		var universe *FileCov
		if r.Universe != nil {
			universe = r.Universe[s.File]
		}
		switch {
		case universe != nil:
			// Measured by the run, untouched by this test: its instrumented
			// lines all count as unhit.
			c := SymbolCov{Known: true}
			for l := from; l <= to; l++ {
				if _, ok := universe.Lines[l]; ok {
					c.Lines++
					c.bodyLines = append(c.bodyLines, l)
				}
			}
			return c
		case included != nil && included(s.File):
			// Inside the include set but not listed: not loaded at all. Which
			// lines were instrumented is unknown, so every body line counts.
			c := SymbolCov{Known: true}
			for l := from; l <= to; l++ {
				c.Lines++
				c.bodyLines = append(c.bodyLines, l)
			}
			return c
		}
		return SymbolCov{}
	}
	if to < from && f.DeclEvidence {
		from, to = s.Line, s.Line
	}
	c := SymbolCov{Known: true}
	for l := from; l <= to; l++ {
		h, ok := f.Lines[l]
		if !ok {
			continue
		}
		c.Lines++
		c.bodyLines = append(c.bodyLines, l)
		if h > 0 {
			c.Hit++
			c.hitLines = append(c.hitLines, l)
		}
	}
	if calls, ok := f.Funcs[s.Line]; ok {
		c.Executed = calls > 0
		return c
	}
	inner, innerRan := false, false
	for l, calls := range f.Funcs {
		if l > s.Line && l <= s.EndLine {
			inner = true
			innerRan = innerRan || calls > 0
		}
	}
	if inner {
		c.Executed = innerRan
	} else {
		c.Executed = c.Hit > 0
	}
	return c
}

// resolver joins a report's file paths to the graph's repo-relative paths.
type resolver struct {
	files map[string]bool
	list  []string // graph files, longest first
}

func newResolver(kg schema.KnowledgeGraph) *resolver {
	r := &resolver{files: map[string]bool{}}
	add := func(f string) {
		if f != "" && !r.files[f] {
			r.files[f] = true
			r.list = append(r.list, f)
		}
	}
	for _, s := range kg.Symbols {
		add(s.File)
	}
	for _, t := range kg.Tests {
		add(t.File)
	}
	for _, m := range kg.Modules {
		add(m.File)
	}
	sort.Slice(r.list, func(i, j int) bool {
		if len(r.list[i]) != len(r.list[j]) {
			return len(r.list[i]) > len(r.list[j])
		}
		return r.list[i] < r.list[j]
	})
	return r
}

// resolve maps one report path to a graph path. root is the prefix the
// report's paths are relative to (from config). A path that is a graph file
// stays; else the longest graph file that is a whole-segment suffix of it
// (an absolute path, a Go import path); else the only graph file it is a
// suffix of (a path relative to a package directory). Anything else is kept
// as it is and joins no symbol.
func (r *resolver) resolve(p, root string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "file://")
	if root = strings.Trim(strings.ReplaceAll(root, "\\", "/"), "/"); root != "" && !path.IsAbs(p) {
		p = root + "/" + strings.TrimPrefix(p, "./")
	}
	if !path.IsAbs(p) {
		p = path.Clean(p)
	}
	if r.files[p] {
		return p
	}
	for _, f := range r.list {
		if strings.HasSuffix(p, "/"+f) {
			return f
		}
	}
	var hit string
	for _, f := range r.list {
		if strings.HasSuffix(f, "/"+p) {
			if hit != "" {
				return p // ambiguous: never guess
			}
			hit = f
		}
	}
	if hit != "" {
		return hit
	}
	return p
}

// includeSet builds the Included predicate of a report from its globs,
// relative to root.
func includeSet(root string, globs []string) Included {
	if len(globs) == 0 {
		return nil
	}
	root = strings.Trim(strings.ReplaceAll(root, "\\", "/"), "/")
	pats := make([]string, 0, len(globs))
	for _, g := range globs {
		g = strings.TrimPrefix(strings.ReplaceAll(g, "\\", "/"), "./")
		if root != "" {
			g = root + "/" + g
		}
		pats = append(pats, g)
	}
	return func(file string) bool {
		for _, p := range pats {
			if ok, _ := doublestar.Match(p, file); ok {
				return true
			}
		}
		return false
	}
}

// anyOf joins Included predicates.
func anyOf(preds []Included) Included {
	var live []Included
	for _, p := range preds {
		if p != nil {
			live = append(live, p)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return func(file string) bool {
		for _, p := range live {
			if p(file) {
				return true
			}
		}
		return false
	}
}
