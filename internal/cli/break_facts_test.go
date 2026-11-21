package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func writeBreaks(t *testing.T, dir, commit string, runs ...audit.BreakRun) {
	t.Helper()
	data, err := json.Marshal(audit.BreaksFile{Schema: audit.BreaksSchema, Commit: commit, Runs: runs})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "lattice", ".cache", "results", "breaks.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// With no breaks.json, or one of another commit, validate says "no break
// evidence" once; with one of this commit it lists the survived and unknown
// runs per feature. All of it is info, so a fail_on: warning gate is green.
// @verifies validation:INV-23
// @verifies validation:INV-24
func TestBreakFactsOnFixture(t *testing.T) {
	dir := driftFixture(t)
	head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	check := func(label string, wantSurvived, wantUnknown int, noEvidence bool) {
		t.Helper()
		by, ok, warnings := driftValidate(t, dir, "--fail-on", "warning")
		if !ok || warnings != 0 {
			t.Errorf("%s: fail_on warning: ok=%v warnings=%d", label, ok, warnings)
		}
		unk := by[schema.CodeBreakUnknownCause]
		if len(by[schema.CodeBreakSurvived]) != wantSurvived || len(unk) != wantUnknown {
			t.Errorf("%s: survived=%d unknown=%d, want %d and %d: %+v", label,
				len(by[schema.CodeBreakSurvived]), len(unk), wantSurvived, wantUnknown, by)
			return
		}
		if noEvidence != (len(unk) == 1 && strings.HasPrefix(unk[0].Message, "no break evidence")) {
			t.Errorf("%s: no-evidence = %v, findings %+v", label, noEvidence, unk)
		}
		for _, v := range append(append([]schema.Violation{}, by[schema.CodeBreakSurvived]...), unk...) {
			if v.Severity != schema.SeverityInfo {
				t.Errorf("%s: %s is %s, want info", label, v.Code, v.Severity)
			}
		}
	}

	check("no file", 0, 1, true)

	survived := audit.BreakRun{Promise: "shop.checkout:INV-1", Enforcer: "src.checkout.place", Method: "early_return", Outcome: "survived"}
	writeBreaks(t, dir, "0000000000000000000000000000000000000000", survived)
	check("another commit", 0, 1, true)

	unknown := audit.BreakRun{Promise: "shop.checkout:INV-2", Enforcer: "src.checkout.place", Outcome: "unknown",
		Reason: "setup failed: docker compose up: cannot connect"}
	writeBreaks(t, dir, head, survived, unknown)
	check("this commit", 1, 1, false)
}
