package config

import "testing"

func TestMandateCovers(t *testing.T) {
	m := Mandate{ID: "M-1", Grants: "cr_decide", Expires: "2026-12-31"}

	if !m.Covers("cr_decide", "wording", 1, "2026-06-12") {
		t.Fatal("default mandate must cover tier-1 wording")
	}
	if m.Covers("cr_decide", "narrowing", 1, "2026-06-12") {
		t.Fatal("narrowings are never delegable")
	}
	if m.Covers("cr_decide", "wording", 2, "2026-06-12") {
		t.Fatal("default tier ceiling is 1")
	}
	if m.Covers("cr_decide", "widening", 1, "2026-06-12") {
		t.Fatal("default classes are wording-only")
	}
	if m.Covers("cr_decide", "wording", 1, "2027-01-01") {
		t.Fatal("expired mandate must not cover anything")
	}

	wide := Mandate{ID: "M-2", Grants: "cr_decide", Classes: []string{"wording", "widening"}, TierMax: 2}
	if !wide.Covers("cr_decide", "widening", 2, "2026-06-12") {
		t.Fatal("explicit classes + tier ceiling must be honored")
	}
	if wide.Covers("cr_decide", "narrowing", 1, "2026-06-12") {
		t.Fatal("narrowing rejected even if listed in classes")
	}
}

// A brd_approve mandate covers tiers up to tier_max (default 1), never tier
// 3, and nothing after it expires.
// @verifies brds.council:INV-4
func TestMandateCoversBRD(t *testing.T) {
	m := Mandate{ID: "m", Grants: GrantBRDApprove, TierMax: 2, Expires: "2027-01-01"}
	for _, tc := range []struct {
		tier  int
		today string
		want  bool
	}{
		{1, "2026-10-24", true},
		{2, "2026-10-24", true},
		{3, "2026-10-24", false}, // money and regulatory are never delegable
		{1, "2027-01-02", false}, // expired
	} {
		if got := m.CoversBRD(tc.tier, tc.today); got != tc.want {
			t.Errorf("CoversBRD(%d, %s) = %v, want %v", tc.tier, tc.today, got, tc.want)
		}
	}
	if (Mandate{Grants: GrantBRDApprove}).CoversBRD(2, "2026-10-24") {
		t.Error("tier_max defaults to 1")
	}
	if (Mandate{Grants: "cr_decide", TierMax: 2}).CoversBRD(1, "2026-10-24") {
		t.Error("a cr_decide mandate does not cover BRD approval")
	}
}

// Only an active mode without a brd_approval human gate lets a council approve.
func TestCouncilMayApprove(t *testing.T) {
	for mode, want := range map[string]bool{"": false, "gated": false, "council": true, "autonomous": true} {
		if got := (Autonomy{DefaultMode: mode}).CouncilMayApprove(); got != want {
			t.Errorf("mode %q: CouncilMayApprove = %v, want %v", mode, got, want)
		}
	}
}
