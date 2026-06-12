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
