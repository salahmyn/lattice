package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// adapters.yaml is strict: an adapter block outside `adapters:` or an unknown
// adapter name is an error, not a silently unscanned language.
func TestLoadAdaptersStrict(t *testing.T) {
	write := func(body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, AdaptersFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	if _, err := LoadAdapters(write("adapters:\n  python: {enabled: true}\npostgres:\n  enabled: true\n")); err == nil {
		t.Error("a top-level postgres block should be an error")
	}
	if _, err := LoadAdapters(write("adapters:\n  postgress: {enabled: true}\n")); err == nil || !strings.Contains(err.Error(), "unknown adapter") {
		t.Errorf("a misspelt adapter should be an unknown-adapter error, got %v", err)
	}
	cfg, err := LoadAdapters(write("adapters:\n  postgres:\n    enabled: true\n    migrations: [\"db/*.sql\"]\n"))
	if err != nil || !cfg.IsEnabled("postgres") || cfg.Adapters["postgres"].Migrations[0] != "db/*.sql" {
		t.Errorf("valid postgres config: %+v, %v", cfg, err)
	}
}
