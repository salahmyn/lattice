package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// The audit page lists a row per promise with filters by class and feature,
// opens each fake, and never gives a non-positive class the positive colour.
// @verifies audit:INV-2
func TestAuditPage(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".cache", "results")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	bf := `{"schema":"lattice.breaks/1","runs":[
	  {"promise":"pay:INV-1","enforcer":"pay.Guard","method":"early_return","baseline":"pass","outcome":"survived"},
	  {"promise":"pay:INV-2","enforcer":"pay.Cap","method":"early_return","baseline":"pass","outcome":"killed"}]}`
	if err := os.WriteFile(filepath.Join(cache, "breaks.json"), []byte(bf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit-notes.yaml"),
		[]byte("- {promise: 'pay:INV-1', reader: ana, verdict: confirmed_fake, note: stubbed guard}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{
			{ID: "pay", Version: 1, Status: schema.StatusProduction, Invariants: []schema.Invariant{
				{ID: "INV-1", Statement: "guard holds"}, {ID: "INV-2", Statement: "cap holds"}}},
			{ID: "ship", Version: 1, Status: schema.StatusProduction, Invariants: []schema.Invariant{{ID: "INV-1", Statement: "ships"}}},
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "pay.Guard", Feature: "pay", EnforcesInvariants: []string{"INV-1"}, File: "pay.go", Line: 4},
			{FQN: "pay.Cap", Feature: "pay", EnforcesInvariants: []string{"INV-2"}, File: "pay.go", Line: 9},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "pay.TestGuard", Feature: "pay", IsTest: true, Verifies: []string{"INV-1"}},
			{FQN: "pay.TestCap", Feature: "pay", IsTest: true, Verifies: []string{"INV-2"}},
		},
	}
	srv := New(&workspace.Workspace{LatticeDir: dir}, Options{
		GraphBuilder: func(context.Context) (schema.KnowledgeGraph, error) { return kg, nil },
	})
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	w := get("/audit")
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("/audit = %d %s", w.Code, body)
	}
	for _, want := range []string{"pay:INV-1", "pay:INV-2", "ship:INV-1", "passes_when_broken", "stubbed guard", "pay.TestGuard", "pay.Guard"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(body, "<details class=\"audit-row bg-white rounded-lg border border-slate-200\" open>") {
		t.Error("a fake row must open by default")
	}
	// The fake is drawn as an error, never as ok.
	if i := strings.Index(body, "pay:INV-1"); i < 0 || !strings.Contains(body[:i], "badge is-error\">fake") {
		t.Error("fake must carry the error badge")
	}
	// Positive colours belong to break_tested alone here (pay:INV-2): the
	// chip and the row; no row of another class takes an ok badge.
	if n := strings.Count(body, "badge is-ok\">break_tested"); n != 2 || strings.Count(body, "badge is-ok\">") != 2 {
		t.Errorf("positive badges: %d", strings.Count(body, "badge is-ok\">"))
	}
	if body := get("/audit?class=fake").Body.String(); !strings.Contains(body, "pay:INV-1") || strings.Contains(body, "pay:INV-2</span>") {
		t.Error("class filter")
	}
	if body := get("/audit?feature=ship").Body.String(); strings.Contains(body, "pay:INV-1</span>") || !strings.Contains(body, "ship:INV-1") {
		t.Error("feature filter")
	}

	var rep audit.Report
	if w := get("/api/v1/audit?class=fake"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rep) != nil ||
		rep.Schema != audit.Schema || len(rep.Rows) != 1 || rep.Rows[0].Kind != audit.KindPassesWhenBroken {
		t.Errorf("/api/v1/audit = %d %s", w.Code, w.Body.String())
	}
}
