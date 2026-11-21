package slice

// @module-feature reachability

import (
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// MaxExercisedBy caps the verifiers listed on one hop.
const MaxExercisedBy = 5

// Readings of Record.Exercised.
const (
	ExercisedMeasured = "measured"
	ExercisedUnknown  = "unknown"
)

// Coverage is the ingested per-test coverage a slice reads, wired by the
// caller (coverage.Index implements it) so the slice names no provider.
type Coverage interface {
	// TestRan reports whether a test's own coverage executed a symbol, and
	// the basis of that answer.
	TestRan(test, symbol string) (executed bool, basis string)
	// HasPerTest reports whether any per-test coverage was ingested.
	HasPerTest() bool
	// LayerSpan is the layers a test's own run executed; ok is false when the
	// test has no per-test coverage.
	LayerSpan(test string) (layers []string, ok bool)
}

// Verifier is one verifier of the promise and what its own run covered.
type Verifier struct {
	ID string `json:"id"`
	// Layers is the sorted span of its own run, or ["unknown"] when it has
	// no per-test coverage. An empty list means it ran and executed no
	// symbol of the graph.
	Layers []string `json:"layers"`
}

// VerifiersOf lists the verifiers of an audit row, sorted.
func VerifiersOf(row audit.Row) []string {
	out := make([]string, 0, len(row.Evidence.Verifiers))
	for _, v := range row.Evidence.Verifiers {
		out = append(out, v.FQN)
	}
	sort.Strings(out)
	return out
}

// WithCoverage returns the graph bound to ingested coverage. A nil graph
// stays nil; a nil cov leaves the graph without coverage.
func (g *Graph) WithCoverage(cov Coverage) *Graph {
	if g == nil {
		return nil
	}
	c := *g
	c.cov = cov
	return &c
}

// Exercise marks which hops of the listed paths were executed, from the
// per-test coverage the graph is bound to, and lists the verifiers with
// their layer spans.
//
// A hop is exercised when one verifier's own per-test run executed symbols on
// both sides of it. Only a per-test report counts: a per-file or suite run
// never does, and neither does a verifier with no coverage. A node that is no
// symbol of the knowledge graph (a table, a module's top-level code) cannot
// be executed by itself, so each side of the hop is represented by the
// nearest symbol on that side of the path: the start side by the closest
// symbol at or before the hop's start, the end side by the closest symbol at
// or after the hop's end. A hop with no symbol on a side cannot be shown
// exercised. A hop whose truth is unknown is never exercised: running both
// ends does not show an ambiguous reference was the edge taken. Co-execution
// shows both sides ran in that verifier's test, not that the call crossed
// the hop.
//
// Without per-test coverage the record says exercised "unknown" once and no
// hop reads exercised; absent is never false and unknown is never a pass. The
// reading is not touched.
// @enforces reachability:INV-12
func Exercise(rec *Record, g *Graph, verifiers []string) {
	var cov Coverage
	if g != nil {
		cov = g.cov
	}
	ids := append([]string(nil), verifiers...)
	sort.Strings(ids)
	rec.Verifiers = nil
	for _, id := range ids {
		layers := []string{schema.LayerUnknown}
		if cov != nil {
			if l, ok := cov.LayerSpan(id); ok {
				layers = append([]string{}, l...)
			}
		}
		rec.Verifiers = append(rec.Verifiers, Verifier{ID: id, Layers: layers})
	}
	if cov == nil || !cov.HasPerTest() {
		rec.Exercised, rec.ExercisedWhy = ExercisedUnknown, "no per-test coverage was ingested, so no hop is shown exercised"
		return
	}
	rec.Exercised = ExercisedMeasured
	rec.ExercisedWhy = "a hop is exercised when one verifier's own run executed symbols on both sides of it"
	ran := map[[2]string]bool{}
	ranBy := func(v, sym string) bool {
		k := [2]string{v, sym}
		r, seen := ran[k]
		if !seen {
			executed, basis := cov.TestRan(v, sym)
			r = executed && basis == coverage.BasisPerTest
			ran[k] = r
		}
		return r
	}
	for pi := range rec.Paths {
		p := &rec.Paths[pi]
		n := 0
		for i := range p.Hops {
			h := &p.Hops[i]
			h.Exercised, h.ExercisedBy = false, nil
			if h.Truth != TruthResolved {
				continue
			}
			from, to := g.nearestSymbol(p, i, -1), g.nearestSymbol(p, i+1, +1)
			if from == "" || to == "" {
				continue
			}
			for _, v := range ids {
				if ranBy(v, from) && ranBy(v, to) && len(h.ExercisedBy) < MaxExercisedBy {
					h.ExercisedBy = append(h.ExercisedBy, v)
				}
			}
			if h.Exercised = len(h.ExercisedBy) > 0; h.Exercised {
				n++
			}
		}
		p.ExercisedHops = &n
	}
}

// nearestSymbol is the symbol standing for node i of the path (node 0 is the
// root, node len(Hops) the enforcer), looking in the direction dir from the
// node itself, or "" when the path has none that way.
func (g *Graph) nearestSymbol(p *Path, i, dir int) string {
	for ; i >= 0 && i <= len(p.Hops); i += dir {
		fqn := ""
		if i < len(p.Hops) {
			fqn = p.Hops[i].From
		} else {
			fqn = p.Hops[i-1].To
		}
		if g.isSym[fqn] {
			return fqn
		}
	}
	return ""
}
