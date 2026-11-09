package ui

// @module-feature audit

import (
	"net/http"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
)

// auditRowView is a row plus how to draw it. Only break_tested and exercised
// get a positive colour, and exercised a weaker one; every other class,
// unknown included, stays out of green.
type auditRowView struct {
	audit.Row
	Badge string // a .badge modifier, or is-ok-weak (the page's own style)
	Open  bool   // fakes open by default
}

// @enforces audit:INV-2
func badgeFor(class string) string {
	switch class {
	case audit.ClassBreakTested:
		return "is-ok"
	case audit.ClassExercised:
		return "is-ok-weak"
	case audit.ClassFake, audit.ClassUnenforced:
		return "is-error"
	case audit.ClassUnverified, audit.ClassNotDemonstrated:
		return "is-warn"
	}
	return "is-info" // demonstrated_only, unknown
}

type auditClassChip struct {
	Class string
	Count int
	Badge string
}

// auditReport computes the audit over the live workspace, unfiltered. The
// page does not shell out to git, so dirty is only what `lattice audit`
// reports on the command line.
func (s *Server) auditReport(r *http.Request) (audit.Report, error) {
	kg, err := s.graph(r.Context())
	if err != nil {
		return audit.Report{}, err
	}
	return audit.Compute(audit.Collect(s.ws.LatticeDir, kg, false)), nil
}

// pageAudit lists one row per invariant, filterable by class and feature.
// Each row opens to its verifiers, enforcers, the evidence behind the class
// and any second reader's note.
func (s *Server) pageAudit(w http.ResponseWriter, r *http.Request) {
	full, err := s.auditReport(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	class, feature := r.URL.Query().Get("class"), r.URL.Query().Get("feature")
	shown := audit.Filter{Class: class, Feature: feature}.Apply(full)

	seen := map[string]bool{}
	var features []string
	for _, row := range full.Rows {
		if !seen[row.Feature] {
			seen[row.Feature] = true
			features = append(features, row.Feature)
		}
	}
	sort.Strings(features)

	var chips []auditClassChip
	for _, c := range audit.Classes {
		chips = append(chips, auditClassChip{Class: c, Count: full.Summary.ByClass[c], Badge: badgeFor(c)})
	}
	rows := make([]auditRowView, 0, len(shown.Rows))
	for _, row := range shown.Rows {
		rows = append(rows, auditRowView{Row: row, Badge: badgeFor(row.Class), Open: row.Class == audit.ClassFake})
	}

	s.render(w, "audit.html", pageData{
		Title:       "Assurance audit",
		Active:      "audit",
		JSONHref:    "/api/v1/audit",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Audit"}},
		Body: map[string]interface{}{
			"Report":     shown,
			"Rows":       rows,
			"RowCount":   len(rows),
			"Total":      full.Summary.Total,
			"Chips":      chips,
			"Features":   features,
			"Class":      class,
			"Feature":    feature,
			"InputLines": full.InputLines(),
		},
	})
}

// apiAudit exposes the report as lattice.audit/1, with the same filters.
func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request) {
	full, err := s.auditReport(r)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, audit.Filter{Class: r.URL.Query().Get("class"), Feature: r.URL.Query().Get("feature")}.Apply(full))
}
