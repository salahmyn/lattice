package coverage

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Finding codes proposed by rnd/0009. Prototype-local: not in schema, and
// nothing raises them outside this package's tests.
const (
	CodeVerifierMissesEnforcer = "VERIFIER_MISSES_ENFORCER"
	CodeSymbolUnreached        = "SYMBOL_UNREACHED"
	CodeSymbolTestOnly         = "SYMBOL_TEST_ONLY"
	CodeSymbolUntested         = "SYMBOL_UNTESTED"
	CodeCoverageBelowFloor     = "COVERAGE_BELOW_FLOOR"
)

// SymbolCov is one symbol's coverage in one report.
type SymbolCov struct {
	Known    bool // the report covers the symbol's file
	Executed bool // the symbol's body ran at least once
	Lines    int  // instrumented lines in the body
	Hit      int  // of those, lines that ran
}

// Of maps a report onto a symbol by its line extent. The body is the lines
// after the declaration line up to EndLine: V8 marks a function's declaration
// line as run when its module loads, so that line proves nothing. A function
// record at the declaration line (LCOV FN/FNDA) decides Executed when present.
func Of(r Report, s schema.GraphSymbol) SymbolCov {
	f := r[s.File]
	if f == nil {
		return SymbolCov{}
	}
	c := SymbolCov{Known: true}
	from, to := s.Line+1, s.EndLine
	if to < from { // a one-line symbol
		from, to = s.Line, s.Line
	}
	for l := from; l <= to; l++ {
		h, ok := f.Lines[l]
		if !ok {
			continue
		}
		c.Lines++
		if h > 0 {
			c.Hit++
		}
	}
	if calls, ok := f.Funcs[s.Line]; ok {
		c.Executed = calls > 0
	} else {
		c.Executed = c.Hit > 0
	}
	return c
}

// Edges maps a symbol's FQN to the FQNs its body names: the static call
// graph, as far as a name scan can see it.
type Edges map[string][]string

var (
	reIdent  = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)
	reRPC    = regexp.MustCompile(`\.rpc\(\s*["'\x60]([A-Za-z_][A-Za-z0-9_.]*)["'\x60]`)
	reSQLRef = regexp.MustCompile(`(?i)\b(?:([a-z_][a-z0-9_]*)\s*\.\s*)?([a-z_][a-z0-9_]*)\s*\(`)
)

