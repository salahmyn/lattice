package ui

// @module-feature reachability

import (
	"context"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
)

// sliceView is a promise's slice as the audit page draws it: the record, a
// badge for the reading and one line per path.
type sliceView struct {
	slice.Record
	Badge string
	Lines []sliceLine
}

// sliceLine is one path of a slice: where it starts, the layers it is carried
// by, and the chain of nodes.
type sliceLine struct {
	Root    string
	Layers  []string
	Chain   string
	Unknown bool
}

// sliceBadge draws a reading. None is green: a slice says which ways in reach
// the code, not that the promise holds. Complete is plain information, and
// every reading that leaves something unproven is a warning.
func sliceBadge(reading string) string {
	switch reading {
	case slice.ReadingUnreached, slice.ReadingUnknownFromHop:
		return "is-warn"
	}
	return "is-info"
}

// sliceGraph returns the slice graph of the live workspace, or nil when the
// server has no SliceBuilder or the build failed or had no reach graph. Every
// promise then reads unknown.
func (s *Server) sliceGraph(ctx context.Context) *slice.Graph {
	if s.opts.SliceBuilder == nil {
		return nil
	}
	f, _, err := s.facts(ctx)
	if err != nil {
		return nil
	}
	return f.sg
}

// sliceViewOf computes the slice of one audit row.
func sliceViewOf(g *slice.Graph, row audit.Row) sliceView {
	rec := slice.Compute(g, row.Promise, slice.EnforcersOf(row))
	slice.Exercise(&rec, g, slice.VerifiersOf(row))
	v := sliceView{Record: rec, Badge: sliceBadge(rec.Reading)}
	for _, p := range rec.Paths {
		l := sliceLine{Root: p.Root.FQN, Layers: p.CarriedBy, Chain: slice.PathLine(p)}
		if p.Root.EntryPoint != "" {
			l.Root = "entry point " + p.Root.EntryPoint
		} else if p.Root.Reason != "" {
			l.Root = p.Root.Reason + ": " + p.Root.FQN
		}
		for _, h := range p.Hops {
			if h.Truth != slice.TruthResolved {
				l.Unknown = true
			}
		}
		v.Lines = append(v.Lines, l)
	}
	return v
}
