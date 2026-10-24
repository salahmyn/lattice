package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// require_level per tier: a tier-3 criterion must be exercised while the
// rest need only the default, and a demonstrated row below exercised says
// what the coverage lacks.
// @verifies validation:INV-12
func TestRequireLevelPerTier(t *testing.T) {
	kg := levelGraph()
	kg.BRDs[0].SuccessCriteria[0].Tier = 3 // SC-1, demonstrated once T1 passes
	passT1 := func(fqn string) (bool, bool) { return true, fqn == "T1" }
	cfg := config.Default()
	cfg.Validate.RequireLevel = config.RequireLevel{Default: "verified", Tier: map[int]string{3: "exercised"}}

	v := Validate(kg, cfg, Options{ResultOf: passT1})
	if got := belowLevel(v); got != "brd.x:SC-1 brd.x:SC-3" {
		t.Fatalf("no coverage: BRD_BELOW_LEVEL = %q", got)
	}
	msg := ""
	for _, f := range v {
		if f.Code == schema.CodeBRDBelowLevel && f.Subject == "brd.x:SC-1" {
			msg = f.Message
		}
	}
	if !strings.Contains(msg, "below the required exercised for tier 3: no per-test coverage ingested") {
		t.Errorf("message = %q", msg)
	}

	// T1's own run executed the enforcer S: SC-1 is exercised.
	ran := coverage.NewIndex(coverage.Set{PerTest: map[string][]string{"T1": {"S"}}}, kg)
	if got := belowLevel(Validate(kg, cfg, Options{ResultOf: passT1, Coverage: ran})); got != "brd.x:SC-3" {
		t.Errorf("exercised: BRD_BELOW_LEVEL = %q, want brd.x:SC-3", got)
	}
	// T1 ran, but not S: still demonstrated, and the reason says so.
	missed := coverage.NewIndex(coverage.Set{PerTest: map[string][]string{"T1": {}}}, kg)
	for _, f := range Validate(kg, cfg, Options{ResultOf: passT1, Coverage: missed}) {
		if f.Code == schema.CodeBRDBelowLevel && f.Subject == "brd.x:SC-1" &&
			!strings.Contains(f.Message, "no verifier's per-test coverage executed an enforcer") {
			t.Errorf("missed: %q", f.Message)
		}
	}

	// The scalar form and the flag still work: exercised for everything.
	cfg.Validate.RequireLevel = config.RequireLevel{Default: "exercised"}
	if got := belowLevel(Validate(kg, cfg, Options{ResultOf: passT1, Coverage: ran})); got != "brd.x:SC-2 brd.x:SC-3 brd.x:US-1" {
		t.Errorf("exercised everywhere: %q", got)
	}

	for _, bad := range []config.RequireLevel{
		{Tier: map[int]string{4: "exercised"}},
		{Default: "demonstrated", Tier: map[int]string{3: "shipped"}},
	} {
		cfg.Validate.RequireLevel = bad
		if !codes(Validate(kg, cfg, Options{}))[schema.CodeValidatePolicy] {
			t.Errorf("%+v should raise VALIDATE_POLICY", bad)
		}
	}
}

// coverageGraph: one invariant whose only verifier's run missed its enforcer,
// with a stale stamp and a floor to breach.
func coverageGraph() (schema.KnowledgeGraph, *coverage.Index) {
	feat := brdManifest("checkout")
	feat.Invariants = []schema.Invariant{{ID: "INV-1", Statement: "a"}, {ID: "INV-2", Statement: "b"}}
	kg := schema.KnowledgeGraph{
		GeneratedFromCommit: "c1",
		Features:            []schema.Manifest{feat},
		Symbols: []schema.GraphSymbol{
			{FQN: "S", Kind: "function", File: "s.js", Line: 1, EndLine: 4, Feature: "checkout", EnforcesInvariants: []string{"INV-1"}},
			{FQN: "S2", Kind: "function", File: "s.js", Line: 6, EndLine: 8, Feature: "checkout", EnforcesInvariants: []string{"INV-2"}},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "T1", IsTest: true, File: "t.js", Verifies: []string{"checkout:INV-1"}},
			{FQN: "T2", IsTest: true, File: "t.js", Verifies: []string{"checkout:INV-2"}},
		},
	}
	set := coverage.Set{Commit: "c0", PerTest: map[string][]string{"T1": {}},
		Symbols: map[string]coverage.SymbolCov{"S": {Lines: 3, Hit: 0}, "S2": {Lines: 2, Hit: 2, Executed: true}}}
	return kg, coverage.NewIndex(set, kg)
}

func severities(v []schema.Violation) map[string]schema.Severity {
	out := map[string]schema.Severity{}
	for _, f := range v {
		switch f.Code {
		case schema.CodeVerifierMissesEnforcer, schema.CodeVerifierUnproven, schema.CodeCoverageStale, schema.CodeCoverageBelowFloor:
			out[f.Code] = f.Severity
		}
	}
	return out
}

// The coverage codes are off until config.yaml has a coverage: block; then
// they take their default severities, a floor turns COVERAGE_BELOW_FLOOR on,
// and an explicit validate.severity entry still wins. No data, no finding.
// @verifies validation:INV-11
func TestCoveragePolicy(t *testing.T) {
	kg, ix := coverageGraph()

	if got := severities(Validate(kg, config.Default(), Options{Coverage: ix})); len(got) != 0 {
		t.Errorf("no coverage: block: every coverage code is off, got %v", got)
	}

	cfg := config.Default()
	cfg.Coverage = &config.Coverage{}
	got := severities(Validate(kg, cfg, Options{Coverage: ix}))
	want := map[string]schema.Severity{
		schema.CodeVerifierMissesEnforcer: schema.SeverityWarning,
		schema.CodeCoverageStale:          schema.SeverityWarning,
		schema.CodeVerifierUnproven:       schema.SeverityInfo,
	}
	if len(got) != len(want) {
		t.Errorf("coverage: {} = %v, want %v", got, want)
	}
	for code, sev := range want {
		if got[code] != sev {
			t.Errorf("%s = %q, want %q", code, got[code], sev)
		}
	}

	cfg.Coverage.Floors = config.CoverageFloors{Invariant: 80}
	if got := severities(Validate(kg, cfg, Options{Coverage: ix})); got[schema.CodeCoverageBelowFloor] != schema.SeverityWarning {
		t.Errorf("a declared floor turns COVERAGE_BELOW_FLOOR on: %v", got)
	}

	cfg.Validate.Severity = map[string]string{schema.CodeVerifierMissesEnforcer: "error", schema.CodeCoverageStale: "off"}
	got = severities(Validate(kg, cfg, Options{Coverage: ix}))
	if got[schema.CodeVerifierMissesEnforcer] != schema.SeverityError || got[schema.CodeCoverageStale] != "" {
		t.Errorf("explicit severities win: %v", got)
	}

	// Explicit severity without a coverage: block also turns a code on.
	only := config.Default()
	only.Validate.Severity = map[string]string{schema.CodeVerifierMissesEnforcer: "warning"}
	if got := severities(Validate(kg, only, Options{Coverage: ix})); len(got) != 1 || got[schema.CodeVerifierMissesEnforcer] != schema.SeverityWarning {
		t.Errorf("explicit opt-in: %v", got)
	}

	// No coverage ingested: nothing fires, whatever the policy.
	if got := severities(Validate(kg, cfg, Options{})); len(got) != 0 {
		t.Errorf("no data: %v", got)
	}
}
