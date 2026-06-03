package ui

import (
	"net/http"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// brdRow is the row shape the BRD list template iterates over. Carries
// the BRD itself plus a count of features linked to it — the template
// must not call back into the brd package, so the join is done here.
type brdRow struct {
	B            schema.BRD
	FeatureCount int
}

func (s *Server) pageBRDs(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	featuresByBRD := brd.FeaturesByBRD(kg.BRDs, kg.Features)

	rows := make([]brdRow, 0, len(kg.BRDs))
	for _, b := range kg.BRDs {
		rows = append(rows, brdRow{B: b, FeatureCount: len(featuresByBRD[b.ID])})
	}
	// Status-then-id sort: approved BRDs surface first, drafts at the
	// bottom — same "what's ready" / "what needs work" ordering the
	// Features dashboard uses.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].B.Status != rows[j].B.Status {
			return statusRank(rows[i].B.Status) < statusRank(rows[j].B.Status)
		}
		return rows[i].B.ID < rows[j].B.ID
	})

	s.render(w, "brds.html", pageData{
		Title:    "BRDs",
		Active:   "brds",
		JSONHref: "/api/v1/brds",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "BRDs"}},
		Body: map[string]interface{}{
			"Total": len(rows),
			"BRDs":  rows,
		},
	})
}

func (s *Server) pageBRD(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b := brd.Find(kg.BRDs, id)
	if b == nil {
		http.NotFound(w, r)
		return
	}
	features := brd.FeaturesByBRD(kg.BRDs, kg.Features)[id]
	drift := b.Approval != nil && b.Approval.ApprovedVersion > 0 && b.Version > b.Approval.ApprovedVersion

	// RTM rows for *this* BRD only: lets the detail page surface the
	// "is what we built what we asked for?" answer inline, without a
	// click out to /rtm.
	cfg, _ := config.Load(s.ws.LatticeDir)
	matrix := rtm.Build(kg, rtm.Options{
		MutationThreshold: cfg.MutationTesting.Thresholds.Default,
	})
	var rtmRows []rtm.Row
	var rtmSummary *rtm.BRDSummary
	for i := range matrix.Rows {
		if matrix.Rows[i].BRDID == id {
			rtmRows = append(rtmRows, matrix.Rows[i])
		}
	}
	for i := range matrix.Summaries {
		if matrix.Summaries[i].BRDID == id {
			rtmSummary = &matrix.Summaries[i]
			break
		}
	}

	s.render(w, "brd.html", pageData{
		Title:    b.ID,
		Active:   "brds",
		JSONHref: "/api/v1/brds/" + id,
		Breadcrumbs: []crumb{
			{Label: "Overview", Href: "/"},
			{Label: "BRDs", Href: "/brds"},
			{Label: b.ID},
		},
		Body: map[string]interface{}{
			"BRD":        b,
			"Features":   features,
			"Drift":      drift,
			"RTMRows":    rtmRows,
			"RTMSummary": rtmSummary,
		},
	})
}

// apiBRDs returns every BRD with its linked-feature count.
func (s *Server) apiBRDs(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	featuresByBRD := brd.FeaturesByBRD(kg.BRDs, kg.Features)
	type out struct {
		schema.BRD
		Features []string `json:"features"`
	}
	rows := make([]out, 0, len(kg.BRDs))
	for _, b := range kg.BRDs {
		rows = append(rows, out{BRD: b, Features: featuresByBRD[b.ID]})
	}
	writeJSON(w, rows)
}

// apiBRD returns one BRD plus its linked features.
func (s *Server) apiBRD(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	b := brd.Find(kg.BRDs, id)
	if b == nil {
		http.NotFound(w, r)
		return
	}
	features := brd.FeaturesByBRD(kg.BRDs, kg.Features)[id]
	writeJSON(w, map[string]interface{}{
		"brd":      b,
		"features": features,
	})
}

// statusRank orders BRD statuses so the dashboard reads top-down from
// "needs attention" (draft) to "settled" (approved/superseded). The
// rank is deliberately unstable across upgrades — re-sort, don't
// persist.
func statusRank(s schema.BRDStatus) int {
	switch s {
	case schema.BRDDraft:
		return 0
	case schema.BRDProposed:
		return 1
	case schema.BRDApproved:
		return 2
	case schema.BRDSuperseded:
		return 3
	default:
		return 4
	}
}
