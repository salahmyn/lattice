package ui

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
)

// apiEPDecision is the UI mirror of `lattice ep accept|reject`. Same
// underlying Decide() helper so CLI and UI mutations land identical
// state on disk.
//
// Path: PUT /api/v1/entry-points/{id}/decision
// Body: {"decision": "accept" | "reject"}
func (s *Server) apiEPDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, errStr("missing entry-point id"), http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	res, err := entrypoints.Decide(s.ws.EntryPointsDir(), id, req.Decision)
	if err != nil {
		writeJSONError(w, err, http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, res)
}
