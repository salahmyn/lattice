package ui

import (
	"net/http"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// actorTouchpoints aggregates everything an actor can do in the system.
// Computed live from context.yaml + the knowledge graph — no extra
// authoring required beyond declaring the actor in context.yaml.
type actorTouchpoints struct {
	Actor       schema.Actor        `json:"actor"`
	Features    []string            `json:"features"`
	EntryPoints []schema.EntryPoint `json:"entry_points"`
	BRDs        []string            `json:"brds"`
}

func (s *Server) pageActors(w http.ResponseWriter, r *http.Request) {
	ctx, err := schema.LoadContext(s.ws.ContextPath())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Per-actor row: how many EPs they touch. Cheap to compute and
	// gives the listing a sortable signal beyond just the actor name.
	type row struct {
		Actor       schema.Actor
		EPCount     int
		FeatureCount int
	}
	rows := make([]row, 0, len(ctx.Actors))
	for _, a := range ctx.Actors {
		touch := computeActorTouchpoints(a, ctx, kg)
		rows = append(rows, row{Actor: a, EPCount: len(touch.EntryPoints), FeatureCount: len(touch.Features)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Actor.ID < rows[j].Actor.ID })

	s.render(w, "actors.html", pageData{
		Title:    "Actors",
		Active:   "actors",
		JSONHref: "/api/v1/actors",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Actors"}},
		Body: map[string]interface{}{
			"Rows":            rows,
			"ExternalSystems": ctx.ExternalSystems,
		},
	})
}

func (s *Server) pageActor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, err := schema.LoadContext(s.ws.ContextPath())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var actor *schema.Actor
	for i := range ctx.Actors {
		if ctx.Actors[i].ID == id {
			actor = &ctx.Actors[i]
			break
		}
	}
	if actor == nil {
		http.NotFound(w, r)
		return
	}
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	touch := computeActorTouchpoints(*actor, ctx, kg)

	s.render(w, "actor.html", pageData{
		Title:    "Actor: " + actor.Name,
		Active:   "actors",
		JSONHref: "/api/v1/actors/" + id,
		Breadcrumbs: []crumb{
			{Label: "Overview", Href: "/"},
			{Label: "Actors", Href: "/actors"},
			{Label: actor.Name},
		},
		Body: touch,
	})
}

func (s *Server) apiActors(w http.ResponseWriter, r *http.Request) {
	ctx, err := schema.LoadContext(s.ws.ContextPath())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	out := make([]actorTouchpoints, 0, len(ctx.Actors))
	for _, a := range ctx.Actors {
		out = append(out, computeActorTouchpoints(a, ctx, kg))
	}
	writeJSON(w, out)
}

func (s *Server) apiActor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, err := schema.LoadContext(s.ws.ContextPath())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	var actor *schema.Actor
	for i := range ctx.Actors {
		if ctx.Actors[i].ID == id {
			actor = &ctx.Actors[i]
			break
		}
	}
	if actor == nil {
		http.NotFound(w, r)
		return
	}
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, computeActorTouchpoints(*actor, ctx, kg))
}

// computeActorTouchpoints expands an actor's `uses` list into the full
// touchpoint set:
//   - declared feature ids (verbatim from actor.uses, if they match a feature)
//   - every EP whose flow visits any of those features
//   - the BRDs those features implement (so the actor → BRD link is one click)
//
// The actor.uses field is intentionally loose — operators can list a
// component name or a feature id. We resolve both: if it matches a
// feature exactly, use it; otherwise treat as a name to substring-match
// against feature ids. Better to overshow than undershow on a
// PM-facing surface.
func computeActorTouchpoints(a schema.Actor, ctx schema.ArchitectureContext, kg schema.KnowledgeGraph) actorTouchpoints {
	_ = ctx // not used yet; reserved for cross-actor links

	featureIDs := map[string]bool{}
	for _, f := range kg.Features {
		featureIDs[f.ID] = true
	}

	// Pass 1: exact-id matches.
	touched := map[string]bool{}
	for _, u := range a.Uses {
		if featureIDs[u] {
			touched[u] = true
		}
	}
	// Pass 2: substring fallback so an actor that names a component
	// ("checkout") still picks up every feature under that prefix.
	for _, u := range a.Uses {
		if featureIDs[u] {
			continue
		}
		for fid := range featureIDs {
			if fid == u || hasPrefix(fid, u+".") {
				touched[fid] = true
			}
		}
	}

	t := actorTouchpoints{Actor: a}
	for f := range touched {
		t.Features = append(t.Features, f)
	}
	sort.Strings(t.Features)

	// Every EP that visits a touched feature joins the list.
	for _, ep := range kg.EntryPoints {
		for _, step := range ep.Flow {
			if touched[step.Feature] {
				t.EntryPoints = append(t.EntryPoints, ep)
				break
			}
		}
	}
	sort.Slice(t.EntryPoints, func(i, j int) bool { return t.EntryPoints[i].ID < t.EntryPoints[j].ID })

	// BRDs the touched features point at — gives the page a "what
	// business intent does this actor exercise?" cross-link.
	brdSeen := map[string]bool{}
	for _, f := range kg.Features {
		if touched[f.ID] && f.ImplementsBRD != "" && !brdSeen[f.ImplementsBRD] {
			brdSeen[f.ImplementsBRD] = true
			t.BRDs = append(t.BRDs, f.ImplementsBRD)
		}
	}
	for _, b := range kg.BRDs {
		for _, fid := range b.ImplementsVia {
			if touched[fid] && !brdSeen[b.ID] {
				brdSeen[b.ID] = true
				t.BRDs = append(t.BRDs, b.ID)
				break
			}
		}
	}
	sort.Strings(t.BRDs)
	return t
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
