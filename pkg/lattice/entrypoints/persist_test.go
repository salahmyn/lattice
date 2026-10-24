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
		Flow:    []schema.FlowStep{{Feature: "checkout.refund"}},
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
		Status: schema.StatusProposal, Version: 1,
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

// A file that does not parse, or has no id, is reported instead of dropped.
func TestLoadEntryPointsReport(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"ok.yaml":   "id: http.get.ok\nkind: http_route\n",
		"bad.yaml":  "id: http.get.admin\nkind: http_route\npurpose: The console at /admin: totals\n",
		"noid.yaml": "kind: cron\n",
		"notes.txt": "ignored",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	eps, problems, err := LoadEntryPointsReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].ID != "http.get.ok" {
		t.Errorf("eps = %+v", eps)
	}
	got := map[string]LoadProblem{}
	for _, p := range problems {
		got[filepath.Base(p.Path)] = p
	}
	if p, ok := got["bad.yaml"]; !ok || p.Line != 3 {
		t.Errorf("bad.yaml problem = %+v (want line 3)", p)
	}
	if _, ok := got["noid.yaml"]; !ok || len(problems) != 2 {
		t.Errorf("problems = %+v", problems)
	}
}
