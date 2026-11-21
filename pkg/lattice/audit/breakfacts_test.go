package audit

import (
	"reflect"
	"testing"
)

func factsFile(commit string, runs ...BreakRun) *BreaksFile {
	return &BreaksFile{Schema: BreaksSchema, Commit: commit, Runs: runs}
}

// Survived runs and unknown runs are read per feature, by the audit's own
// rule: a run with every verifier run is survived, a partial one is unknown,
// and a killed one is neither.
// @verifies audit:INV-12
func TestReadBreakFacts(t *testing.T) {
	bf := factsFile("abc",
		BreakRun{Promise: "fees:INV-1", Enforcer: "src.fees.charge", Method: "early_return", Outcome: "survived"},
		BreakRun{Promise: "fees:INV-2", Enforcer: "src.fees.refund", Method: "scripted", Outcome: "killed"},
		BreakRun{Promise: "fees:INV-3", Enforcer: "src.fees.cap", Method: "early_return", Outcome: "survived",
			NotRunVerifiers: []string{"t.a"}},
		BreakRun{Promise: "fees:INV-4", Enforcer: "src.fees.fx", Outcome: "unknown", Reason: "setup failed: docker compose up: daemon down"},
		BreakRun{Promise: "fees:INV-5", Enforcer: "src.fees.fx", Outcome: "unknown", Reason: "setup failed: docker compose up: daemon down"},
		BreakRun{Promise: "wallet:INV-1", Enforcer: "src.wallet.top_up", Outcome: "unknown", Reason: "no runner configured for typescript (set break.runners or break.commands in config.yaml)"},
		// an edge run is not an enforcer's break
		BreakRun{Promise: "wallet:INV-2", Enforcer: "src.wallet.edge", Method: MethodEdge, Outcome: "survived", Hop: &BreakHop{From: "a", To: "b", Kind: "http"}},
	)
	f := ReadBreakFacts(bf, "abc")
	if !f.Evidence || f.Why != "" {
		t.Fatalf("evidence = %v %q", f.Evidence, f.Why)
	}
	if want := []SurvivedRun{{Promise: "fees:INV-1", Enforcer: "src.fees.charge", Method: "early_return"}}; !reflect.DeepEqual(f.Survived["fees"], want) {
		t.Errorf("survived = %+v, want %+v", f.Survived["fees"], want)
	}
	if len(f.Survived["wallet"]) != 0 {
		t.Errorf("an edge run read as a survived promise: %+v", f.Survived["wallet"])
	}
	got := f.Unknown["fees"]
	if len(got) != 2 {
		t.Fatalf("fees unknown groups = %+v, want setup_failed and partial", got)
	}
	if got[0].Cause != CauseSetupFailed || got[0].Count != 2 || got[0].Reason != "docker compose up: daemon down" ||
		!reflect.DeepEqual(got[0].Refs, []string{"fees:INV-4", "fees:INV-5"}) {
		t.Errorf("setup group = %+v", got[0])
	}
	if got[1].Cause != CausePartial || got[1].Count != 1 {
		t.Errorf("partial group = %+v", got[1])
	}
	if w := f.Unknown["wallet"]; len(w) != 1 || w[0].Cause != CauseNoRunner {
		t.Errorf("wallet unknown = %+v", w)
	}
}

// A missing file, a schema it does not know and a file of another commit are
// no evidence, with the reason, never an empty set of survivors.
// @verifies audit:INV-12
func TestReadBreakFactsNoEvidence(t *testing.T) {
	run := BreakRun{Promise: "fees:INV-1", Enforcer: "e", Outcome: "survived"}
	for name, c := range map[string]struct {
		bf   *BreaksFile
		want string
	}{
		"missing":      {nil, "no breaks.json"},
		"other commit": {factsFile("old", run), "is for commit old"},
		"other schema": {&BreaksFile{Schema: "lattice.breaks/9", Commit: "abc", Runs: []BreakRun{run}}, "is not lattice.breaks/1"},
	} {
		f := ReadBreakFacts(c.bf, "abc")
		if f.Evidence || len(f.Survived) != 0 || len(f.Unknown) != 0 || !containsStr(f.Why, c.want) {
			t.Errorf("%s: %+v, want no evidence mentioning %q", name, f, c.want)
		}
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
