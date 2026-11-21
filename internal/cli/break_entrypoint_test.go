package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A wrapper reports every error a handler throws; the deployed entry point is
// the wrapper around a handler that catches every error itself, so on the live
// path the wrapper never sees one. Its two verifiers call the wrapper directly
// with a handler that throws.
const (
	epReportSrc = `// @module-feature ops.errors
export const reports: string[] = [];

export type Handler = (path: string) => Promise<{ status: number; body: string }>;

/**
 * Wraps a request handler: any error it throws is reported, then answered with a 500.
 * @capability report_errors
 * @enforces INV-1
 */
export function withReport(handler: Handler): Handler {
  return async (path) => {
    try {
      return await handler(path);
    } catch (e) {
      reports.push(path + ': ' + String(e));
      return { status: 500, body: 'error' };
    }
  };
}
`
	epHandlerSrc = `// @module-feature ops.api
export interface Store { readonly load: (id: string) => Promise<string> }

/**
 * The real handler. It catches every error itself and answers a 500.
 * @capability get_item
 */
export function createHandler(store: Store) {
  return async (path: string): Promise<{ status: number; body: string }> => {
    try {
      return { status: 200, body: await store.load(path.slice(1)) };
    } catch {
      return { status: 500, body: 'error' };
    }
  };
}
`
	epIndexSrc = `// @module-feature ops.api
import { createHandler, type Store } from './handler.ts';
import { withReport } from './report.ts';

/** The entry point as deployed: the real handler inside the wrapper. */
export function serve(store: Store) {
  return withReport(createHandler(store));
}
`
	epDirectTest = `import { test } from 'node:test';
import { reports, withReport } from './report.ts';

/** @verifies ops.errors:INV-1 */
test('an error thrown by the handler is reported', async () => {
  reports.length = 0;
  await withReport(() => Promise.reject(new Error('boom')))('/x');
});
/** @verifies ops.errors:INV-1 */
test('a normal answer reports nothing', async () => {
  reports.length = 0;
  await withReport(() => Promise.resolve({ status: 200, body: 'ok' }))('/x');
});
`
	// The demanded verifier: it enters through the deployed entry point.
	epServeTest = `import { test } from 'node:test';
import { reports } from './report.ts';
import { serve } from './index.ts';

/** @verifies ops.errors:INV-1 */
test('a request that fails on the server is reported', async () => {
  reports.length = 0;
  await serve({ load: () => Promise.reject(new Error('db down')) })('/item-1');
});
`
	epEntryPoint = `id: ops.item_route
version: 1
status: production
kind: http_route
purpose: |
  GET /<id>: one item, through the deployed wrapper.
owners:
  business: product
  engineering: backend
trigger:
  method: GET
  path: /<id>
handler:
  symbol: src.index.serve
flow:
  - feature: ops
  - feature: ops.api
  - feature: ops.errors
`
	epInvariant = "Every request that fails on the server is reported to the error tracker, and answered with a 500."
)

func epFeature(id, extra string) string {
	return "id: " + id + "\nversion: 1\nstatus: production\npurpose: |\n  " + id + ".\nowners:\n  business: product\n  engineering: backend\n" + extra
}

// epFixture is a committed TypeScript repo with one promise, ops.errors:INV-1,
// kept by withReport and verified by two tests that call it directly. serveTest
// adds a third that enters through the entry point.
func epFixture(t *testing.T, serveTest bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":          "lattice/.cache/\nlattice/lattice.json\n",
		"lattice/config.yaml": "validate:\n  severity:\n    FEATURE_NO_BRD: off\n",
		"lattice/entry-points/ops/item_route.yaml": epEntryPoint,
		"lattice/features/ops.yaml":                epFeature("ops", ""),
		"lattice/features/ops/api.yaml": epFeature("ops.api",
			"capabilities:\n  - id: get_item\n    summary: Returns one item.\n    rules:\n      - A failing store answers a 500.\n"),
		"lattice/features/ops/errors.yaml": epFeature("ops.errors",
			"capabilities:\n  - id: report_errors\n    summary: Reports every error a request handler throws.\n    rules:\n      - The report names the request path.\n"+
				"invariants:\n  - id: INV-1\n    statement: "+epInvariant+"\n    verifiable_by:\n      - test\n"),
		"src/report.ts":      epReportSrc,
		"src/handler.ts":     epHandlerSrc,
		"src/index.ts":       epIndexSrc,
		"src/report.test.ts": epDirectTest,
	}
	if serveTest {
		files["src/serve.test.ts"] = epServeTest
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fixture")
	return dir
}

