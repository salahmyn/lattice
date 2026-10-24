package config

// @module-feature workspace

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Coverage is the `coverage:` block of config.yaml (docs/rnd/0009). Its
// presence, even as `coverage: {}`, turns on the coverage findings at their
// default severities; without it they stay off. `lattice results ingest`
// reads Reports and PerTest.Dirs when it is given no paths.
type Coverage struct {
	// Reports are whole-suite coverage reports, read when `results ingest`
	// is given no paths. A coverage.py JSON report written with
	// --show-contexts also carries per-test data.
	Reports []CoverageReport `yaml:"reports,omitempty"`
	// PerTest says how per-test coverage is collected and where it lies.
	PerTest CoveragePerTest `yaml:"per_test,omitempty"`
	// Floors are minimum line-coverage percentages. Declaring one turns on
	// COVERAGE_BELOW_FLOOR for it.
	Floors CoverageFloors `yaml:"floors,omitempty"`
	// Roots are code the runtime reaches by a convention the call graph
	// cannot see (a route table, a framework hook, a library's exported
	// API), added to the automatic roots of the reachability check.
	Roots []CoverageRoot `yaml:"roots,omitempty"`
	// Suites group test files for SUITE_OVERLAP: suite name -> globs over
	// the graph's file paths. Unset, each top-level test directory (tests/,
	// e2e/, ...) is one suite.
	Suites map[string][]string `yaml:"suites,omitempty"`
	// Overlap is the Jaccard index at or above which two suites overlap
	// (SUITE_OVERLAP). 0 means the default, 0.8.
	Overlap float64 `yaml:"overlap,omitempty"`
}

// CoverageRoot declares one root of the reachability check: a symbol (an
// FQN, or a glob over FQNs) or a file (a glob over the graph's file paths,
// whose top-level code and exported symbols become roots). Reason says why
// the runtime reaches it.
type CoverageRoot struct {
	Symbol string `yaml:"symbol,omitempty"`
	File   string `yaml:"file,omitempty"`
	Reason string `yaml:"reason,omitempty"`
}

// DefaultOverlap is the suite-overlap threshold when coverage.overlap is unset.
const DefaultOverlap = 0.8

// OverlapOrDefault returns coverage.overlap, or DefaultOverlap when unset.
func (c Coverage) OverlapOrDefault() float64 {
	if c.Overlap <= 0 {
		return DefaultOverlap
	}
	return c.Overlap
}

// CoverageReport locates one coverage report and says how to read its paths.
type CoverageReport struct {
	// Path is the report file, relative to the directory holding lattice/.
	Path string `yaml:"path"`
	// Format is lcov, go, istanbul, coveragepy, cobertura, clover or
	// pg_stat_functions. Empty means detect it from the content.
	Format string `yaml:"format,omitempty"`
	// Root is the prefix the report's file paths are relative to, from the
	// directory holding lattice/ (e.g. apps/web for a package-local LCOV).
	Root string `yaml:"root,omitempty"`
	// Include are the files the run instrumented, as globs relative to Root.
	// A file inside this set that the report does not list counts as zero
	// hits; any other absent file has no data.
	Include []string `yaml:"include,omitempty"`
}

// CoveragePerTest configures per-test coverage, the only evidence that can
// make a criterion exercised.
type CoveragePerTest struct {
	// OnlyVerifiers keeps per-test reports of @verifies tests only (default
	// true). A source that holds every test in one run anyway (coverage.py
	// contexts, a Postgres call-count export) is always kept whole.
	OnlyVerifiers *bool `yaml:"only_verifiers,omitempty"`
	// Dirs hold one report per test, named by the test's slug
	// (`lattice results tests` prints it), or, with basis per_file, one
	// report per test file.
	Dirs []CoverageDir `yaml:"dirs,omitempty"`
}

// CoverageDir is a directory of per-test (or per-file) reports.
type CoverageDir struct {
	// Dir is relative to the directory holding lattice/.
	Dir string `yaml:"dir"`
	// Basis is per_test (default: one report per test) or per_file (one
	// report per test file, named by the file's slug).
	Basis   string   `yaml:"basis,omitempty"`
	Format  string   `yaml:"format,omitempty"`
	Root    string   `yaml:"root,omitempty"`
	Include []string `yaml:"include,omitempty"`
}

