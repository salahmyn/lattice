package entrypoints

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// TestMergePersistedWins covers the v0.3.1 promise: a human-authored
// purpose on a persisted EP must NOT be overwritten by the detector
// when the next extract runs. The detector's runtime-derived flow
// data, however, fills in when the persisted EP is silent on flows.
func TestMergePersistedWins(t *testing.T) {
	detected := []schema.EntryPoint{{
		ID:      "ep.http.post.refunds",
		Kind:    schema.EntryPointKindHTTP,
		Purpose: "detector-fallback purpose",
		Flow: []schema.FlowStep{{Feature: "checkout.refund"}},
	}}
	persisted := []schema.EntryPoint{{
		ID:      "ep.http.post.refunds",
		Kind:    schema.EntryPointKindHTTP,
		Purpose: "Customer requests a refund.", // human-authored
		// no flow — should pull from detector
	}}
	merged := Merge(detected, persisted)
	if len(merged) != 1 {
		t.Fatalf("merge produced %d, want 1", len(merged))
	}
	if merged[0].Purpose != "Customer requests a refund." {
		t.Errorf("persisted purpose lost: %q", merged[0].Purpose)
	}
	if len(merged[0].Flow) != 1 || merged[0].Flow[0].Feature != "checkout.refund" {
		t.Errorf("detector flow not filled in: %v", merged[0].Flow)
	}
}

func TestSaveAndLoadEntryPoint(t *testing.T) {
	dir := t.TempDir()
	ep := schema.EntryPoint{
		ID: "ep.cli.app.reminder", Kind: schema.EntryPointKindCLI,
		Status:  schema.StatusProposal, Version: 1,
		Trigger: schema.Trigger{Command: "app:reminder"},
		Handler: schema.Handler{Symbol: `App\Console\ReminderCmd::handle`},
		Purpose: "Sends a daily merchant reminder.",
	}
	path, err := SaveEntryPoint(dir, ep)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "ep", "cli", "app", "reminder.yaml"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not written: %v", err)
	}
	loaded, err := LoadEntryPoints(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, []schema.EntryPoint{ep}) {
		t.Errorf("round-trip lost data:\nloaded:    %+v\noriginal: %+v", loaded, ep)
	}
}

func TestLoadEntryPointsMissingDir(t *testing.T) {
	got, err := LoadEntryPoints(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Errorf("missing dir should be tolerated, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}
