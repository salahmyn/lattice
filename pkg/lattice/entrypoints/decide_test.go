package entrypoints

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// TestDecideAcceptFlipStatus proves accept rewrites status to
// production without touching any other manifest field — purpose,
// flow, owners all round-trip identical.
func TestDecideAcceptFlipStatus(t *testing.T) {
	dir := t.TempDir()
	ep := schema.EntryPoint{
		ID: "ep.http.post.refunds", Kind: schema.EntryPointKindHTTP,
		Version: 1, Status: schema.StatusProposal,
		Purpose: "Customer requests a refund.",
		Trigger: schema.Trigger{Method: "POST", Path: "/refunds"},
		Handler: schema.Handler{Symbol: `App\Http\RefundsCtrl::store`},
	}
	if _, err := SaveEntryPoint(dir, ep); err != nil {
		t.Fatal(err)
	}
	res, err := Decide(dir, ep.ID, DecisionAcceptEP)
	if err != nil {
		t.Fatalf("decide accept: %v", err)
	}
	if res.NewStatus != string(schema.StatusProduction) {
		t.Errorf("status flip = %q, want production", res.NewStatus)
	}
	loaded, err := LoadEntryPoints(dir)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("reload: got %d eps, err=%v", len(loaded), err)
	}
	if loaded[0].Status != schema.StatusProduction {
		t.Errorf("disk status = %q, want production", loaded[0].Status)
	}
	if loaded[0].Purpose != ep.Purpose {
		t.Errorf("purpose corrupted by accept: %q", loaded[0].Purpose)
	}
}

// TestDecideRejectArchive proves reject moves the manifest under
// .rejected/ (preserving the directory shape) and that LoadEntryPoints
// no longer returns it — but the bytes are recoverable.
func TestDecideRejectArchive(t *testing.T) {
	dir := t.TempDir()
	ep := schema.EntryPoint{
		ID: "ep.cli.app.junk", Kind: schema.EntryPointKindCLI,
		Version: 1, Status: schema.StatusProposal,
		Trigger: schema.Trigger{Command: "app:junk"},
		Handler: schema.Handler{Symbol: `App\Console\JunkCmd::handle`},
	}
	if _, err := SaveEntryPoint(dir, ep); err != nil {
		t.Fatal(err)
	}
	res, err := Decide(dir, ep.ID, DecisionRejectEP)
	if err != nil {
		t.Fatalf("decide reject: %v", err)
	}
	wantPath := filepath.Join(dir, ".rejected", "ep", "cli", "app", "junk.yaml")
	if res.ArchivedAt != wantPath {
		t.Errorf("archive path = %s, want %s", res.ArchivedAt, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("rejected archive missing: %v", err)
	}
	loaded, _ := LoadEntryPoints(dir)
	if len(loaded) != 0 {
		t.Errorf("rejected EP must not appear in LoadEntryPoints, got %d", len(loaded))
	}
}

// TestDecideUnknownAction returns a usable error rather than half-
// writing anything — pre-condition for the UI's HTTP 422 path.
func TestDecideUnknownAction(t *testing.T) {
	dir := t.TempDir()
	_, _ = SaveEntryPoint(dir, schema.EntryPoint{ID: "ep.x", Kind: "x", Version: 1})
	_, err := Decide(dir, "ep.x", "maybe")
	if err == nil || !strings.Contains(err.Error(), "decision must be") {
		t.Errorf("expected unknown-action error, got %v", err)
	}
}