// CoverageFloors are minimum line coverage percentages (0-100). A floor of
// 0 is not declared.
type CoverageFloors struct {
	// Feature: the body lines of a feature's runtime symbols, over the suite.
	Feature float64 `yaml:"feature,omitempty"`
	// Invariant: the body lines of an invariant's enforcers, over what its
	// verifiers ran when each has per-test data, else over the suite.
	Invariant float64 `yaml:"invariant,omitempty"`
	// Tier: per BRD criterion tier, the worst of the criterion's invariants.
	Tier map[int]float64 `yaml:"tier,omitempty"`
}

// Declared reports whether any floor is set.
func (f CoverageFloors) Declared() bool {
	if f.Feature > 0 || f.Invariant > 0 {
		return true
	}
	for _, v := range f.Tier {
		if v > 0 {
			return true
		}
	}
	return false
}

// OnlyVerifiersOrDefault returns per_test.only_verifiers, true when unset.
func (p CoveragePerTest) OnlyVerifiersOrDefault() bool {
	return p.OnlyVerifiers == nil || *p.OnlyVerifiers
}

// CoverageConfig returns the coverage block, or its zero value when absent.
func (c Config) CoverageConfig() Coverage {
	if c.Coverage == nil {
		return Coverage{}
	}
	return *c.Coverage
}

// CoverageConfigured reports whether config.yaml has a coverage: block.
func (c Config) CoverageConfigured() bool { return c.Coverage != nil }

// RequireLevel is validate.require_level: one truth level for every BRD
// criterion and scenario, or a default plus per-tier levels:
//
//	require_level: demonstrated
//	require_level: {default: demonstrated, tier: {3: exercised}}
//
// A criterion uses the level of its tier when one is set, else Default.
// Scenarios have no tier and use Default.
type RequireLevel struct {
	Default string         `yaml:"default,omitempty"`
	Tier    map[int]string `yaml:"tier,omitempty"`
}

// UnmarshalYAML accepts a scalar level or the {default, tier} mapping.
// An unknown key in the mapping is an error, so a misspelt key cannot
// silently drop the gate.
func (r *RequireLevel) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		r.Default = n.Value
		r.Tier = nil
		return nil
	case yaml.MappingNode:
		*r = RequireLevel{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i].Value, n.Content[i+1]
			switch key {
			case "default":
				if err := val.Decode(&r.Default); err != nil {
					return fmt.Errorf("validate.require_level.default: %w", err)
				}
			case "tier":
				if err := val.Decode(&r.Tier); err != nil {
					return fmt.Errorf("validate.require_level.tier: want {<tier>: <level>}: %w", err)
				}
			default:
				return fmt.Errorf("validate.require_level: unknown key %q (want default, tier)", key)
			}
		}
		return nil
	}
	return fmt.Errorf("validate.require_level: want a level or {default, tier}")
}

// MarshalYAML writes the scalar form when no tier level is set.
func (r RequireLevel) MarshalYAML() (interface{}, error) {
	if len(r.Tier) == 0 {
		return r.Default, nil
	}
	type plain RequireLevel
	return plain(r), nil
}

// IsZero lets omitempty drop an unset level.
func (r RequireLevel) IsZero() bool { return r.Default == "" && len(r.Tier) == 0 }

// For returns the level a criterion of the given tier must reach, "" for none.
func (r RequireLevel) For(tier int) string {
	if l, ok := r.Tier[tier]; ok {
		return l
	}
	return r.Default
}

// String renders the level for messages: the scalar, or "default X, tier N: Y".
func (r RequireLevel) String() string {
	if len(r.Tier) == 0 {
		return r.Default
	}
	tiers := make([]int, 0, len(r.Tier))
	for t := range r.Tier {
		tiers = append(tiers, t)
	}
	sort.Ints(tiers)
	parts := []string{}
	if r.Default != "" {
		parts = append(parts, "default "+r.Default)
	}
	for _, t := range tiers {
		parts = append(parts, "tier "+strconv.Itoa(t)+": "+r.Tier[t])
	}
	return strings.Join(parts, ", ")
}
