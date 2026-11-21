package validate

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func entryFindings(v []schema.Violation) []schema.Violation {
	var out []schema.Violation
	for _, f := range v {
		if f.Code == schema.CodeBreakNoEntryPoint {
			out = append(out, f)
		}
	}
	return out
}

// BREAK_NO_ENTRYPOINT is info and always on: it comes from the audit's none
// outcome with no configuration, an unknown or proven outcome raises nothing,
// and a run that fails on warnings stays green with it open. An explicit
// severity still wins.
// @verifies audit:INV-11
func TestBreakNoEntryPointIsInfoAndAlwaysOn(t *testing.T) {
	kg, _ := coverageGraph()
	rep := &audit.Report{Rows: []audit.Row{
		{Promise: "checkout:INV-1", Feature: "checkout", Invariant: "INV-1",
			EntryPoint: &audit.EntryProof{Outcome: audit.EntryNone, Enforcers: []string{"checkout.Guard"},
				Killers: []string{"checkout.TestGuard"}, WaysIn: []audit.WayIn{{ID: "checkout.route", Handler: "checkout.Serve"}}}},
		{Promise: "checkout:INV-2", Feature: "checkout", Invariant: "INV-2", EntryPoint: &audit.EntryProof{Outcome: audit.EntryUnknown}},
		{Promise: "checkout:INV-3", Feature: "checkout", Invariant: "INV-3", EntryPoint: &audit.EntryProof{Outcome: audit.EntryProven}},
	}}
	cfg := config.Default()

	got := entryFindings(Validate(kg, cfg, Options{Audit: rep}))
	if len(got) != 1 || got[0].Subject != "checkout:INV-1" || got[0].Severity != schema.SeverityInfo {
		t.Fatalf("findings = %+v, want one info finding for checkout:INV-1", got)
	}
	for _, f := range got {
		if Fails(f, schema.SeverityWarning) {
			t.Errorf("fail_on warning must stay green with %s open", f.Code)
		}
		if !Fails(f, schema.SeverityInfo) {
			t.Errorf("fail_on info does fail on it")
		}
	}
	if got := entryFindings(Validate(kg, cfg, Options{})); len(got) != 0 {
		t.Fatalf("no audit computed must raise nothing: %+v", got)
	}

	cfg.Validate.Severity = map[string]string{schema.CodeBreakNoEntryPoint: "off"}
	if got := entryFindings(Validate(kg, cfg, Options{Audit: rep})); len(got) != 0 {
		t.Fatalf("severity off must drop it: %+v", got)
	}
}
