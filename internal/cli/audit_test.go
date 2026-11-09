package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
)

func auditJSON(t *testing.T, repo string, args ...string) audit.Report {
	t.Helper()
	out, err := run(t, repo, newAuditCommand, args...)
	if err != nil {
		t.Fatalf("audit: %v\n%s", err, out)
	}
	var rep audit.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("audit output: %v\n%s", err, out)
	}
	return rep
}

func auditRow(t *testing.T, rep audit.Report, promise string) audit.Row {
	t.Helper()
	for _, r := range rep.Rows {
		if r.Promise == promise {
			return r
		}
	}
	t.Fatalf("no row %s", promise)
	return audit.Row{}
}

// On the coverage example, one verifier stubs its guard. With the run's
// per-test coverage ingested, that promise reads fake, never_runs_enforcer;
// the honest ones read exercised. Without per-test coverage nothing reads
// fake: "no tool" stays apart from "fake".
// @verifies audit:INV-1
// @verifies audit:INV-2
func TestAuditCoverageExample(t *testing.T) {
	repo := copyTree(t, "../../examples/coverage-prototype")
	abs := func(p string) string { return filepath.Join(repo, p) }

	// Results only: demonstrated, nothing says whether the verifiers run an enforcer.
	if out, err := run(t, repo, newResultsCommand, "ingest", abs("coverage/junit.xml")); err != nil {
		t.Fatalf("ingest results: %v\n%s", err, out)
	}
	rep := auditJSON(t, repo)
	if rep.Schema != audit.Schema || rep.Summary.Total != 3 || rep.Summary.Fakes != 0 {
		t.Fatalf("results only: %+v", rep.Summary)
	}
	for _, r := range rep.Rows {
		if r.Class != audit.ClassDemonstratedOnly || r.Kind != audit.KindNoTool {
			t.Errorf("%s = %s/%s, want demonstrated_only/no_tool", r.Promise, r.Class, r.Kind)
		}
	}

	if out, err := run(t, repo, newResultsCommand, "ingest", abs("coverage/junit.xml"), abs("coverage/all.lcov"),
		"--per-test", abs("coverage/per-test")); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	rep = auditJSON(t, repo)
	stub := auditRow(t, rep, "wallet:INV-1")
	if stub.Class != audit.ClassFake || stub.Kind != audit.KindNeverRunsEnforcer {
		t.Fatalf("wallet:INV-1 = %s/%s (%s), want fake/never_runs_enforcer", stub.Class, stub.Kind, stub.Reason)
	}
	if len(stub.Evidence.Verifiers) != 1 || stub.Evidence.Verifiers[0].RanEnforcer || stub.Evidence.Verifiers[0].Coverage != "per_test" {
		t.Errorf("evidence = %+v", stub.Evidence.Verifiers)
	}
	for _, p := range []string{"wallet:INV-2", "fees:INV-1"} {
		if r := auditRow(t, rep, p); r.Class != audit.ClassExercised {
			t.Errorf("%s = %s, want exercised", p, r.Class)
		}
	}
	if !rep.Inputs.Coverage.Present || rep.Inputs.Coverage.PerTest != 3 || !rep.Inputs.Results.Present || rep.Inputs.Breaks.Present {
		t.Errorf("inputs = %+v", rep.Inputs)
	}
	if got := auditRow(t, rep, "wallet:INV-1").Signals.Confirmed.Value; got != audit.ConfirmedDraft {
		t.Errorf("confirmed = %s (the example BRD is a draft)", got)
	}
	if rep.Rows[0].Promise != "wallet:INV-1" {
		t.Errorf("the fake sorts first, got %s", rep.Rows[0].Promise)
	}

	// Filters.
	if f := auditJSON(t, repo, "--class", "fake"); len(f.Rows) != 1 || f.Summary.Total != 1 {
		t.Errorf("--class fake = %d rows", len(f.Rows))
	}
	if f := auditJSON(t, repo, "--feature", "fees"); len(f.Rows) != 1 || f.Rows[0].Feature != "fees" {
		t.Errorf("--feature fees = %d rows", len(f.Rows))
	}
	if _, err := run(t, repo, newAuditCommand, "--class", "nonsense"); err == nil {
		t.Error("an unknown --class must fail")
	}
}

// breaks.json outcomes written by `lattice break` map to break_tested and
// passes_when_broken, and a reader's note shows without changing the class.
// @verifies audit:INV-3
// @verifies audit:INV-1
func TestAuditReadsBreaksAndNotes(t *testing.T) {
	repo := copyTree(t, "../../examples/coverage-prototype")
	abs := func(p string) string { return filepath.Join(repo, p) }
	if out, err := run(t, repo, newResultsCommand, "ingest", abs("coverage/junit.xml"), abs("coverage/all.lcov"),
		"--per-test", abs("coverage/per-test")); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	rep := auditJSON(t, repo)
	enf := func(p string) string { return auditRow(t, rep, p).Evidence.Enforcers[0].FQN }

	bf := audit.BreaksFile{Schema: audit.BreaksSchema, Runs: []audit.BreakRun{
		{Promise: "wallet:INV-2", Enforcer: enf("wallet:INV-2"), Method: "early_return", Baseline: "pass", Outcome: "killed"},
		{Promise: "fees:INV-1", Enforcer: enf("fees:INV-1"), Method: "scripted", Baseline: "pass", Outcome: "survived"},
		{Promise: "wallet:INV-1", Enforcer: enf("wallet:INV-1"), Method: "early_return", Baseline: "pass", Outcome: "unknown", Reason: "no runner configured"},
	}}
	data, _ := json.Marshal(bf)
	if err := os.WriteFile(abs("lattice/.cache/results/breaks.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	notes := "- promise: wallet:INV-1\n  reader: second-reader\n  verdict: confirmed_fake\n  note: the stub replaces the guard\n"
	if err := os.WriteFile(abs("lattice/audit-notes.yaml"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}

	rep = auditJSON(t, repo)
	if r := auditRow(t, rep, "wallet:INV-2"); r.Class != audit.ClassBreakTested || r.Signals.BreakTested.Method != "early_return" {
		t.Errorf("wallet:INV-2 = %s %+v", r.Class, r.Signals.BreakTested)
	}
	if r := auditRow(t, rep, "fees:INV-1"); r.Class != audit.ClassFake || r.Kind != audit.KindPassesWhenBroken {
		t.Errorf("fees:INV-1 = %s/%s", r.Class, r.Kind)
	}
	stub := auditRow(t, rep, "wallet:INV-1")
	if stub.Class != audit.ClassFake || stub.Signals.BreakTested.Value != "unknown" || len(stub.Notes) != 1 || stub.Notes[0].Verdict != "confirmed_fake" {
		t.Errorf("wallet:INV-1 = %s %+v notes=%v", stub.Class, stub.Signals.BreakTested, stub.Notes)
	}

	// Text and Markdown.
	for _, c := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"lattice audit", "By class", "never_runs_enforcer", "second reader second-reader"}},
		{[]string{"--markdown"}, []string{"## Lattice audit", "### Fakes", "| `wallet:INV-1` |"}},
	} {
		var out bytes.Buffer
		io := &IO{Repo: repo, Out: &out, Err: &out, Actor: "test"}
		cmd := newAuditCommand(io)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs(c.args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		for _, w := range c.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%v output lacks %q:\n%s", c.args, w, out.String())
			}
		}
	}
}