// epBreaks writes breaks.json with one killed break of withReport that failed
// the given tests (the titles a runner reports).
func epBreaks(t *testing.T, dir string, failed ...string) {
	t.Helper()
	bf := audit.BreaksFile{Schema: audit.BreaksSchema, Runs: []audit.BreakRun{{
		Promise: "ops.errors:INV-1", Enforcer: "src.report.withReport", Method: "early_return",
		Baseline: "pass", Outcome: "killed", FailedTests: failed,
	}}}
	data, _ := json.Marshal(bf)
	p := filepath.Join(dir, "lattice", ".cache", "results", "breaks.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	epDirect1 = "an error thrown by the handler is reported"
	epDirect2 = "a normal answer reports nothing"
	epServe   = "a request that fails on the server is reported"
)

func epProof(t *testing.T, dir string) *audit.EntryProof {
	t.Helper()
	rep := auditJSON(t, dir)
	row := auditRow(t, rep, "ops.errors:INV-1")
	if row.Signals.BreakTested.Value != "killed" {
		t.Fatalf("break_tested = %+v, want killed", row.Signals.BreakTested)
	}
	return row.EntryPoint
}

// Validate's own report: the BREAK_NO_ENTRYPOINT findings, and whether a
// run that fails on warnings still passes.
func epValidate(t *testing.T, dir string, args ...string) (findings []schema.Violation, ok bool, warnings int) {
	t.Helper()
	out, _ := run(t, dir, newValidateCommand, args...)
	var rep struct {
		Violations []schema.Violation `json:"violations"`
		Warnings   int                `json:"warnings"`
		OK         bool               `json:"ok"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	for _, v := range rep.Violations {
		if v.Code == schema.CodeBreakNoEntryPoint {
			findings = append(findings, v)
		}
	}
	return findings, rep.OK, rep.Warnings
}

// The wrapper's break is killed by two tests that call it directly, and the
// deployed entry point (serve) is not what any of them runs: the audit says
// none, and validate names the way in that reaches the enforcer. The finding is
// info, so a run that fails on warnings stays green.
// @verifies audit:INV-11
func TestBreakNoEntryPointAsCommitted(t *testing.T) {
	dir := epFixture(t, false)
	epBreaks(t, dir, epDirect1, epDirect2)

	p := epProof(t, dir)
	if p == nil || p.Outcome != audit.EntryNone {
		t.Fatalf("entry-point proof = %+v, want none", p)
	}
	if len(p.WaysIn) != 1 || p.WaysIn[0].ID != "ops.item_route" || p.WaysIn[0].Handler != "src.index.serve" {
		t.Errorf("ways in = %+v", p.WaysIn)
	}
	rep := auditJSON(t, dir)
	if rep.Summary.EntryPoint == nil || rep.Summary.EntryPoint.None != 1 {
		t.Errorf("summary = %+v", rep.Summary.EntryPoint)
	}
	// The class and the break signal are not changed by the proof.
	if row := auditRow(t, rep, "ops.errors:INV-1"); row.Signals.BreakTested.Value != "killed" {
		t.Errorf("break_tested = %+v", row.Signals.BreakTested)
	}

	got, ok, warnings := epValidate(t, dir, "--fail-on", "warning")
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want one BREAK_NO_ENTRYPOINT", got)
	}
	f := got[0]
	if f.Severity != schema.SeverityInfo || f.Subject != "ops.errors:INV-1" ||
		!strings.Contains(f.Message, "ops.item_route (src.index.serve)") || !strings.Contains(f.Message, "src.report.withReport") ||
		f.NextAction == nil || !strings.Contains(f.NextAction.Detail, "@verifies ops.errors:INV-1") {
		t.Errorf("finding = %+v", f)
	}
	if warnings != 0 || !ok {
		out, _ := run(t, dir, newValidateCommand, "--fail-on", "warning")
		t.Log(out)
		t.Errorf("fail_on warning: ok=%v warnings=%d; an info finding must not fail it", ok, warnings)
	}
	if text := runAudit(t, dir, false); !strings.Contains(text, "entry-point proof: 0 proven, 1 none, 0 unknown") ||
		!strings.Contains(text, "entry point none") {
		t.Errorf("audit text:\n%s", text)
	}
}

// A third verifier that enters through serve, and is among the tests that
// failed after the break, leaves nothing to report: the static graph says it
// can run the way in, which is unknown, never none.
// @verifies audit:INV-11
func TestBreakNoEntryPointSilentWhenAnEntryPointVerifierKilled(t *testing.T) {
	dir := epFixture(t, true)
	epBreaks(t, dir, epDirect1, epDirect2, epServe)

	p := epProof(t, dir)
	if p == nil || p.Outcome != audit.EntryUnknown || p.Cause != audit.CauseNoDynamic {
		t.Fatalf("entry-point proof = %+v, want unknown (no dynamic evidence)", p)
	}
	if got, _, _ := epValidate(t, dir); len(got) != 0 {
		t.Fatalf("findings = %+v, want none", got)
	}
	if text := runAudit(t, dir, false); !strings.Contains(text, "entry-point proof: 0 proven, 0 none, 1 unknown (1 no dynamic evidence)") {
		t.Errorf("audit text:\n%s", text)
	}
}

// The same verifier exists, but only the two direct tests killed the break:
// the kill did not come from the entry-point verifier.
// @verifies audit:INV-11
func TestBreakNoEntryPointWhenOnlyDirectTestsKilled(t *testing.T) {
	dir := epFixture(t, true)
	epBreaks(t, dir, epDirect1, epDirect2)

	p := epProof(t, dir)
	if p == nil || p.Outcome != audit.EntryNone {
		t.Fatalf("entry-point proof = %+v, want none", p)
	}
	for _, k := range p.Killers {
		if strings.Contains(k, "serve") || strings.Contains(k, "fails-on-the-server") {
			t.Errorf("killers = %v, the entry-point verifier did not kill the break", p.Killers)
		}
	}
	if got, _, _ := epValidate(t, dir); len(got) != 1 {
		t.Fatalf("findings = %+v, want one", got)
	}
}

// Without breaks.json the promise is not break-tested: no proof, no finding,
// and no line in the audit.
// @verifies audit:INV-11
func TestBreakNoEntryPointNeedsBreaks(t *testing.T) {
	dir := epFixture(t, false)
	rep := auditJSON(t, dir)
	row := auditRow(t, rep, "ops.errors:INV-1")
	if row.EntryPoint != nil || rep.Summary.EntryPoint != nil {
		t.Errorf("proof without breaks: row %+v, summary %+v", row.EntryPoint, rep.Summary.EntryPoint)
	}
	if got, _, _ := epValidate(t, dir); len(got) != 0 {
		t.Fatalf("findings = %+v, want none", got)
	}
	if text := runAudit(t, dir, false); strings.Contains(text, "entry-point proof") {
		t.Errorf("audit text mentions the proof:\n%s", text)
	}
}
