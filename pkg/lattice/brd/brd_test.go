package brd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestLoadAllEmptyDirIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	brds, viol := LoadAll(filepath.Join(dir, "nonexistent"), dir)
	if len(brds) != 0 || len(viol) != 0 {
		t.Fatalf("expected empty, got %d brds / %d violations", len(brds), len(viol))
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	b := schema.BRD{
		ID: "brd.checkout", Version: 1, Status: schema.BRDDraft,
		Title:           "Checkout refunds",
		BusinessProblem: "Reduce CS volume.",
		ImplementsVia:   []string{"checkout.refund"},
	}
	path, err := Save(dir, b)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if filepath.Base(path) != "brd.checkout.yaml" {
		t.Errorf("file name = %s, want brd.checkout.yaml", filepath.Base(path))
	}
	// Second save without SaveForce must fail — refusing to clobber is
	// the contract that `lattice brd new` relies on.
	if _, err := Save(dir, b); err == nil {
		t.Error("expected Save to refuse to overwrite")
	}

	loaded, viol := LoadAll(dir, dir)
	if len(viol) != 0 {
		t.Fatalf("unexpected violations: %v", viol)
	}
	if len(loaded) != 1 || loaded[0].ID != b.ID {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded[0].ImplementsVia[0] != "checkout.refund" {
		t.Errorf("implements_via dropped: %+v", loaded[0])
	}
}

func TestSaveForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	b := schema.BRD{
		ID: "brd.checkout", Version: 1, Status: schema.BRDDraft,
		Title: "v1", BusinessProblem: "p",
	}
	if _, err := Save(dir, b); err != nil {
		t.Fatal(err)
	}
	b.Version = 2
	b.Title = "v2"
	if _, err := SaveForce(dir, b); err != nil {
		t.Fatalf("SaveForce: %v", err)
	}
	got, _ := schema.LoadBRD(PathFor(dir, b.ID))
	if got.Version != 2 || got.Title != "v2" {
		t.Errorf("SaveForce didn't overwrite: %+v", got)
	}
}

func TestFeaturesByBRDUnionsForwardAndReverse(t *testing.T) {
	brds := []schema.BRD{{
		ID: "brd.x", ImplementsVia: []string{"a"},
	}}
	features := []schema.Manifest{
		{ID: "a"}, // forward link from BRD
		{ID: "b", ImplementsBRD: "brd.x"}, // reverse link from feature
	}
	got := FeaturesByBRD(brds, features)
	if len(got["brd.x"]) != 2 || got["brd.x"][0] != "a" || got["brd.x"][1] != "b" {
		t.Errorf("expected [a b], got %v", got["brd.x"])
	}
}

func TestLoadAllReportsParseFailureAsViolation(t *testing.T) {
	dir := t.TempDir()
	// Garbage YAML — mismatched indentation under a mapping that
	// makes the parser fail outright.
	bad := "id: brd.x\nversion: 1\nstakeholders:\n  business_owner: x\n bad_indent: y\n"
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	brds, viol := LoadAll(dir, dir)
	if len(brds) != 0 {
		t.Errorf("expected no brds, got %d", len(brds))
	}
	if len(viol) != 1 || viol[0].Code != schema.CodeBRDSchema {
		t.Errorf("expected one BRD_SCHEMA violation, got %+v", viol)
	}
}
