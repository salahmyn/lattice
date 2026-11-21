package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func breakGraph() schema.KnowledgeGraph {
	kg := driftGraph()
	kg.GeneratedFromCommit = "abc123"
	return kg
}

func breakFile(commit string, runs ...audit.BreakRun) *audit.BreaksFile {
	return &audit.BreaksFile{Schema: audit.BreaksSchema, Commit: commit, Runs: runs}
}

// BREAK_SURVIVED lists, once per feature, each promise whose break survived
// with its enforcer and method; BREAK_UNKNOWN_CAUSE groups the unknown runs
// by cause with a count and the reason text. Both are info.
// @verifies validation:INV-23
func TestBreakFacts(t *testing.T) {
	bf := breakFile("abc123",
		audit.BreakRun{Promise: "fees:INV-1", Enforcer: "src.fees.charge", Method: "early_return", Outcome: "survived"},
		audit.BreakRun{Promise: "fees:INV-2", Enforcer: "src.fees.cap", Method: "scripted", Outcome: "survived"},
		audit.BreakRun{Promise: "fees:INV-3", Enforcer: "src.fees.fx", Outcome: "unknown", Reason: "setup failed: docker compose up: cannot connect"},
		audit.BreakRun{Promise: "fees:INV-4", Enforcer: "src.fees.fx", Outcome: "unknown", Reason: "setup failed: docker compose up: cannot connect"},
		audit.BreakRun{Promise: "fees:INV-5", Enforcer: "src.fees.fx", Outcome: "unknown", Reason: "baseline fails"},
		audit.BreakRun{Promise: "wallet:INV-1", Enforcer: "src.wallet.top_up", Outcome: "killed"},
	)
	got := Validate(breakGraph(), config.Default(), Options{Breaks: bf, BreaksRead: true})

	surv := driftByCode(got, schema.CodeBreakSurvived)
	if len(surv) != 1 || surv[0].FeatureID != "fees" || surv[0].Severity != schema.SeverityInfo {
		t.Fatalf("BREAK_SURVIVED = %+v, want one info finding on fees", surv)
	}
	for _, want := range []string{"fees:INV-1", "src.fees.charge", "early_return", "fees:INV-2", "src.fees.cap", "scripted"} {
		if !strings.Contains(surv[0].Message, want) {
			t.Errorf("BREAK_SURVIVED message lacks %q: %s", want, surv[0].Message)
		}
	}

	unk := driftByCode(got, schema.CodeBreakUnknownCause)
	if len(unk) != 1 || unk[0].FeatureID != "fees" || unk[0].Severity != schema.SeverityInfo {
		t.Fatalf("BREAK_UNKNOWN_CAUSE = %+v, want one info finding on fees", unk)
	}
	for _, want := range []string{"3 unknown break runs", "setup failed x2 (docker compose up: cannot connect)", "other x1 (baseline fails)"} {
		if !strings.Contains(unk[0].Message, want) {
			t.Errorf("BREAK_UNKNOWN_CAUSE message lacks %q: %s", want, unk[0].Message)
		}
	}
}

// A missing file, or one of another commit, reads "no break evidence" once,
// as info, and raises no survived or per-feature finding from it.
// @verifies validation:INV-24
func TestNoBreakEvidence(t *testing.T) {
	stale := breakFile("old999", audit.BreakRun{Promise: "fees:INV-1", Enforcer: "e", Outcome: "survived"})
	for name, bf := range map[string]*audit.BreaksFile{"missing": nil, "other commit": stale} {
		got := Validate(breakGraph(), config.Default(), Options{Breaks: bf, BreaksRead: true})
		unk := driftByCode(got, schema.CodeBreakUnknownCause)
		if len(unk) != 1 || unk[0].Severity != schema.SeverityInfo || !strings.HasPrefix(unk[0].Message, "no break evidence") {
			t.Errorf("%s: BREAK_UNKNOWN_CAUSE = %+v, want one info finding reading no break evidence", name, unk)
		}
		if s := driftByCode(got, schema.CodeBreakSurvived); len(s) != 0 {
			t.Errorf("%s: a file that is not evidence raised BREAK_SURVIVED: %+v", name, s)
		}
	}
	// A caller that did not read the cache says nothing.
	got := Validate(breakGraph(), config.Default(), Options{})
	if len(driftByCode(got, schema.CodeBreakUnknownCause))+len(driftByCode(got, schema.CodeBreakSurvived)) != 0 {
		t.Errorf("break findings without BreaksRead: %+v", got)
	}
}

// Neither finding is above info, so a gate that fails on warnings stays
// green with them present.
// @verifies validation:INV-23
func TestBreakFactsStayInfo(t *testing.T) {
	bf := breakFile("abc123",
		audit.BreakRun{Promise: "fees:INV-1", Enforcer: "e", Outcome: "survived"},
		audit.BreakRun{Promise: "fees:INV-2", Enforcer: "e", Outcome: "unknown", Reason: "setup failed: x"})
	threshold, err := FailOn("warning")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []Options{{Breaks: bf, BreaksRead: true}, {BreaksRead: true}} {
		for _, v := range Validate(breakGraph(), config.Default(), o) {
			if (v.Code == schema.CodeBreakSurvived || v.Code == schema.CodeBreakUnknownCause) && Fails(v, threshold) {
				t.Errorf("%s fails a fail_on: warning gate: %+v", v.Code, v)
			}
		}
	}
}
