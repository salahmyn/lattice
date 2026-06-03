package ui

import (
	"net/http"

	"github.com/salahmyn/lattice/pkg/lattice/views"
)

// pageJourney renders the v0.6 journey view: every EP that touches a
// feature in the BRD's `implements_via` set, in one mermaid graph.
// The page answers "show me the X flow" with a single click instead
// of forcing the operator to reconstruct it from N entry-point
// detail pages.
func (s *Server) pageJourney(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	j := views.BuildJourney(kg, id)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	// Strip the markdown fence so the template wraps the body with the
	// mermaid runtime element directly — same contract as pageFlow.
	mermaid := stripMermaidFence(j.Mermaid)

	s.render(w, "journey.html", pageData{
		Title:    "Journey: " + id,
		Active:   "brds",
		JSONHref: "/api/v1/journeys/" + id,
		Breadcrumbs: []crumb{
			{Label: "Overview", Href: "/"},
			{Label: "BRDs", Href: "/brds"},
			{Label: id, Href: "/brds/" + id},
			{Label: "Journey"},
		},
		Body: map[string]interface{}{
			"BRDID":       j.BRDID,
			"BRDTitle":    j.BRDTitle,
			"Features":    j.Features,
			"EntryPoints": j.EntryPoints,
			"Mermaid":     mermaid,
		},
	})
}

// apiJourney is the agent-friendly JSON shape: features in the BRD's
// surface, every EP that joins the journey, and the same rendered
// mermaid diagram the UI shows.
func (s *Server) apiJourney(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	j := views.BuildJourney(kg, id)
	if j == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, j)
}
