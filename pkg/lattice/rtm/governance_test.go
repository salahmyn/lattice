package rtm

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// directWireFixture: one criterion wired directly to an enforcer and a
// verifier, no restatement invariant.
func directWireFixture() schema.KnowledgeGraph {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria = []schema.BRDCriterion{{
		ID:         "SC-9",
		Statement:  "Requests by non-members to any team resource return 403.",
		Tier:       2,
		DirectWire: true,
		EnforcedBy: []string{"src.guards.assertMember"},
		VerifiedBy: []string{"Tests\\NonMember403"},
	}}
	return kg
}

func TestDirectWireCriterionLadder(t *testing.T) {
	m := Build(directWireFixture(), Options{})
	row := findRow(t, m, "SC-9")
	if !row.DirectWire || row.Tier != 2 {
		t.Fatalf("direct_wire=%v tier=%d, want true/2", row.DirectWire, row.Tier)
	}
	if row.Status != StatusVerified {
		t.Fatalf("status=%q, want verified (wired, no results)", row.Status)
	}

	m = Build(directWireFixture(), Options{
		ResultOf: func(fqn string) (bool, bool) { return fqn == "Tests\\NonMember403", fqn == "Tests\\NonMember403" },
	})
	if got := findRow(t, m, "SC-9").Status; got != StatusDemonstrated {
		t.Fatalf("status=%q, want demonstrated with green result", got)
	}
}

func TestLiteProfileCapsAtVerified(t *testing.T) {
	m := Build(directWireFixture(), Options{
		ProfileLite: true,
		ResultOf:    func(string) (bool, bool) { return true, true },
	})
	row := findRow(t, m, "SC-9")
	if row.Status != StatusVerified {
		t.Fatalf("status=%q, want verified — lite ceiling is wired", row.Status)
	}
}

func TestFlagsRideAlongsideGreen(t *testing.T) {
	m := Build(directWireFixture(), Options{
		ResultOf: func(string) (bool, bool) { return true, true },
		FlagsOf: func(unit string) []string {
			if unit == "brd.checkout/SC-9" {
				return []string{"demoted by CR-1"}
			}
			return nil
		},
	})
	row := findRow(t, m, "SC-9")
	if row.Status != StatusDemonstrated {
		t.Fatalf("status=%q — the flag must not replace the computed status", row.Status)
	}
	if !row.Flagged || len(row.Flags) != 1 {
		t.Fatalf("flagged=%v flags=%v, want the open flag reported alongside", row.Flagged, row.Flags)
	}
	if m.Summaries[0].Flagged != 1 {
		t.Fatalf("summary flagged=%d, want 1", m.Summaries[0].Flagged)
	}
}

func findRow(t *testing.T, m Matrix, sc string) Row {
	t.Helper()
	for _, r := range m.Rows {
		if r.CriterionID == sc {
			return r
		}
	}
	t.Fatalf("row %s not found", sc)
	return Row{}
}
