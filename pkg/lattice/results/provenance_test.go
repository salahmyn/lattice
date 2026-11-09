package results

import (
	"os"
	"path/filepath"
	"testing"
)

// @verifies results:INV-6
func TestIngestStampsCIRun(t *testing.T) {
	for _, v := range ciRunVars {
		t.Setenv(v, "")
	}
	dir := t.TempDir()
	report := filepath.Join(dir, "r.xml")
	if err := os.WriteFile(report, []byte(sampleJUnit), 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := Ingest(dir, "abc123", report)
	if err != nil {
		t.Fatal(err)
	}
	if set.CIRun != LocalRun || Load(dir).CIRun != LocalRun {
		t.Fatalf("no CI env: ci_run = %q, want %q", set.CIRun, LocalRun)
	}

	t.Setenv("GITHUB_RUN_ID", "4812")
	if got := CIRunID(); got != "4812" {
		t.Fatalf("GITHUB_RUN_ID: %q", got)
	}
	if _, err := Ingest(dir, "abc123", report); err != nil {
		t.Fatal(err)
	}
	if got := Load(dir); got.CIRun != "4812" || got.Commit != "abc123" {
		t.Fatalf("stamp = %q @ %q, want 4812 @ abc123", got.CIRun, got.Commit)
	}
}
