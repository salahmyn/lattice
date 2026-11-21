package cli

// @module-feature reachability

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// sliceBuilder builds the read-only graph and the slice graph over its reach
// result. The graph is nil in review mode, which has no source.
func sliceBuilder(ws *workspace.Workspace) func(context.Context) (schema.KnowledgeGraph, *slice.Graph, error) {
	return func(ctx context.Context) (schema.KnowledgeGraph, *slice.Graph, error) {
		kg, rr, err := buildGraphReach(ctx, ws, buildOptions{readOnly: true})
		if err != nil {
			return kg, nil, err
		}
		sg := slice.NewGraph(rr, kg, ws.Layers)
		if ix := coverageIndex(ws, kg); ix != nil { // a nil *Index must not become a non-nil Coverage
			sg = sg.WithCoverage(ix)
		}
		return kg, sg, nil
	}
}

// newSliceCommand is `lattice slice <invariant-id>`: the ways into the system
// that run the code keeping one promise, and the layers each passes through.
func newSliceCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slice <invariant-id>",
		Short: "Show which ways into the system reach the code that keeps a promise, and through which layers",
		Long: `Answers, for one invariant (a promise, as <feature>:INV-n): which roots of the
call graph (entry points, entry files, jobs, triggers) end up running the code
that enforces it, and through which layers?

'lattice audit' says whether an enforcer is reached. The slice adds the paths:
for every root that reaches an enforcer, one shortest path, hop by hop, each
hop an edge of the reach graph (call, new, rpc, table, jsx, ref) with the
runtime layer of both ends, and the ordered layers the path is carried by.

A hop is resolved when the graph resolved the reference. It is unknown when the
only edge is an ambiguous reference followed to one of its recorded candidates;
a reference with no recorded candidate is not followed. The reading, in this
order:

  no_enforcer       the promise has no enforcer
  unknown           no reach graph (review mode) or no roots
  unreached         some enforcer has no path from any root
  unknown_from_hop  every enforcer has a path, and a path has an unknown hop
  complete          every enforcer has a path and every hop is resolved

A hop is exercised when one verifier's own per-test run executed symbols on both
sides of it (a node that is no symbol, such as a table, is stood for by the
nearest symbol on its side of the path). It prints =kind=> instead of -kind->,
each path says how many of its hops are exercised, and the verifiers are listed
with the layers their own runs covered. Without ingested per-test coverage the
slice says so once and no hop reads exercised; this never changes the reading.

A hop is break-tested (kind*, break_tested in --json) when an edge break of it,
recorded by 'lattice break --edges' for this commit, was killed: the literal
that names the hop's target at its out end was altered, and a verifier failed.
A hop no edge break killed shows nothing; this never changes the reading either.

A slice with an unknown hop is never complete. At most 50 paths are listed
(unknown ones first); the rest are counted. --json prints lattice.slice/1.
An unknown id is a usage error (exit 2).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			kg, sg, err := sliceBuilder(ws)(cmd.Context())
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			dirty, _ := gitDirty(filepath.Dir(ws.LatticeDir), ws.LatticeDir)
			rep := audit.Compute(audit.Collect(ws.LatticeDir, kg, dirty))
			id := strings.TrimSpace(args[0])
			if unknown := rep.UnknownPromises([]string{id}); len(unknown) > 0 {
				return usageError(io, "SLICE_UNKNOWN_PROMISE", "unknown promise "+id+" (want <feature>:INV-n, as listed by lattice audit)")
			}
			var row audit.Row
			for _, r := range rep.Rows {
				if r.Promise == id {
					row = r
				}
			}
			rec := slice.Compute(sg, id, slice.EnforcersOf(row))
			slice.Exercise(&rec, sg, slice.VerifiersOf(row))
			slice.MarkBreakTested(&rec, edgeRunsOf(ws.LatticeDir, kg.GeneratedFromCommit, id))
			rec.Commit = kg.GeneratedFromCommit
			if io.JSON {
				return io.printJSON(rec)
			}
			slice.WriteText(io.Out, rec)
			return nil
		},
	}
	return cmd
}

// edgeRunsOf reads the edge breaks of one promise from breaks.json, when the
// file is for the commit the graph is at.
func edgeRunsOf(latticeDir, head, promise string) []slice.EdgeRun {
	bf, _, err := audit.LoadBreaks(latticeDir)
	if err != nil {
		return nil
	}
	var out []slice.EdgeRun
	for _, r := range audit.EdgeRuns(bf, head)[promise] {
		by := r.FailedTests
		if len(by) == 0 {
			by = r.Verifiers
		}
		out = append(out, slice.EdgeRun{From: r.Hop.From, To: r.Hop.To, Kind: r.Hop.Kind, Killed: r.Outcome == "killed", By: by})
	}
	return out
}