// StaticEdges builds the static graph by scanning each symbol's source lines
// (root is the code root the graph's file paths are relative to):
//
//   - an identifier in a body that names another symbol is an edge to it
//     (every symbol of that name: over-approximate, so no live code reads as
//     dead because two modules reuse a name);
//   - `.rpc('name')` in app code is an edge to the SQL function sql.public.name;
//   - `name(` or `schema.name(` in a SQL function body is an edge to that SQL
//     function.
//
// This stands in for the tree-sitter and SCIP edges the note proposes. It
// does not see comments as comments, dynamic dispatch, or table access.
func StaticEdges(root string, kg schema.KnowledgeGraph) (Edges, error) {
	byName := map[string][]string{} // app symbols by simple name
	sqlFn := map[string]string{}    // "schema.name" -> FQN
	for _, s := range kg.Symbols {
		if s.Language == "postgres" {
			if strings.HasPrefix(s.FQN, "sql.policy.") || strings.HasPrefix(s.FQN, "sql.trigger.") {
				continue
			}
			sqlFn[strings.ToLower(s.Name)] = s.FQN
			continue
		}
		byName[s.Name] = append(byName[s.Name], s.FQN)
	}
	files := map[string][]string{}
	lines := func(file string) ([]string, error) {
		if l, ok := files[file]; ok {
			return l, nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		l := strings.Split(string(data), "\n")
		files[file] = l
		return l, nil
	}

	edges := Edges{}
	all := append(append([]schema.GraphSymbol{}, kg.Symbols...), kg.Tests...)
	for _, s := range all {
		src, err := lines(s.File)
		if err != nil {
			return nil, err
		}
		end := s.EndLine
		if end < s.Line {
			end = s.Line
		}
		if s.Line < 1 || end > len(src) {
			continue
		}
		body := strings.Join(src[s.Line-1:end], "\n")
		seen := map[string]bool{s.FQN: true}
		add := func(to string) {
			if to != "" && !seen[to] {
				seen[to] = true
				edges[s.FQN] = append(edges[s.FQN], to)
			}
		}
		if s.Language == "postgres" {
			for _, m := range reSQLRef.FindAllStringSubmatch(body, -1) {
				schemaName := strings.ToLower(m[1])
				if schemaName == "" {
					schemaName = "public"
				}
				add(sqlFn[schemaName+"."+strings.ToLower(m[2])])
			}
			continue
		}
		for _, m := range reRPC.FindAllStringSubmatch(body, -1) {
			name := strings.ToLower(m[1])
			if !strings.Contains(name, ".") {
				name = "public." + name
			}
			add(sqlFn[name])
		}
		for _, id := range reIdent.FindAllString(body, -1) {
			for _, to := range byName[id] {
				add(to)
			}
		}
	}
	for k := range edges {
		sort.Strings(edges[k])
	}
	return edges, nil
}

// Reach says how a symbol is reached in the static graph.
type Reach string

const (
	ReachEntry    Reach = "entry"     // from a declared entry point's handler
	ReachTestOnly Reach = "test_only" // only from tests
	ReachNone     Reach = "unreached" // from nothing
)

// Reachability walks the static graph from every entry-point handler, then
// from every test, and classifies each production symbol.
func Reachability(kg schema.KnowledgeGraph, edges Edges) map[string]Reach {
	walk := func(roots []string) map[string]bool {
		seen := map[string]bool{}
		queue := append([]string{}, roots...)
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			if seen[n] {
				continue
			}
			seen[n] = true
			queue = append(queue, edges[n]...)
		}
		return seen
	}
	var entryRoots, testRoots []string
	for _, ep := range kg.EntryPoints {
		entryRoots = append(entryRoots, ep.Handler.Symbol)
	}
	for _, t := range kg.Tests {
		testRoots = append(testRoots, t.FQN)
	}
	fromEntry, fromTests := walk(entryRoots), walk(testRoots)
	out := map[string]Reach{}
	for _, s := range kg.Symbols {
		switch {
		case fromEntry[s.FQN]:
			out[s.FQN] = ReachEntry
		case fromTests[s.FQN]:
			out[s.FQN] = ReachTestOnly
		default:
			out[s.FQN] = ReachNone
		}
	}
	return out
}

// Policy is the prototype's slice of the proposed `coverage:` config.
// A floor of 0 is off.
type Policy struct {
	FeatureFloor   float64 // percent of a feature's body lines run by the suite
	InvariantFloor float64 // percent of an invariant's enforcer lines run by its verifiers
}

// Input is everything one analysis reads.
type Input struct {
	KG    schema.KnowledgeGraph
	Edges Edges
	// Suite is the coverage of the whole test run.
	Suite Report
	// PerTest is each test's own coverage, keyed by test FQN. A test with
	// no entry has no per-test evidence; it is never read as "ran nothing".
	PerTest map[string]Report
}

// invariant is one feature:INV ref with its enforcers and verifiers.
type invariant struct {
	ref       string
	feature   string
	enforcers []schema.GraphSymbol
	verifiers []string
}

func invariants(kg schema.KnowledgeGraph) []invariant {
	byRef := map[string]*invariant{}
	var order []string
	get := func(ref string) *invariant {
		if iv, ok := byRef[ref]; ok {
			return iv
		}
		feature, _, _ := strings.Cut(ref, ":")
		iv := &invariant{ref: ref, feature: feature}
		byRef[ref] = iv
		order = append(order, ref)
		return iv
	}
	qualify := func(ref, feature string) string {
		if strings.Contains(ref, ":") {
			return ref
		}
		return feature + ":" + ref
	}
	for _, f := range kg.Features {
		for _, inv := range f.Invariants {
			get(f.ID + ":" + inv.ID)
		}
	}
	for _, s := range kg.Symbols {
		for _, ref := range s.EnforcesInvariants {
			iv := get(qualify(ref, s.Feature))
			iv.enforcers = append(iv.enforcers, s)
		}
	}
	for _, t := range kg.Tests {
		for _, ref := range t.Verifies {
			iv := get(qualify(ref, t.Feature))
			iv.verifiers = append(iv.verifiers, t.FQN)
		}
	}
	sort.Strings(order)
	out := make([]invariant, 0, len(order))
	for _, r := range order {
		out = append(out, *byRef[r])
	}
	return out
}

