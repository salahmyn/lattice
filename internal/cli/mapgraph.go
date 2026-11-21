package cli

// @module-feature cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/ui"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// mapGraph is the block graph a map draws (export map, serve): the graph
// lattice refresh would build from the workspace's inputs now. When it
// differs from the stored graph it is written to the store, the one thing
// refresh writes, and nothing else is. When it cannot be built, the stored
// graph is drawn as it is and the note says why and what to do. Only an
// unreadable store is an error.
// @enforces cli:INV-5
func mapGraph(ctx context.Context, ws *workspace.Workspace, st store.Store) (store.Graph, *ui.MapRefresh, error) {
	old, err := st.Graph(ctx)
	if err != nil {
		return store.Graph{}, nil, err
	}
	stale := func(f *refreshFailure) (store.Graph, *ui.MapRefresh, error) {
		note := fmt.Sprintf("this is the stored block graph; it was not rebuilt from the workspace (%s: %s)", f.code, f.msg)
		if s, ok := f.next.(string); ok && s != "" {
			note += "; " + s
		}
		return old, &ui.MapRefresh{Note: note + "; then run lattice refresh"}, nil
	}
	if f := refreshable(ws); f != nil {
		return stale(f)
	}
	res, report, _, f := buildBlockGraph(ctx, ws, st)
	if f != nil {
		return stale(f)
	}
	r := &ui.MapRefresh{Warnings: append(report.Warnings, res.Warnings...)}
	cur := store.Graph{Blocks: res.Blocks, Edges: res.Edges, Sources: res.Sources, Flows: res.Flows}
	if sameGraph(old, cur) {
		return old, r, nil
	}
	r.Rebuilt = true
	r.Summary = blocks.DiffGraphs(
		schema.KnowledgeGraph{Blocks: old.Blocks, BlockEdges: old.Edges, Sources: old.Sources, Flows: old.Flows},
		schema.KnowledgeGraph{Blocks: cur.Blocks, BlockEdges: cur.Edges, Sources: cur.Sources, Flows: cur.Flows}).Summary()
	if err := st.ReplaceGraph(ctx, cur); err != nil {
		r.Note = "the block graph was rebuilt for this page but not saved (" + err.Error() + "); run lattice refresh"
		return cur, r, nil
	}
	r.Saved = true
	return cur, r, nil
}

// sameGraph reports whether two block graphs are equal as stored, an empty
// list and a missing one alike.
func sameGraph(a, b store.Graph) bool {
	enc := func(v any, n int) []byte {
		if n == 0 {
			return nil
		}
		data, _ := json.Marshal(v)
		return data
	}
	return bytes.Equal(enc(a.Blocks, len(a.Blocks)), enc(b.Blocks, len(b.Blocks))) &&
		bytes.Equal(enc(a.Edges, len(a.Edges)), enc(b.Edges, len(b.Edges))) &&
		bytes.Equal(enc(a.Sources, len(a.Sources)), enc(b.Sources, len(b.Sources))) &&
		bytes.Equal(enc(a.Flows, len(a.Flows)), enc(b.Flows, len(b.Flows)))
}
