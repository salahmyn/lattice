package ui

// @module-feature reachability

import (
	"net/http"

	"github.com/salahmyn/lattice/pkg/lattice/contract"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
)

// v2PromiseSlice is GET /api/v2/promises/{id}/slice: the record `lattice
// slice <id> --json` prints (lattice.slice/1), from the same function. An
// unknown promise is a 404, as it is a usage error on the CLI.
func (s *Server) v2PromiseSlice(wr http.ResponseWriter, r *http.Request) {
	w := s.loadOr500(wr, r)
	if w == nil {
		return
	}
	id := r.PathValue("id")
	row, ok := w.rows[id]
	if !ok {
		writeV2Error(wr, http.StatusNotFound, "PROMISE_NOT_FOUND", "no promise "+id+" (want <feature>:INV-n, as listed by lattice audit)", nil)
		return
	}
	rec := slice.Compute(w.sg, id, slice.EnforcersOf(row))
	rec.Commit = w.kg.GeneratedFromCommit
	writeV2(wr, http.StatusOK, rec, contract.LintOptions{})
}
