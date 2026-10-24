package scip

// @module-feature scip

import (
	"sort"
	"strings"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

// CallGraph is a per-symbol callees index built from a SCIP corpus.
// It powers v0.3.1's SCIP-backed flow tracer by answering "starting
// from this entry-point handler, which other symbols are reached?"
// transitively.
//
// SCIP does not directly encode caller→callee edges. We reconstruct
// them per file: for any reference occurrence at line L in file F, the
// caller is the innermost definition whose extent contains L. A
// definition's extent is its `enclosing_range` when the indexer records
// one. Without it we fall back to "most recent definition wins": the
// definition that starts closest before L, the standard SCIP-without-AST
// trick, which credits top-level code after a function to that function.
type CallGraph struct {
	// callees maps the SCIP symbol moniker of a callerto the set of
	// SCIP symbol monikers it references inside its body.
	callees map[string]map[string]bool
}

// BuildCallGraph constructs a CallGraph from every loaded index.
// O(occurrences) — fine for a multi-thousand-file repo.
func (c *Corpus) BuildCallGraph() *CallGraph {
	g := &CallGraph{callees: map[string]map[string]bool{}}
	if c == nil || len(c.indexes) == 0 {
		return g
	}
	for _, idx := range c.indexes {
		for _, doc := range idx.Documents {
			defs := definitionsByLine(doc)
			if len(defs) == 0 {
				continue
			}
			for _, occ := range doc.Occurrences {
				if occ.SymbolRoles&definitionRole != 0 {
					continue // skip definition occurrences themselves
				}
				caller := mostRecentDef(defs, occurrenceLine(occ))
				if caller == "" || caller == occ.Symbol {
					continue
				}
				set, ok := g.callees[caller]
				if !ok {
					set = map[string]bool{}
					g.callees[caller] = set
				}
				set[occ.Symbol] = true
			}
		}
	}
	return g
}

// CalleesOf returns the direct callees of caller — every symbol the
// caller's body references.
func (g *CallGraph) CalleesOf(caller string) []string {
	if g == nil {
		return nil
	}
	set := g.callees[caller]
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TransitiveCallees walks the call graph from start until either
// maxDepth is reached or no new symbols are visited. Cycle-safe:
// every symbol is visited at most once. Returns visited symbols in
// BFS order so the closest reaches are first in the result.
func (g *CallGraph) TransitiveCallees(start string, maxDepth int) []string {
	if g == nil {
		return nil
	}
	if maxDepth <= 0 {
		maxDepth = 8 // a sensible default for v0.3.1
	}
	visited := map[string]bool{start: true}
	frontier := []string{start}
	var out []string
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, sym := range frontier {
			for _, callee := range g.CalleesOf(sym) {
				if visited[callee] {
					continue
				}
				visited[callee] = true
				out = append(out, callee)
				next = append(next, callee)
			}
		}
		frontier = next
	}
	return out
}

// ResolveSymbol matches a Lattice IR FQN to a SCIP symbol moniker by
// simple-name comparison. Returns the best match (longest common
// suffix). This is the same join key BlastRadius uses, generalised to
// return the SCIP-side identifier so the caller can hand it to
// CalleesOf / TransitiveCallees.
func (c *Corpus) ResolveSymbol(fqn string) string {
	needle := simpleName(fqn)
	if needle == "" || c == nil {
		return ""
	}
	bestSym, bestLen := "", 0
	for _, idx := range c.indexes {
		for _, doc := range idx.Documents {
			for _, occ := range doc.Occurrences {
				if occ.SymbolRoles&definitionRole == 0 {
					continue
				}
				if !symbolMatches(occ.Symbol, needle) {
					continue
				}
				if score := scoreSymbolMatch(occ.Symbol, fqn); score > bestLen {
					bestSym, bestLen = occ.Symbol, score
				}
			}
		}
	}
	return bestSym
}

// scoreSymbolMatch returns the length of the longest suffix of fqn
// that appears anywhere in scipSym — letting "App\\Foo::bar" match
// "scip-php php . . . App.Foo.bar()" without forcing exact identity.
func scoreSymbolMatch(scipSym, fqn string) int {
	// Normalise both sides to dot-separated lowercase for the match.
	a := normaliseFQN(scipSym)
	b := normaliseFQN(fqn)
	if a == b {
		return len(b) * 2 // strongest possible match
	}
	if strings.Contains(a, b) {
		return len(b)
	}
	return 0
}

func normaliseFQN(s string) string {
	s = strings.ToLower(s)
	for _, r := range []string{"\\", "::", "/", "(", ")", "#"} {
		s = strings.ReplaceAll(s, r, ".")
	}
	return strings.Trim(s, ". ")
}

// definitionsByLine returns the (line, symbol) pairs for every
// function/method/class definition in doc, sorted by line ascending.
// Lines are 1-based; endLine is the last line of the definition's
// enclosing_range, or 0 when the indexer did not record one.
type defAt struct {
	line    int
	endLine int
	symbol  string
}

func definitionsByLine(doc *scippb.Document) []defAt {
	var out []defAt
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&definitionRole == 0 {
			continue
		}
		out = append(out, defAt{line: occurrenceLine(occ), endLine: enclosingEndLine(occ), symbol: occ.Symbol})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// mostRecentDef returns the symbol of the innermost definition that
// encloses targetLine: the latest-starting definition at or before it
// whose extent reaches it. A definition with no recorded extent is taken
// to reach it, the "most recent definition wins" fallback. It returns ""
// when no definition encloses the line, as for top-level code. Used to
// attribute a reference to its enclosing function.
// @enforces scip:INV-1
func mostRecentDef(defs []defAt, targetLine int) string {
	if len(defs) == 0 {
		return ""
	}
	// Binary search for the rightmost def whose line ≤ targetLine.
	lo, hi := 0, len(defs)
	for lo < hi {
		mid := (lo + hi) / 2
		if defs[mid].line <= targetLine {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for i := lo - 1; i >= 0; i-- {
		if d := defs[i]; d.endLine == 0 || d.endLine >= targetLine {
			return d.symbol
		}
	}
	return ""
}

// enclosingEndLine is the 1-based last line of a definition's
// enclosing_range ([startLine, startChar, endLine, endChar], or
// [line, startChar, endChar] on one line), or 0 when it has none.
func enclosingEndLine(occ *scippb.Occurrence) int {
	switch r := occ.EnclosingRange; len(r) {
	case 4:
		return int(r[2]) + 1
	case 3:
		return int(r[0]) + 1
	}
	return 0
}

// NewCorpus wraps already-parsed indexes in a corpus.
func NewCorpus(indexes ...*scippb.Index) *Corpus {
	return &Corpus{indexes: indexes}
}

// Def is where a SCIP symbol is defined: its document and 1-based line.
type Def struct {
	Symbol string
	File   string
	Line   int
}

// ResolvedRef is one reference occurrence of the corpus with both ends
// located: Caller is the innermost definition that encloses the reference
// (bounded by its enclosing_range, as BuildCallGraph attributes it), or
// zero for top-level code; Callee is the referenced symbol's definition.
type ResolvedRef struct {
	File   string
	Line   int
	Caller Def
	Callee Def
}

// References returns every reference whose symbol the corpus defines,
// ordered by file and line. References to symbols defined outside the
// corpus (the standard library, dependencies) and to local symbols are
// left out: they are not code of the workspace.
// @enforces scip:INV-1
func (c *Corpus) References() []ResolvedRef {
	if c == nil {
		return nil
	}
	defs := map[string]Def{}
	for _, idx := range c.indexes {
		for _, doc := range idx.Documents {
			for _, occ := range doc.Occurrences {
				if occ.SymbolRoles&definitionRole == 0 || strings.HasPrefix(occ.Symbol, "local ") {
					continue
				}
				if _, ok := defs[occ.Symbol]; !ok {
					defs[occ.Symbol] = Def{Symbol: occ.Symbol, File: doc.RelativePath, Line: occurrenceLine(occ)}
				}
			}
		}
	}
	var out []ResolvedRef
	for _, idx := range c.indexes {
		for _, doc := range idx.Documents {
			byLine := definitionsByLine(doc)
			for _, occ := range doc.Occurrences {
				if occ.SymbolRoles&definitionRole != 0 {
					continue
				}
				callee, ok := defs[occ.Symbol]
				if !ok {
					continue
				}
				ln := occurrenceLine(occ)
				r := ResolvedRef{File: doc.RelativePath, Line: ln, Callee: callee}
				if caller := mostRecentDef(byLine, ln); caller != "" {
					if caller == occ.Symbol {
						continue
					}
					r.Caller = Def{Symbol: caller, File: doc.RelativePath}
					for _, d := range byLine {
						if d.symbol == caller && d.line <= ln {
							r.Caller.Line = d.line
						}
					}
				}
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}
