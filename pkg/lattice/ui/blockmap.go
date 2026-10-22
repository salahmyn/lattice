package ui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
)

//go:embed assets/map/map.html
var mapHTML []byte

// The block map (spec §9): /map renders the workspace's block graph, and
// /api/graph, /api/search and /api/relations/{id} serve it from the store.

const graphSlot = "/*__GRAPH__*/"

// MapHTML returns the map page with g inlined, so it works as a single file
// without a server (lattice export map). A non-empty title replaces "Block map".
func MapHTML(g store.Graph, title string) ([]byte, error) {
	data, err := json.Marshal(g) // escapes < and >, so the data cannot close its <script>
	if err != nil {
		return nil, err
	}
	page := bytes.Replace(mapHTML, []byte(graphSlot), data, 1)
	if title != "" {
		t := []byte(html.EscapeString(title))
		page = bytes.Replace(page, []byte("<title>Block map</title>"), append(append([]byte("<title>"), t...), "</title>"...), 1)
		page = bytes.Replace(page, []byte("<h1>Block map "), append(append([]byte("<h1>"), t...), ' '), 1)
	}
	return page, nil
}

// withStore runs fn with the store; the Postgres store holds one connection,
// so calls are serialized.
func (s *Server) withStore(w http.ResponseWriter, fn func(store.Store) (any, error)) {
	if s.opts.Store == nil {
		writeJSONError(w, errors.New("no block graph store: serve opens the workspace store"), http.StatusServiceUnavailable)
		return
	}
	s.storeMu.Lock()
	v, err := fn(s.opts.Store)
	s.storeMu.Unlock()
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

func (s *Server) pageMap(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(bytes.Replace(mapHTML, []byte(graphSlot), nil, 1))
}

func (s *Server) apiGraph(w http.ResponseWriter, r *http.Request) {
	s.withStore(w, func(st store.Store) (any, error) { return st.Graph(r.Context()) })
}

func (s *Server) apiBlockSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	s.withStore(w, func(st store.Store) (any, error) {
		return st.Search(r.Context(), q.Get("q"), store.Filter{Kind: q.Get("kind"), System: q.Get("system"), Tier: q.Get("tier"), Limit: limit})
	})
}

func (s *Server) apiRelations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir := store.Direction(q.Get("dir"))
	if dir == "" {
		dir = store.Both
	}
	depth, _ := strconv.Atoi(q.Get("depth"))
	if depth == 0 {
		depth = 1
	}
	var kinds []string
	if k := q.Get("kinds"); k != "" {
		kinds = strings.Split(k, ",")
	}
	s.withStore(w, func(st store.Store) (any, error) {
		return st.Relations(r.Context(), r.PathValue("id"), dir, kinds, depth)
	})
}

//go:embed assets/map/views.html
var viewsHTML []byte

const businessSlot = "/*__BUSINESS__*/"

// BusinessData is what the views page reads: the business view plus the
// blocks it can name and pick.
type BusinessData struct {
	View   blocks.BusinessView `json:"view"`
	Blocks []BlockRef          `json:"blocks"`
}

// BlockRef is a block as the views page lists it.
type BlockRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Status string `json:"status,omitempty"`
	Name   string `json:"name,omitempty"` // catalogue id, when the block has one
}

// Business reads the graph and the BRD records and computes the views page's data.
func Business(ctx context.Context, st store.Store) (BusinessData, error) {
	g, err := st.Graph(ctx)
	if err != nil {
		return BusinessData{}, err
	}
	recs, err := st.List(ctx, "brd")
	if err != nil {
		return BusinessData{}, err
	}
	var brds []schema.BRD
	for _, r := range recs {
		b, err := blocks.ParseBRD(r.Body, "brd "+r.ID)
		if err != nil {
			return BusinessData{}, err
		}
		brds = append(brds, b)
	}
	d := BusinessData{View: blocks.Business(g.Blocks, g.Edges, brds), Blocks: make([]BlockRef, 0, len(g.Blocks))}
	for _, b := range g.Blocks {
		if b.Initiative == "" {
			d.Blocks = append(d.Blocks, BlockRef{ID: b.ID, Title: b.Title, Kind: b.Kind, Status: b.Attrs["status"], Name: b.Attrs["catalogue_id"]})
		}
	}
	return d, nil
}

// ViewsHTML returns the views page with d inlined (lattice export views).
func ViewsHTML(d BusinessData, title string) ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	page := bytes.Replace(viewsHTML, []byte(businessSlot), data, 1)
	if title != "" {
		t := []byte(html.EscapeString(title))
		page = bytes.Replace(page, []byte("<title>Block views</title>"), append(append([]byte("<title>"), t...), "</title>"...), 1)
		page = bytes.Replace(page, []byte("<h1>Block views "), append(append([]byte("<h1>"), t...), ' '), 1)
	}
	return page, nil
}

func (s *Server) pageViews(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(bytes.Replace(viewsHTML, []byte(businessSlot), nil, 1))
}

func (s *Server) apiBusiness(w http.ResponseWriter, r *http.Request) {
	s.withStore(w, func(st store.Store) (any, error) { return Business(r.Context(), st) })
}
