package importer

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// DefaultMinCandidateSymbols is the floor below which a source directory is
// considered too thin to stand as a feature candidate.
const DefaultMinCandidateSymbols = 3

// Options tunes Stage-1 discovery.
type Options struct {
	// Scopes, when non-empty, restricts discovery to source files under any
	// of these code-root-relative subtrees (e.g. "Modules/Accounts").
	// Multi-scope lets a reviewer drive several bounded contexts in one
	// pass — replaces the "scan, then hand-filter candidates.json" pattern.
	Scopes []string
	// MinCandidateSymbols is the fewest production symbols a directory must
	// hold to become a candidate. Below it, its symbols stay unclustered and
	// drag down discovery coverage. Zero falls back to the default.
	MinCandidateSymbols int
	// Exclude is a set of slash-path globs; matching files are ignored
	// entirely (e.g. generated code the adapters still parse).
	Exclude []string
}

func (o Options) minSymbols() int {
	if o.MinCandidateSymbols > 0 {
		return o.MinCandidateSymbols
	}
	return DefaultMinCandidateSymbols
}

// dirGroup accumulates everything discovered for one source directory.
type dirGroup struct {
	dir      string
	prodSyms []ir.Symbol
	testSyms []ir.Symbol
	files    map[string]bool
	surfaces []ir.Surface
	langs    map[string]int
}

// Discover runs Stage 1: it clusters the parsed modules into feature
// candidates and reports discovery coverage. It is deterministic.
func Discover(modules []ir.Module, opts Options) CandidatesFile {
	groups := groupByDirectory(modules, opts)

	dirs := make([]string, 0, len(groups))
	for d := range groups {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	min := opts.minSymbols()
	var candidates []Candidate
	pkgTotals := make(map[string]int)
	clustered := make(map[string]bool)

	for _, d := range dirs {
		g := groups[d]
		if len(g.prodSyms) == 0 {
			continue // a directory of pure test code is not a candidate
		}
		pkgTotals[d] = len(g.prodSyms)
		if len(g.prodSyms) < min {
			continue
		}
		candidates = append(candidates, buildCandidate(g))
		clustered[d] = true
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })

	return CandidatesFile{
		Version:    candidatesVersion,
		Scopes:     append([]string(nil), opts.Scopes...),
		Candidates: candidates,
		Coverage:   computeCoverage(pkgTotals, clustered),
	}
}

// InScopes reports whether file falls under any of the given scope subtrees.
// Empty scopes means "no restriction" — consistent with the discovery
// behaviour, so this helper is reusable at draft/review filter time too.
func InScopes(file string, scopes []string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if inScope(file, s) {
			return true
		}
	}
	return false
}

// FilterByScopes returns a copy of cf containing only candidates whose
// files lie under at least one of scopes. Empty scopes returns cf
// untouched. Used by draft/review to scope late without re-running scan.
func FilterByScopes(cf CandidatesFile, scopes []string) CandidatesFile {
	if len(scopes) == 0 {
		return cf
	}
	out := cf
	out.Candidates = nil
	for _, c := range cf.Candidates {
		keep := false
		for _, f := range c.Files {
			if InScopes(f, scopes) {
				keep = true
				break
			}
		}
		if keep {
			out.Candidates = append(out.Candidates, c)
		}
	}
	return out
}

// groupByDirectory buckets every in-scope symbol by its source directory.
func groupByDirectory(modules []ir.Module, opts Options) map[string]*dirGroup {
	groups := make(map[string]*dirGroup)
	for _, m := range modules {
		if !InScopes(m.File, opts.Scopes) || excluded(m.File, opts.Exclude) {
			continue
		}
		dir := path.Dir(m.File)
		g := groups[dir]
		if g == nil {
			g = &dirGroup{dir: dir, files: map[string]bool{}, langs: map[string]int{}}
			groups[dir] = g
		}
		hasProd := false
		for _, s := range m.Symbols {
			if s.IsTest {
				g.testSyms = append(g.testSyms, s)
				continue
			}
			g.prodSyms = append(g.prodSyms, s)
			hasProd = true
		}
		if hasProd {
			g.files[m.File] = true
			if m.Language != "" {
				g.langs[m.Language]++
			}
		}
		g.surfaces = append(g.surfaces, m.Surfaces...)
	}
	return groups
}

