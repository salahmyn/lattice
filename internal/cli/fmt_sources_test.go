package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fmt keeps a BRD's sources, every field of each, in canonical order:
// sources after the existing fields, and kind, ref, title, url, checked_by,
// checked_at within a source. A second run changes nothing.
func TestFmtBRDSources(t *testing.T) {
	repo := t.TempDir()
	p := filepath.Join(repo, "lattice", "brds", "brd.fees.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, "lattice", "config.yaml"), []byte("{}\n"), 0o644)
	hand := "id: brd.fees\nversion: 1\nstatus: draft\ntitle: Fees\n" +
		"sources:\n" +
		"  - checked_at: 2026-11-10\n    url: https://tracker.example/WAL-12\n    ref: WAL-12\n    checked_by: ana@example.com\n    kind: ticket\n    title: Withdrawal fees\n" +
		"  - ref: ADR-7\n    kind: adr\n" +
		"business_problem: p\nprovenance:\n  source: human\n"
	if err := os.WriteFile(p, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := newFmtCommand(&IO{Repo: repo, Out: &out, Err: &out})
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		err := cmd.Execute()
		return out.String(), err
	}
	if out, err := run(); err != nil || strings.Contains(out, "skipped") {
		t.Fatalf("fmt: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(p)
	want := "provenance:\n  source: human\n" +
		"sources:\n" +
		"  - kind: ticket\n    ref: WAL-12\n    title: Withdrawal fees\n    url: https://tracker.example/WAL-12\n" +
		"    checked_by: ana@example.com\n    checked_at: \"2026-11-10\"\n" +
		"  - kind: adr\n    ref: ADR-7\n"
	if !strings.HasSuffix(string(got), want) {
		t.Fatalf("not canonical:\n%s\nwant suffix:\n%s", got, want)
	}
	if out, err := run("--check"); err != nil {
		t.Errorf("second --check should pass: %v\n%s", err, out)
	}
}
