package ui

// @module-feature ui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

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
	limit = min(limit, 500)
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
	View     blocks.BusinessView `json:"view"`
	Blocks   []BlockRef          `json:"blocks"`
	Versions map[string]string   `json:"versions"` // BRD id -> stored version, so a saved draft can be saved again
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
	versions := map[string]string{}
	for _, r := range recs {
		versions[r.ID] = r.Version
		b, err := blocks.ParseBRD(r.Body, "brd "+r.ID)
		if err != nil {
			return BusinessData{}, err
		}
		brds = append(brds, b)
	}
	d := BusinessData{View: blocks.Business(g.Blocks, g.Edges, brds), Blocks: make([]BlockRef, 0, len(g.Blocks)), Versions: versions}
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

var draftID = regexp.MustCompile(`^brd\.draft\.[a-z0-9-]{1,80}$`)

// draftRequest is a feature drafted on the views page.
type draftRequest struct {
	Version string        `json:"version"` // the stored version this replaces, "" for a new draft
	BRD     blocks.BRDDoc `json:"brd"`
}

// apiSaveDraft stores a drafted feature as a BRD record with status draft.
// It takes JSON only and refuses a cross-site Origin, so another page cannot post here.
func (s *Server) apiSaveDraft(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONError(w, errors.New("send the draft as application/json"), http.StatusUnsupportedMediaType)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
		writeJSONError(w, errors.New("cross-site request refused"), http.StatusForbidden)
		return
	}
	var req draftRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	d := req.BRD
	b := schema.BRD{ID: d.ID, Version: 1, Status: schema.BRDDraft, Title: d.Title, BusinessProblem: d.BusinessProblem,
		Blocks: d.Blocks, OpenQuestions: d.OpenQuestions, HumanReviewRequired: true}
	for _, f := range d.Flags {
		b.Flags = append(b.Flags, schema.BRDFlag{ID: f.ID, Meaning: f.Meaning})
	}
	for _, sc := range d.Scenarios {
		b.UserScenarios = append(b.UserScenarios, schema.UserScenario{ID: sc.ID, Narrative: sc.Narrative, Given: sc.Given, When: sc.When, Then: sc.Then, Blocks: sc.Blocks})
	}
	if !draftID.MatchString(b.ID) {
		writeJSONError(w, errors.New("a draft's id is brd.draft. plus up to 80 lower-case letters, digits and dashes"), http.StatusBadRequest)
		return
	}
	body, err := yaml.Marshal(b)
	if err == nil {
		_, err = blocks.ParseBRD(body, "draft "+b.ID)
	}
	if err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	if s.opts.Store == nil {
		writeJSONError(w, errors.New("no store"), http.StatusServiceUnavailable)
		return
	}
	s.storeMu.Lock()
	rec, err := s.opts.Store.Put(r.Context(), store.Record{Kind: "brd", ID: b.ID, Version: req.Version, Body: body})
	s.storeMu.Unlock()
	var stale *store.StaleError
	if errors.As(err, &stale) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "current_version": stale.Current})
		return
	}
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"id": rec.ID, "version": rec.Version})
}
