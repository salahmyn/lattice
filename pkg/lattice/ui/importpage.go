package ui

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/importer"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// importCandidateRow is the joined view of a Stage-1 candidate, its
// session decision, and (when present) the v0.2.1 draft mode tag —
// everything the UI needs to render one row of the review table.
type importCandidateRow struct {
	importer.Candidate
	Decision string `json:"decision"` // "" | "accepted" | "rejected"
	Mode     string `json:"mode,omitempty"`     // deterministic | llm | cached | fallback (v0.2.1)
	FeatureID string `json:"feature_id,omitempty"` // when a draft exists, the labeled feature
}

// pageImport renders the candidates table. Filters apply server-side
// (package prefix substring; decision status; mode) so the URL is
// shareable: /import?package=modules/Webhook&decision=pending
func (s *Server) pageImport(w http.ResponseWriter, r *http.Request) {
	rows, sessStatus, err := s.collectImportRows(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Filters from the query string.
	pkg := strings.TrimSpace(r.URL.Query().Get("package"))
	decision := strings.TrimSpace(r.URL.Query().Get("decision"))
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	filtered := rows[:0:0]
	for _, row := range rows {
		if pkg != "" && !strings.Contains(strings.ToLower(row.Package), strings.ToLower(pkg)) {
			continue
		}
		if decision != "" {
			want := decision
			if decision == "pending" && row.Decision != "" {
				continue
			} else if decision != "pending" && row.Decision != want {
				continue
			}
		}
		if mode != "" && row.Mode != mode {
			continue
		}
		filtered = append(filtered, row)
	}
	counts := map[string]int{}
	for _, r := range rows {
		switch r.Decision {
		case "accepted", "rejected":
			counts[r.Decision]++
		default:
			counts["pending"]++
		}
	}
	s.render(w, "import.html", pageData{
		Title:    "Import session",
		Active:   "import",
		JSONHref: "/api/v1/import/candidates",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Import session"}},
		Body: map[string]interface{}{
			"Rows":         filtered,
			"Total":        len(rows),
			"Counts":       counts,
			"Filter":       map[string]string{"package": pkg, "decision": decision, "mode": mode},
			"SessionStatus": sessStatus,
		},
	})
}

// pageImportCandidate renders one candidate's bundle — the v0.2.0 shape
// `lattice import review <id>` prints, but click-throughable.
func (s *Server) pageImportCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, _, err := s.collectImportRows(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var row importCandidateRow
	found := false
	for _, x := range rows {
		if x.ID == id {
			row, found = x, true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	// Load the draft if present.
	var draft schema.Manifest
	draftPath := filepath.Join(s.ws.ImportDir(), importer.DraftsDirName, row.ID+".yaml")
	if m, err := schema.LoadManifest(draftPath); err == nil && m != nil {
		draft = *m
	}
	s.render(w, "import-candidate.html", pageData{
		Title:    row.ID,
		Active:   "import",
		JSONHref: "/api/v1/import/candidates",
		Breadcrumbs: []crumb{
			{Label: "Overview", Href: "/"},
			{Label: "Import session", Href: "/import"},
			{Label: row.ID},
		},
		Body: map[string]interface{}{"Row": row, "Draft": draft},
	})
}

// collectImportRows is the shared loader: reads candidates.json + session +
// per-candidate draft yaml (for mode + feature-id) and merges them.
func (s *Server) collectImportRows(r *http.Request) ([]importCandidateRow, string, error) {
	cf, err := importer.LoadCandidates(filepath.Join(s.ws.ImportDir(), importer.CandidatesFileName))
	if err != nil {
		// No import session yet — that's not an error, just an empty page.
		return nil, "", nil
	}
	sess, _ := importer.LoadSession(filepath.Join(s.ws.ImportDir(), importer.SessionFileName))
	draftsDir := filepath.Join(s.ws.ImportDir(), importer.DraftsDirName)

	rows := make([]importCandidateRow, 0, len(cf.Candidates))
	for _, c := range cf.Candidates {
		row := importCandidateRow{Candidate: c}
		if sess.Decisions != nil {
			row.Decision = sess.Decisions[c.ID]
		}
		// Best-effort load draft to get feature id and (when persisted) mode.
		if m, err := schema.LoadManifest(filepath.Join(draftsDir, c.ID+".yaml")); err == nil && m != nil {
			row.FeatureID = m.ID
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, sess.Status, nil
}

// --- API ---

func (s *Server) apiImportCandidates(w http.ResponseWriter, r *http.Request) {
	rows, _, err := s.collectImportRows(r)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, rows)
}

// apiImportCandidate returns one candidate's full bundle — symbols,
// evidence, draft manifest, decision. Feeds the v0.4.1 drawer so the
// reviewer can open and accept/reject without full-page navigation
// between candidates.
func (s *Server) apiImportCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, _, err := s.collectImportRows(r)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	var row importCandidateRow
	found := false
	for _, x := range rows {
		if x.ID == id {
			row, found = x, true
			break
		}
	}
	if !found {
		writeJSONError(w, errStr("no such candidate: "+id), http.StatusNotFound)
		return
	}
	// Load the draft manifest if present so the drawer can show it
	// without a second round trip.
	var draft *schema.Manifest
	draftPath := filepath.Join(s.ws.ImportDir(), importer.DraftsDirName, row.ID+".yaml")
	if m, err := schema.LoadManifest(draftPath); err == nil && m != nil {
		draft = m
	}
	writeJSON(w, map[string]interface{}{
		"candidate": row,
		"draft":     draft,
	})
}

// decisionPayload mirrors the CLI batch shape: candidate_id ->
// "accept" | "reject". Reusing the v0.2.1 batch driver keeps the UI
// and CLI byte-for-byte identical.
type decisionPayload struct {
	Decisions map[string]string `json:"decisions"`
}

func (s *Server) apiImportDecisions(w http.ResponseWriter, r *http.Request) {
	var p decisionPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	// Normalise (accept/accepted/ACCEPT -> DecisionAccepted, ditto reject).
	decisions := map[string]string{}
	for k, v := range p.Decisions {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "accept", "accepted":
			decisions[k] = importer.DecisionAccepted
		case "reject", "rejected":
			decisions[k] = importer.DecisionRejected
		default:
			writeJSONError(w, errBadAction(k, v), http.StatusBadRequest)
			return
		}
	}
	results, promoted, err := s.applyImportBatch(r, decisions)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{
		"results":          results,
		"promoted_parents": promoted,
	})
}

type errStr string

func (e errStr) Error() string { return string(e) }

func errBadAction(id, v string) error {
	return errStr("decision for " + id + ": unknown action " + v + " (use accept|reject)")
}
