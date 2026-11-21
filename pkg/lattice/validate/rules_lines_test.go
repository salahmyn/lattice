package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/lines"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func linesFindings(v []schema.Violation) []schema.Violation {
	var out []schema.Violation
	for _, f := range v {
		if f.Code == schema.CodeVerifiedLinesBelowFloor {
			out = append(out, f)
		}
	}
	return out
}

// The verified-lines floor is opt-in: with no coverage.floors.verified_lines
// nothing fires, even given line data; with it, a feature below it is a
// warning and a feature with no line data is info, and an explicit severity
// still wins.
// @verifies audit:INV-10
func TestVerifiedLinesFloorIsOptIn(t *testing.T) {
	kg, _ := coverageGraph()
	pct := 25.0
	rep := &lines.Report{Features: []lines.Feature{
		{Feature: "checkout", Totals: lines.Totals{WithIntent: lines.Levels{Run: 1, NotRun: 3}, Verified: &pct}},
	}}
	noData := &lines.Report{Features: []lines.Feature{
		{Feature: "checkout", Totals: lines.Totals{WithIntent: lines.Levels{Unknown: 9}}},
	}}

	cfg := config.Default()
	if got := linesFindings(Validate(kg, cfg, Options{Lines: rep})); len(got) != 0 {
		t.Fatalf("no floor declared, yet: %+v", got)
	}
	cfg.Coverage = &config.Coverage{}
	if got := linesFindings(Validate(kg, cfg, Options{Lines: rep})); len(got) != 0 {
		t.Fatalf("a coverage: block without the floor must not fire: %+v", got)
	}

	cfg.Coverage = &config.Coverage{Floors: config.CoverageFloors{VerifiedLines: 80}}
	got := linesFindings(Validate(kg, cfg, Options{Lines: rep}))
	if len(got) != 1 || got[0].Severity != schema.SeverityWarning || got[0].Subject != "checkout" ||
		!strings.Contains(got[0].Message, "25.0%") || !strings.Contains(got[0].Message, "floor of 80%") {
		t.Fatalf("below the floor: %+v", got)
	}
	got = linesFindings(Validate(kg, cfg, Options{Lines: noData}))
	if len(got) != 1 || got[0].Severity != schema.SeverityInfo || !strings.Contains(got[0].Message, "no line data") {
		t.Fatalf("no line data: %+v", got)
	}
	if got := linesFindings(Validate(kg, cfg, Options{})); len(got) != 0 {
		t.Fatalf("no line data computed must raise nothing: %+v", got)
	}

	cfg.Validate.Severity = map[string]string{schema.CodeVerifiedLinesBelowFloor: "error"}
	if got := linesFindings(Validate(kg, cfg, Options{Lines: rep})); len(got) != 1 || got[0].Severity != schema.SeverityError {
		t.Fatalf("an explicit severity must win: %+v", got)
	}
}
