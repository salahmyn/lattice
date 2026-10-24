package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func load(t *testing.T, text string) (Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

func TestRequireLevelForms(t *testing.T) {
	cfg, err := load(t, "validate:\n  require_level: demonstrated\n")
	if err != nil || cfg.Validate.RequireLevel.For(3) != "demonstrated" || len(cfg.Validate.RequireLevel.Tier) != 0 {
		t.Fatalf("scalar: %+v %v", cfg.Validate.RequireLevel, err)
	}
	cfg, err = load(t, "validate:\n  require_level: {default: demonstrated, tier: {3: exercised}}\n")
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Validate.RequireLevel
	if r.For(1) != "demonstrated" || r.For(3) != "exercised" || r.String() != "default demonstrated, tier 3: exercised" {
		t.Errorf("per tier: %+v (%s)", r, r)
	}
	if _, err := load(t, "validate:\n  require_level: {defualt: demonstrated}\n"); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("a misspelt key must fail, got %v", err)
	}
	// The scalar form round-trips as a scalar, so configs keep their shape.
	out, _ := yaml.Marshal(ValidatePolicy{RequireLevel: RequireLevel{Default: "verified"}})
	if strings.TrimSpace(string(out)) != "require_level: verified" {
		t.Errorf("marshal = %q", out)
	}
	if out, _ := yaml.Marshal(ValidatePolicy{}); strings.Contains(string(out), "require_level") {
		t.Errorf("an unset level is omitted: %q", out)
	}
}

func TestCoverageBlock(t *testing.T) {
	cfg, err := load(t, "mutation_testing:\n  enabled: false\n")
	if err != nil || cfg.CoverageConfigured() {
		t.Fatalf("no block: configured=%v %v", cfg.CoverageConfigured(), err)
	}
	cfg, err = load(t, "coverage: {}\n")
	if err != nil || !cfg.CoverageConfigured() || cfg.CoverageConfig().Floors.Declared() || !cfg.CoverageConfig().PerTest.OnlyVerifiersOrDefault() {
		t.Fatalf("empty block: %+v %v", cfg.Coverage, err)
	}
	cfg, err = load(t, `coverage:
  reports:
    - {path: coverage/lcov.info, format: lcov, root: apps/web, include: ["src/**"]}
  per_test:
    only_verifiers: false
    dirs:
      - {dir: coverage/per-test}
      - {dir: coverage/per-file, basis: per_file}
  floors: {feature: 60, invariant: 80, tier: {2: 90, 3: 100}}
`)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.CoverageConfig()
	if len(c.Reports) != 1 || c.Reports[0].Root != "apps/web" || c.PerTest.OnlyVerifiersOrDefault() ||
		len(c.PerTest.Dirs) != 2 || c.PerTest.Dirs[1].Basis != "per_file" || c.Floors.Tier[3] != 100 || !c.Floors.Declared() {
		t.Errorf("coverage = %+v", c)
	}
}
