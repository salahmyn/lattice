package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Without a slice builder there is no reach graph: a promise with an
// enforcer reads unknown, one with none reads no_enforcer, and neither is
// drawn as a pass. The API answers 404 for a promise it does not know.
func TestSliceWithoutAReachGraph(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{ID: "pay", Version: 1, Status: schema.StatusProduction, Invariants: []schema.Invariant{
			{ID: "INV-1", Statement: "guard holds"}, {ID: "INV-2", Statement: "nothing enforces this"}}}},
		Symbols: []schema.GraphSymbol{{FQN: "pay.Guard", Feature: "pay", EnforcesInvariants: []string{"INV-1"}, File: "pay.go", Line: 4}},
	}
	srv := New(&workspace.Workspace{LatticeDir: t.TempDir()}, Options{
		GraphBuilder: func(context.Context) (schema.KnowledgeGraph, error) { return kg, nil },
	})
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	w := get("/audit?promise=pay:INV-1")
	for _, want := range []string{`class="audit-slice"`, "Slice (which ways in reach the enforcers", "unknown</span>", "there is no reach graph"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("audit page lacks %q:\n%s", want, w.Body.String())
		}
	}
	if w := get("/audit?promise=pay:INV-2"); !strings.Contains(w.Body.String(), "no_enforcer</span>") {
		t.Errorf("a promise with no enforcer:\n%s", w.Body.String())
	}

	api := get("/api/v2/promises/pay:INV-1/slice")
	if api.Code != 200 || !strings.Contains(api.Body.String(), `"contract": "lattice.slice/1"`) || !strings.Contains(api.Body.String(), `"reading": "unknown"`) {
		t.Errorf("api = %d %s", api.Code, api.Body.String())
	}
	if w := get("/api/v2/promises/pay:INV-9/slice"); w.Code != 404 || !strings.Contains(w.Body.String(), "PROMISE_NOT_FOUND") {
		t.Errorf("unknown promise = %d %s", w.Code, w.Body.String())
	}
}

func TestSliceBadgeIsNeverGreen(t *testing.T) {
	for _, reading := range []string{"complete", "unknown_from_hop", "unreached", "no_enforcer", "unknown"} {
		if b := sliceBadge(reading); b == "is-ok" || b == "is-ok-weak" {
			t.Errorf("%s is drawn %s: a slice says which ways in reach the code, not that the promise holds", reading, b)
		}
	}
}