// verifierEvidence reports, for one invariant, which verifiers executed an
// enforcer and how many verifiers have no per-test coverage.
func (in Input) verifierEvidence(iv invariant) (hitting []string, unknown int) {
	for _, v := range iv.verifiers {
		r, ok := in.PerTest[v]
		if !ok {
			unknown++
			continue
		}
		for _, e := range iv.enforcers {
			if Of(r, e).Executed {
				hitting = append(hitting, v)
				break
			}
		}
	}
	return hitting, unknown
}

// behaviour reports symbols that hold runtime code (not interfaces or types).
func behaviour(s schema.GraphSymbol) bool {
	switch s.Kind {
	case "function", "method", "class", "const", "trait":
		return true
	}
	return false
}

func pct(hit, lines int) float64 {
	if lines == 0 {
		return 100
	}
	return math.Round(1000*float64(hit)/float64(lines)) / 10
}

// Analyze raises the rnd/0009 findings. Severities are the proposed defaults.
func Analyze(in Input, p Policy) []schema.Violation {
	var v []schema.Violation
	loc := func(s schema.GraphSymbol) *schema.Location {
		return &schema.Location{File: s.File, Line: s.Line}
	}

	// VERIFIER_MISSES_ENFORCER, and the invariant floor.
	for _, iv := range invariants(in.KG) {
		if len(iv.enforcers) == 0 || len(iv.verifiers) == 0 {
			continue // UNENFORCED_INVARIANT / UNVERIFIED_INVARIANT already cover these
		}
		hitting, unknown := in.verifierEvidence(iv)
		if len(hitting) == 0 && unknown == 0 {
			names := make([]string, 0, len(iv.enforcers))
			for _, e := range iv.enforcers {
				names = append(names, e.FQN)
			}
			v = append(v, schema.Violation{
				Code: CodeVerifierMissesEnforcer, Severity: schema.SeverityWarning,
				Subject: iv.ref, FeatureID: iv.feature,
				Message: fmt.Sprintf("none of the %d verifier(s) of %s executes its enforcer %s: the test can pass without the guard running",
					len(iv.verifiers), iv.ref, strings.Join(names, ", ")),
				Location: loc(iv.enforcers[0]),
				NextAction: &schema.NextAction{Kind: "add_verification",
					Detail: "call the enforcer from the verifier (no stub in its place), or tag the test that does"},
			})
		}
		if p.InvariantFloor > 0 {
			// Measured over what the invariant's own verifiers run when every
			// verifier has per-test evidence, else over the whole suite.
			r := in.Suite
			basis := "the whole suite"
			if unknown == 0 {
				r = Report{}
				for _, t := range iv.verifiers {
					r.Merge(in.PerTest[t])
				}
				basis = "its verifiers"
			}
			lines, hit := 0, 0
			for _, e := range iv.enforcers {
				c := Of(r, e)
				lines += c.Lines
				hit += c.Hit
			}
			if lines > 0 && pct(hit, lines) < p.InvariantFloor {
				v = append(v, schema.Violation{
					Code: CodeCoverageBelowFloor, Severity: schema.SeverityWarning,
					Subject: iv.ref, FeatureID: iv.feature,
					Message: fmt.Sprintf("%s: %s run %.1f%% of its enforcer lines (%d/%d), below the invariant floor of %.0f%%",
						iv.ref, basis, pct(hit, lines), hit, lines, p.InvariantFloor),
					Location: loc(iv.enforcers[0]),
				})
			}
		}
	}

	// Reachability and the feature floor.
	reach := Reachability(in.KG, in.Edges)
	type tally struct{ lines, hit int }
	features := map[string]*tally{}
	for _, s := range in.KG.Symbols {
		if !behaviour(s) {
			continue
		}
		c := Of(in.Suite, s)
		if s.Feature != "" && c.Known {
			t := features[s.Feature]
			if t == nil {
				t = &tally{}
				features[s.Feature] = t
			}
			t.lines += c.Lines
			t.hit += c.Hit
		}
		switch reach[s.FQN] {
		case ReachNone:
			if c.Known && c.Executed {
				continue // the run reached it: a dynamic edge the scan missed
			}
			evidence := "no coverage data for its file"
			if c.Known {
				evidence = "and the test run never executed it"
			}
			v = append(v, schema.Violation{
				Code: CodeSymbolUnreached, Severity: schema.SeverityWarning,
				Subject: s.FQN, FeatureID: s.Feature,
				Message:  fmt.Sprintf("%s is reached from no entry point, no other symbol and no test (%s)", s.FQN, evidence),
				Location: loc(s),
				NextAction: &schema.NextAction{Kind: "remove_or_attribute",
					Detail: "delete it, declare the entry point that calls it, or allowlist it with a reason"},
			})
		case ReachTestOnly:
			v = append(v, schema.Violation{
				Code: CodeSymbolTestOnly, Severity: schema.SeverityInfo,
				Subject: s.FQN, FeatureID: s.Feature,
				Message:  fmt.Sprintf("%s is called only from tests: no entry point reaches it", s.FQN),
				Location: loc(s),
			})
		case ReachEntry:
			if c.Known && !c.Executed {
				v = append(v, schema.Violation{
					Code: CodeSymbolUntested, Severity: schema.SeverityInfo,
					Subject: s.FQN, FeatureID: s.Feature,
					Message:  fmt.Sprintf("%s is reachable from an entry point, but no test executed it", s.FQN),
					Location: loc(s),
				})
			}
		}
	}
	if p.FeatureFloor > 0 {
		for id, t := range features {
			if t.lines > 0 && pct(t.hit, t.lines) < p.FeatureFloor {
				v = append(v, schema.Violation{
					Code: CodeCoverageBelowFloor, Severity: schema.SeverityWarning,
					Subject: id, FeatureID: id,
					Message: fmt.Sprintf("feature %s: the suite runs %.1f%% of its body lines (%d/%d), below the feature floor of %.0f%%",
						id, pct(t.hit, t.lines), t.hit, t.lines, p.FeatureFloor),
				})
			}
		}
	}

	sort.SliceStable(v, func(i, j int) bool {
		if v[i].Code != v[j].Code {
			return v[i].Code < v[j].Code
		}
		return v[i].Subject < v[j].Subject
	})
	return v
}

// Level is the truth level of one invariant with the proposed top rung:
// declared, verified (an enforcer and a verifier exist), demonstrated (every
// verifier with a result passed), exercised (demonstrated, and a verifier's
// own coverage shows it executed an enforcer). resultOf is results.Set.Lookup
// shaped: passed, known.
func Level(in Input, ref string, resultOf func(string) (passed, known bool)) (string, string) {
	for _, iv := range invariants(in.KG) {
		if iv.ref != ref {
			continue
		}
		if len(iv.enforcers) == 0 || len(iv.verifiers) == 0 {
			return "declared", "no enforcer or no verifier"
		}
		anyKnown := false
		for _, t := range iv.verifiers {
			passed, known := resultOf(t)
			if known && !passed {
				return "verified", "a verifier failed"
			}
			anyKnown = anyKnown || known
		}
		if !anyKnown {
			return "verified", "no verifier result ingested"
		}
		hitting, _ := in.verifierEvidence(iv)
		if len(hitting) == 0 {
			return "demonstrated", "verifiers passed, but no verifier's coverage executes an enforcer"
		}
		return "exercised", hitting[0] + " passed and executed an enforcer"
	}
	return "", "unknown invariant"
}
