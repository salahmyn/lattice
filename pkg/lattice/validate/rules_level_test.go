package validate

import (
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// levelGraph has three criteria (demonstrated once T1 passes, verified, and
// unmapped) and one scenario verified by T1.
func levelGraph() schema.KnowledgeGraph {
	feat := brdManifest("checkout")
	feat.Invariants = []schema.Invariant{{ID: "INV-1", Statement: "a"}, {ID: "INV-2", Statement: "b"}}
	return schema.KnowledgeGraph{
		BRDs: []schema.BRD{{
			ID: "brd.x", Version: 1, Status: schema.BRDApproved,
			Title: "x", BusinessProblem: "p",
			ImplementsVia: []string{"checkout"},
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "a", MapsToInvariant: "checkout:INV-1"},
				{ID: "SC-2", Statement: "b", MapsToInvariant: "checkout:INV-2"},
				{ID: "SC-3", Statement: "c"},
			},
			UserScenarios: []schema.UserScenario{{ID: "US-1", Actor: "buyer", VerifiedBy: []string{"T1"}}},
			Approval:      &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 1},
		}},
		Features: []schema.Manifest{feat},
		Symbols:  []schema.GraphSymbol{{FQN: "S", Feature: "checkout", EnforcesInvariants: []string{"INV-1", "INV-2"}}},
		Tests: []schema.GraphSymbol{
			{FQN: "T1", IsTest: true, Verifies: []string{"checkout:INV-1"}},
			{FQN: "T2", IsTest: true, Verifies: []string{"checkout:INV-2"}},
		},
	}
}

// belowLevel lists the subjects of BRD_BELOW_LEVEL findings, sorted and
// joined; a finding that is not an error is marked so the test fails.
func belowLevel(v []schema.Violation) string {
	var subjects []string
	for _, f := range v {
		if f.Code != schema.CodeBRDBelowLevel {
			continue
		}
		s := f.Subject
		if f.Severity != schema.SeverityError {
			s += "(" + string(f.Severity) + ")"
		}
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)
	return strings.Join(subjects, " ")
}

// Without require_level nothing gates on the truth level; with it, every
// criterion and scenario below the level is an error naming its subject.
// @verifies validation:INV-8
func TestRequireLevel(t *testing.T) {
	onlyT1 := Options{ResultOf: func(fqn string) (bool, bool) { return true, fqn == "T1" }}
	if got := belowLevel(Validate(levelGraph(), config.Default(), onlyT1)); got != "" {
		t.Errorf("no require_level: BRD_BELOW_LEVEL = %q, want none", got)
	}
	for _, tc := range []struct {
		level string
		opts  Options
		want  string
	}{
		{"verified", onlyT1, "brd.x:SC-3"},
		{"demonstrated", onlyT1, "brd.x:SC-2 brd.x:SC-3"},
		// With no results ingested, nothing is demonstrated.
		{"demonstrated", Options{}, "brd.x:SC-1 brd.x:SC-2 brd.x:SC-3 brd.x:US-1"},
	} {
		cfg := config.Default()
		cfg.Validate.RequireLevel = tc.level
		if got := belowLevel(Validate(levelGraph(), cfg, tc.opts)); got != tc.want {
			t.Errorf("require_level %s: BRD_BELOW_LEVEL = %q, want %q", tc.level, got, tc.want)
		}
	}
	cfg := config.Default()
	cfg.Validate.RequireLevel = "shipped"
	if !codes(Validate(levelGraph(), cfg, Options{}))[schema.CodeValidatePolicy] {
		t.Error("an unknown require_level should raise VALIDATE_POLICY")
	}
}
