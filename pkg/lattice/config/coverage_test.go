package config

import (
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, text string) (Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
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
