package validate

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func governanceFixture() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		Features: []schema.Manifest{{
			ID: "teams",
			Invariants: []schema.Invariant{
				{ID: "INV-1", Statement: "roles govern everything"},
			},
		}},
		BRDs: []schema.BRD{{
			ID: "brd.teams.core", Status: schema.BRDApproved, Title: "Teams",
			SuccessCriteria: []schema.BRDCriterion{
				{ID: "SC-1", Statement: "only owners delete teams", Tier: 2, MapsToInvariant: "teams:INV-1"},
			},
		}},
	}
}

func has(v []schema.Violation, code string) bool {
	for _, x := range v {
		if x.Code == code {
			return true
		}
	}
	return false
}

func TestMutationRequiredAtTier2(t *testing.T) {
	kg := governanceFixture()
	v := Validate(kg, config.Config{}, Options{})
	if !has(v, schema.CodeMutationRequiredTier) {
		t.Fatal("expected MUTATION_REQUIRED_TIER for tier-2 criterion without mutation evidence")
	}

	// Evidence present → gate satisfied.
	kg.Features[0].MutationScores = map[string]float64{"INV-1": 92}
	v = Validate(kg, config.Config{}, Options{})
	if has(v, schema.CodeMutationRequiredTier) {
		t.Fatal("did not expect MUTATION_REQUIRED_TIER with mutation evidence present")
	}

	// Lite profile defers the gate.
	kg.Features[0].MutationScores = nil
	v = Validate(kg, config.Config{Profile: "lite"}, Options{})
	if has(v, schema.CodeMutationRequiredTier) {
		t.Fatal("lite profile must not fire the tier mutation gate")
	}
}

func TestAuthorNotSeparated(t *testing.T) {
	entries := []ledger.Entry{
		{Unit: "brd.teams.core:SC-1", Actor: "agent:dev-1", Transition: "declared→wired"},
		{Unit: "brd.teams.core:SC-1", Actor: "agent:dev-1", Transition: "wired→demonstrated"},
	}
	v := Validate(governanceFixture(), config.Config{}, Options{LedgerEntries: entries})
	if !has(v, schema.CodeAuthorNotSeparated) {
		t.Fatal("expected AUTHOR_NOT_SEPARATED for single-actor demonstrated unit")
	}

	// A second actor in the history clears it.
	entries = append(entries, ledger.Entry{
		Unit: "brd.teams.core:SC-1", Actor: "agent:reviewer-1", Transition: "wired→demonstrated",
	})
	v = Validate(governanceFixture(), config.Config{}, Options{LedgerEntries: entries})
	if has(v, schema.CodeAuthorNotSeparated) {
		t.Fatal("did not expect AUTHOR_NOT_SEPARATED with an independent actor in the history")
	}
}

func TestOpenFlagsSurfaceInValidate(t *testing.T) {
	v := Validate(governanceFixture(), config.Config{}, Options{
		FlagsOf: func(unit string) []string {
			if unit == "brd.teams.core/SC-1" {
				return []string{"demoted by CR-2"}
			}
			return nil
		},
	})
	if !has(v, schema.CodeCriterionFlagged) {
		t.Fatal("expected CRITERION_FLAGGED for the open flag")
	}
}
