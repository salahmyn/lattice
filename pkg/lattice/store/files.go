package store

// @module-feature store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/buildinfo"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Files keeps records as YAML files (judgments in the judgments dir, other
// kinds under records/<kind>/) and the graph in lattice.json.
type Files struct{ ws *workspace.Workspace }

// OpenFiles returns the files store of ws.
func OpenFiles(ws *workspace.Workspace) *Files { return &Files{ws: ws} }

func (f *Files) Where() string { return f.ws.GraphPath() }
func (f *Files) Close() error  { return nil }

func (f *Files) dir(kind string) string {
	if kind == "judgment" {
		return f.ws.JudgmentsDir()
	}
	return filepath.Join(f.ws.LatticeDir, "records", kind)
}

type fileRecord struct {
	Record
	path string
}

func version(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:8])
}

func (f *Files) list(kind string) ([]fileRecord, error) {
	paths, err := filepath.Glob(filepath.Join(f.dir(kind), "*.yaml"))
	if err != nil {
		return nil, err
	}
	var out []fileRecord
	seen := map[string]string{}
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		id, err := idOf(body)
		switch {
		case err != nil:
			return nil, fmt.Errorf("%s: %w", p, err)
		case id == "":
			return nil, fmt.Errorf("%s: no top-level id", p)
		case seen[id] != "":
			return nil, fmt.Errorf("%s: id %s is also used in %s", p, id, seen[id])
		}
		seen[id] = p
		r := Record{Kind: kind, ID: id, Version: version(body), Body: body, Author: authorOf(body)}
		if st, err := os.Stat(p); err == nil {
			r.UpdatedAt = st.ModTime().UTC().Format(time.RFC3339)
		}
		out = append(out, fileRecord{r, p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *Files) List(_ context.Context, kind string) ([]Record, error) {
	if !name.MatchString(kind) {
		return nil, fmt.Errorf("record kind %q is not a name", kind)
	}
	frs, err := f.list(kind)
	if err != nil {
		return nil, err
	}
	out := make([]Record, len(frs))
	for i, fr := range frs {
		out[i] = fr.Record
	}
	return out, nil
}

func (f *Files) find(kind, id string) (*fileRecord, error) {
	frs, err := f.list(kind)
	if err != nil {
		return nil, err
	}
	for i := range frs {
		if frs[i].ID == id {
			return &frs[i], nil
		}
	}
	return nil, nil
}

func (f *Files) Get(_ context.Context, kind, id string) (Record, error) {
	if !name.MatchString(kind) {
		return Record{}, fmt.Errorf("record kind %q is not a name", kind)
	}
	fr, err := f.find(kind, id)
	if err != nil {
		return Record{}, err
	}
	if fr == nil {
		return Record{}, ErrNotFound
	}
	return fr.Record, nil
}

// @enforces store:INV-1
func (f *Files) Put(ctx context.Context, r Record) (Record, error) {
	if err := checkRecord(r); err != nil {
		return Record{}, err
	}
	cur, err := f.find(r.Kind, r.ID)
	if err != nil {
		return Record{}, err
	}
	switch {
	case cur == nil && r.Version != "":
		return Record{}, &StaleError{Kind: r.Kind, ID: r.ID}
	case cur != nil && r.Version != cur.Version:
		return Record{}, &StaleError{Kind: r.Kind, ID: r.ID, Current: cur.Version}
	}
	p := filepath.Join(f.dir(r.Kind), r.ID+".yaml")
	if cur != nil {
		p = cur.path // keep the file the record already lives in
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Record{}, err
	}
	if cur == nil {
		if _, err := os.Stat(p); err == nil { // a file named after this id may hold another record
			return Record{}, fmt.Errorf("record %s/%s: %s already exists and holds another record", r.Kind, r.ID, p)
		}
	}
	// shortcut: the version check and the write are not atomic, so files mode assumes one writer at a time; use postgres for several
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+r.ID+"-*.tmp")
	if err != nil {
		return Record{}, err
	}
	_, werr := tmp.Write(r.Body)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), p)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return Record{}, werr
	}
	return f.Get(ctx, r.Kind, r.ID)
}

func (f *Files) read() (schema.KnowledgeGraph, error) {
	kg := schema.KnowledgeGraph{SchemaVersion: buildinfo.SchemaVersion}
	data, err := os.ReadFile(f.ws.GraphPath())
	if os.IsNotExist(err) {
		return kg, nil
	}
	if err != nil {
		return kg, err
	}
	if err := json.Unmarshal(data, &kg); err != nil {
		return kg, fmt.Errorf("%s: %w", f.ws.GraphPath(), err)
	}
	return kg, nil
}

func (f *Files) Graph(context.Context) (Graph, error) {
	kg, err := f.read()
	return Graph{Blocks: kg.Blocks, Edges: kg.BlockEdges, Sources: kg.Sources, Flows: kg.Flows}, err
}

// ReplaceGraph writes the block graph into lattice.json and keeps the rest of it.
func (f *Files) ReplaceGraph(_ context.Context, g Graph) error {
	kg, err := f.read()
	if err != nil {
		return err
	}
	kg.Blocks, kg.BlockEdges, kg.Sources, kg.Flows = g.Blocks, g.Edges, g.Sources, g.Flows
	return graph.Write(f.ws.GraphPath(), kg)
}

func (f *Files) Search(ctx context.Context, text string, flt Filter) ([]schema.Block, error) {
	g, err := f.Graph(ctx)
	return searchBlocks(g.Blocks, text, flt), err
}

func (f *Files) Relations(ctx context.Context, id string, dir Direction, kinds []string, depth int) ([]schema.BlockEdge, error) {
	g, err := f.Graph(ctx)
	return relations(g.Edges, id, dir, kinds, depth), err
}
