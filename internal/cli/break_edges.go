package cli

// @module-feature breaktest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
)

// edgeHopsOf reads, for each promise the run selects, the hops on its slice
// paths: every edge a shortest path from a root to one of the promise's
// enforcers crosses, resolved or unknown. A resolved hop of a kind keyed by a
// literal (http, rpc, table, publish) carries the literals that make it; an
// unknown hop (an ambiguous reference followed to one of its candidates) is
// listed with the reason it is not broken. Nothing is guessed: a hop is never
// given a position the graph does not record.
func edgeHopsOf(kg schema.KnowledgeGraph, rr *reach.Result, layers []schema.LayerRule, opts breaktest.Options) map[string][]breaktest.EdgeHop {
	if rr == nil || rr.Graph == nil {
		return nil
	}
	sg := slice.NewGraph(rr, kg, layers)
	type key struct{ from, to, kind string }
	edges := map[key]reach.Edge{}
	for _, e := range rr.Graph.Edges {
		edges[key{e.From, e.To, e.Kind}] = e
	}
	enforcers := map[string][]slice.Enforcer{}
	seen := map[[2]string]bool{}
	for _, s := range kg.Symbols {
		for _, inv := range s.EnforcesInvariants {
			p := inv
			if !strings.Contains(p, ":") {
				p = s.Feature + ":" + inv
			}
			if opts.Selects(p) && !seen[[2]string{p, s.FQN}] {
				seen[[2]string{p, s.FQN}] = true
				enforcers[p] = append(enforcers[p], slice.Enforcer{FQN: s.FQN, Layer: s.Layer})
			}
		}
	}
	out := map[string][]breaktest.EdgeHop{}
	for promise, es := range enforcers {
		rec := slice.Compute(sg, promise, es)
		for _, p := range rec.Paths {
			for _, h := range p.Hops {
				if !breaktest.EdgeKind(h.Kind) {
					continue
				}
				eh := breaktest.EdgeHop{Hop: breaktest.Hop{From: h.From, To: h.To, Kind: h.Kind}}
				if h.Truth != slice.TruthResolved {
					eh.Skip = "the hop is an ambiguous reference followed to one of its candidates, so it is not known to run"
					out[promise] = append(out[promise], eh)
					continue
				}
				for _, s := range edges[key{h.From, h.To, h.Kind}].Sites {
					eh.Sites = append(eh.Sites, breaktest.EdgeSite{File: s.File, Line: s.Line, Start: s.Start, End: s.End, Key: s.Key})
				}
				if h.Kind == "http" {
					// A function literal, not the method value: the call graph follows calls.
					eh.Served = func(key string) bool { return rr.Graph.HTTPServes(key) }
				}
				out[promise] = append(out[promise], eh)
			}
		}
	}
	return out
}

// printEdgeNotes closes the break table with what the edge runs found.
func printEdgeNotes(io *IO, runs []breaktest.Run) {
	var survived []string
	for _, r := range runs {
		if r.Finding == breaktest.CodeEdgeBreakSurvived {
			survived = append(survived, fmt.Sprintf("%s: %s", r.Promise, breaktest.EdgeSummary(r)))
		}
	}
	if len(survived) == 0 {
		return
	}
	sort.Strings(survived)
	io.printf("\n%s (info): no verifier noticed %d hop(s) breaking\n", breaktest.CodeEdgeBreakSurvived, len(survived))
	for _, s := range survived {
		io.printf("  %s\n", s)
	}
}