// buildCandidate turns a qualifying directory group into a Candidate.
func buildCandidate(g *dirGroup) Candidate {
	symbols := make([]string, 0, len(g.prodSyms))
	for _, s := range g.prodSyms {
		symbols = append(symbols, s.FQN)
	}
	sort.Strings(symbols)

	files := make([]string, 0, len(g.files))
	for f := range g.files {
		files = append(files, f)
	}
	sort.Strings(files)

	evidence, points := collectEvidence(g)

	return Candidate{
		ID:         candidateID(symbols),
		Package:    g.dir,
		Language:   dominantLanguage(g.langs),
		Symbols:    symbols,
		Files:      files,
		Confidence: float64(points) / 10,
		Evidence:   evidence,
	}
}

// collectEvidence assembles the evidence list and a confidence score (in
// integer tenths) from the signals that fired for a directory group.
func collectEvidence(g *dirGroup) ([]Evidence, int) {
	var ev []Evidence
	points := 5 // package_structure always fires for a qualifying directory

	ev = append(ev, Evidence{
		Signal: SignalPackage,
		Detail: fmt.Sprintf("%d symbols across %d file(s) in %s",
			len(g.prodSyms), len(g.files), g.dir),
	})

	for _, s := range surfaceEvidence(g.surfaces) {
		ev = append(ev, s)
	}
	if len(g.surfaces) > 0 {
		points += 3
	}

	if len(g.testSyms) > 0 {
		ev = append(ev, Evidence{
			Signal: SignalTestGroup,
			Detail: fmt.Sprintf("%d co-located test symbols", len(g.testSyms)),
		})
		points++
	}

	if sup := supertypeEvidence(g.prodSyms); sup != "" {
		ev = append(ev, Evidence{Signal: SignalSupertype, Detail: sup})
		points++
	}

	if points > 10 {
		points = 10
	}
	return ev, points
}

// surfaceEvidence renders one Evidence per harvested surface, sorted.
func surfaceEvidence(surfaces []ir.Surface) []Evidence {
	labels := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		labels = append(labels, surfaceLabel(s))
	}
	sort.Strings(labels)
	ev := make([]Evidence, 0, len(labels))
	for _, l := range labels {
		ev = append(ev, Evidence{Signal: SignalSurface, Detail: l})
	}
	return ev
}

// surfaceLabel renders a surface as a stable, human-readable string.
func surfaceLabel(s ir.Surface) string {
	switch {
	case s.Method != "" && s.Path != "":
		return s.Type + " " + s.Method + " " + s.Path
	case s.Path != "":
		return s.Type + " " + s.Path
	case s.Name != "":
		return s.Type + " " + s.Name
	default:
		return s.Type
	}
}

// supertypeEvidence reports the most-shared base type, if two or more symbols
// extend it.
func supertypeEvidence(syms []ir.Symbol) string {
	counts := make(map[string]int)
	for _, s := range syms {
		for _, b := range s.BaseClasses {
			counts[b]++
		}
	}
	bestBase, best := "", 1
	for base, n := range counts {
		if n > best || (n == best && base < bestBase) {
			bestBase, best = base, n
		}
	}
	if bestBase == "" {
		return ""
	}
	return fmt.Sprintf("%d symbols extend %s", best, bestBase)
}

// dominantLanguage returns the most common language, alphabetical on ties.
func dominantLanguage(langs map[string]int) string {
	best, bestN := "", 0
	for lang, n := range langs {
		if n > bestN || (n == bestN && lang < best) {
			best, bestN = lang, n
		}
	}
	return best
}

// inScope reports whether file falls under the scope subtree.
func inScope(file, scope string) bool {
	if scope == "" {
		return true
	}
	scope = strings.TrimSuffix(scope, "/")
	return file == scope || strings.HasPrefix(file, scope+"/")
}

// excluded reports whether file matches any exclude glob.
//
// v0.2.1 #8: backed by doublestar so patterns can recurse with `**` —
// the natural way to exclude a whole tree like Modules/**/Database/
// Migrations/**. The old path.Match was single-segment-only and forced
// users to enumerate every depth.
func excluded(file string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, file); ok {
			return true
		}
	}
	return false
}
