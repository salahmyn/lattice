package ui

import (
	"encoding/json"
	"net/http"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// overviewPayload is the shape behind GET /api/v1/overview — the data a
// dashboard card displays without further drilling. Counts come from
// the same KnowledgeGraph every other view reads, so the UI never
// disagrees with the CLI.
type overviewPayload struct {
	Mode            string `json:"mode"` // "embedded" | "standalone" | "review"
	LatticeDir      string `json:"lattice_dir"`
	CodeRoots       []root `json:"code_roots"`
	Counts          counts `json:"counts"`
	GeneratedAt     string `json:"generated_at,omitempty"`
	SchemaVersion   string `json:"schema_version,omitempty"`
	ReviewMode      bool   `json:"review_mode,omitempty"`
}

type root struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Available bool   `json:"available"`
}

type counts struct {
	Features    int                `json:"features"`
	EntryPoints int                `json:"entry_points"`
	EPByKind    map[string]int     `json:"entry_points_by_kind,omitempty"`
	Symbols     int                `json:"symbols"`
	Tests       int                `json:"tests"`
	Violations  int                `json:"violations"`
}

func (s *Server) apiOverview(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	payload := overviewPayload{
		Mode:          string(s.ws.Mode),
		LatticeDir:    s.ws.LatticeDir,
		GeneratedAt:   kg.GeneratedAt,
		SchemaVersion: kg.SchemaVersion,
		ReviewMode:    kg.Review,
		Counts: counts{
			Features:    len(kg.Features),
			EntryPoints: len(kg.EntryPoints),
			Symbols:     len(kg.Symbols),
			Tests:       len(kg.Tests),
			Violations:  len(kg.Violations),
			EPByKind:    map[string]int{},
		},
	}
	for _, ep := range kg.EntryPoints {
		payload.Counts.EPByKind[ep.Kind]++
	}
	for _, r := range s.ws.CodeRoots {
		payload.CodeRoots = append(payload.CodeRoots, root{Name: r.Name, Path: r.Path, Available: r.Available})
	}
	writeJSON(w, payload)
}

func (s *Server) apiFeatures(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, kg.Features)
}

func (s *Server) apiFeature(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	for _, m := range kg.Features {
		if m.ID == id {
			// Augment with the entry points that reach this feature —
			// the inverse direction the UI cares about most.
			payload := struct {
				schema.Manifest
				ReachedBy []schema.EntryPoint `json:"reached_by,omitempty"`
			}{Manifest: m, ReachedBy: featuresReachedBy(id, kg.EntryPoints)}
			writeJSON(w, payload)
			return
		}
	}
	http.Error(w, "feature not found", http.StatusNotFound)
}

func (s *Server) apiEntryPoints(w http.ResponseWriter, r *http.Request) {
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, kg.EntryPoints)
}

func (s *Server) apiEntryPoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kg, err := s.graph(r.Context())
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	for _, ep := range kg.EntryPoints {
		if ep.ID == id {
			writeJSON(w, ep)
			return
		}
	}
	http.Error(w, "entry point not found", http.StatusNotFound)
}

// featuresReachedBy is the inverse of EntryPoint.Flow: given a feature
// id, return every entry point whose flow visits it. Cheap because the
// UI builds the graph once per request anyway.
func featuresReachedBy(featureID string, eps []schema.EntryPoint) []schema.EntryPoint {
	var out []schema.EntryPoint
	for _, ep := range eps {
		for _, step := range ep.Flow {
			if step.Feature == featureID {
				out = append(out, ep)
				break
			}
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeJSONError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
