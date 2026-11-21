package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// A failed setup is said once, as the summary line, with the promises it left
// unrun and the failing command, and the JSON summary carries the same fact.
// @verifies breaktest:INV-20
func TestSetupFailedSummary(t *testing.T) {
	runs := []breaktest.Run{
		{Promise: "fees:INV-1", Outcome: breaktest.Unknown, Reason: "setup failed: docker compose up -d: cannot connect to the daemon"},
		{Promise: "fees:INV-2", Outcome: breaktest.Unknown, Reason: "setup failed: docker compose up -d: cannot connect to the daemon"},
		{Promise: "fees:INV-2", Outcome: breaktest.Unknown, Method: "edge", Reason: "setup failed: docker compose up -d: cannot connect to the daemon"},
		{Promise: "wallet:INV-1", Outcome: breaktest.Unknown, Reason: "baseline fails"},
		{Promise: "wallet:INV-2", Outcome: breaktest.Killed},
	}
	sum := summarize(runs)
	if sum.SetupFailed == nil || sum.SetupFailed.Promises != 2 || sum.SetupFailed.Runs != 3 {
		t.Fatalf("setup_failed = %+v, want 2 promises over 3 runs", sum.SetupFailed)
	}
	want := "setup failed: 2 promises not run (docker compose up -d: cannot connect to the daemon)"
	if got := setupFailedLine(sum.SetupFailed); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if s := summarize(runs[3:]); s.SetupFailed != nil || setupFailedLine(s.SetupFailed) != "" {
		t.Errorf("runs with no failed setup read as one: %+v", s.SetupFailed)
	}
}

// The reason a failed setup leaves on each run is the one the audit's reading
// of breaks.json recognises, so the line and BREAK_UNKNOWN_CAUSE agree.
// @verifies breaktest:INV-20
func TestSetupFailureReasonsAgreeWithAudit(t *testing.T) {
	for _, reason := range []string{
		"setup failed: exit 3",
		"setup failed after a failed restore: psql: connection refused",
		"setup failed after a failed baseline: exit 1",
	} {
		a, aok := audit.SetupFailedReason(reason)
		b, bok := breaktest.SetupFailure(reason)
		if !aok || !bok || a != b || a == "" {
			t.Errorf("%q: audit %q %v, breaktest %q %v", reason, a, aok, b, bok)
		}
	}
	if _, ok := breaktest.SetupFailure("baseline fails"); ok {
		t.Error("a baseline failure read as a failed setup")
	}
}

// A real break with a failing break.setup prints the line under the counts
// and puts the fact in the JSON summary.
// @verifies breaktest:INV-20
func TestBreakPrintsSetupFailed(t *testing.T) {
	dir := jsFixture(t, "break:\n  setup:\n    - \"echo daemon is down >&2; exit 3\"\n  commands:\n    typescript: \"true\"\n")
	runGit(t, dir, "commit", "-q", "-a", "-m", "failing setup", "--allow-empty")

	res := runBreakExit(t, dir, false, "--promise", "web:INV-1")
	if res.code != 0 || !strings.Contains(res.out, "setup failed: 1 promise not run (") || !strings.Contains(res.out, "daemon is down") {
		t.Fatalf("text output (exit %d):\n%s\n%s", res.code, res.out, res.err)
	}
	j := runBreakExit(t, dir, true, "--promise", "web:INV-1")
	if f := j.doc.Summary.SetupFailed; f == nil || f.Promises != 1 || !strings.Contains(f.Reason, "daemon is down") {
		t.Errorf("json summary setup_failed = %+v", j.doc.Summary.SetupFailed)
	}
	// And breaks.json holds the reason BREAK_UNKNOWN_CAUSE reads it by.
	bf, ok, err := audit.LoadBreaks(filepath.Join(dir, "lattice"))
	if err != nil || !ok {
		t.Fatalf("breaks.json: %v %v", ok, err)
	}
	if _, isSetup := audit.SetupFailedReason(bf.Runs[0].Reason); !isSetup {
		t.Errorf("run reason %q is not read as a failed setup", bf.Runs[0].Reason)
	}
}
