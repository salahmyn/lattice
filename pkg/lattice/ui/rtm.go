package ui

import (
	"net/http"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
)

// pageRTM renders the v0.6 traceability matrix as a single table:
// one row per BRD success_criterion, with status, mapped invariant,
// enforcer/verifier counts, and (when present) mutation score. The
// per-BRD roll-up sits in the side panel.
//
// One row per SC means the page directly answers "which business
// goals are unverified?" — the question /coverage's 5th card hints
// at, but doesn't drill into.
func (s *Server) pageRTM(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg, _ := config.Load(s.ws.LatticeDir)
	matrix := rtm.Build(kg, rtm.Options{
		MutationThreshold: cfg.MutationTesting.Thresholds.Default,
	})
	coverage := rtm.ComputeCoverage(matrix)
	s.render(w, "rtm.html", pageData{
		Title:    "Requirements traceability",
		Active:   "rtm",
		JSONHref: "/api/v1/rtm",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "RTM"}},
		Body: map[string]interface{}{
			"Matrix":   matrix,
			"Coverage": coverage,
		},
	})
}

// apiRTM exposes the full matrix as JSON — the same shape MCP tools
// branch on, so a CLI / UI / agent client never has to re-derive
// per-row status.
func (s *Server) apiRTM(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	cfg, _ := config.Load(s.ws.LatticeDir)
	matrix := rtm.Build(kg, rtm.Options{
		MutationThreshold: cfg.MutationTesting.Thresholds.Default,
	})
	writeJSON(w, map[string]interface{}{
		"coverage": rtm.ComputeCoverage(matrix),
		"matrix":   matrix,
	})
}
