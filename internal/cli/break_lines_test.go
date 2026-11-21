package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/lines"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The lines fixture: one JavaScript module per promise, a PL/pgSQL function,
// and a fake runner that writes a JUnit report to {junit} and an LCOV report
// to {coverage}, as a real runner would.
const (
	linesFees = `/** @module-feature fees */

/** @enforces fees:INV-1 */
export function charge(amount) {
  const base = round(amount);
  if (amount < 0) {
    return negative(amount);
  }
  return base * 2;
}

function round(n) {
  return Math.round(n);
}

function negative(n) {
  return -n;
}
`
	linesGuard = `/** @module-feature fees */

/** @enforces fees:INV-2 */
export function guard(s) {
  const ok = s !== '';
  return ok;
}
`
	linesCap = `/** @module-feature fees */

/** @enforces fees:INV-4 */
export function cap(n) {
  const limit = 100;
  return Math.min(n, limit);
}
`
	linesUtil = `/** @module-feature fees */

export function logLine(text) {
  const line = '[fees] ' + text;
  return line;
}
`
	linesSQL = `-- @module-feature fees

-- @enforces fees:INV-3
create or replace function public.check_fee() returns trigger language plpgsql as $$
begin
  if new.fee < 0 then
    raise exception 'negative fee';
  end if;
  return new;
end $$;
`
	linesFeature = `id: fees
version: 1
status: production
purpose: |
  Fee rules, checked in several places.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: A fee is charged twice.
  - id: INV-2
    statement: An empty value is rejected.
  - id: INV-3
    statement: A fee is never negative.
  - id: INV-4
    statement: A fee is capped.
`
	// linesRunner stands in for a test runner. Each mode writes what that
	// runner would: the guard run also runs a test that verifies nothing, the
	// cap run fails when its enforcer is broken, and the database run has no
	// line data.
	linesRunner = `mode=$1; junit=$2; cov=$3
case $mode in
fees)
  cat > "$junit" <<'XML'
<testsuites><testsuite name="fees"><testcase classname="test" name="charge doubles a positive amount"/></testsuite></testsuites>
XML
  cat > "$cov" <<'LCOV'
TN:
SF:src/fees.js
DA:5,1
DA:6,1
DA:7,0
DA:9,1
DA:13,1
DA:17,0
end_of_record
LCOV
  ;;
guard)
  cat > "$junit" <<'XML'
<testsuites><testsuite name="guard"><testcase classname="test" name="guard rejects an empty string"/><testcase classname="test" name="logger smoke test"/></testsuite></testsuites>
XML
  cat > "$cov" <<'LCOV'
TN:
SF:src/guard.js
DA:5,1
DA:6,1
end_of_record
TN:
SF:src/util.js
DA:4,1
DA:5,1
end_of_record
LCOV
  ;;
cap)
  if grep -qF 'if (true) { return undefined; }' src/cap.js; then
    cat > "$junit" <<'XML'
<testsuites><testsuite name="cap"><testcase classname="test" name="cap clamps to the limit"><failure message="not clamped"/></testcase></testsuite></testsuites>
XML
    exit 1
  fi
  cat > "$junit" <<'XML'
<testsuites><testsuite name="cap"><testcase classname="test" name="cap clamps to the limit"/></testsuite></testsuites>
XML
  cat > "$cov" <<'LCOV'
TN:
SF:src/cap.js
DA:5,1
DA:6,1
end_of_record
LCOV
  ;;
db)
  if grep -qF 'no break inserts this' db/migrations/0001_fees.sql; then echo 'not ok 1 - fee check'; exit 1; fi
  echo 'ok 1 - fee check'
  ;;
esac
`
)

func linesRunnerEntry(name, glob, mode string, withFiles ...string) string {
	args := `sh, scripts/fake.sh, ` + mode + `, "{junit}", "{coverage}"`
	if mode == "db" {
		args = `sh, scripts/fake.sh, db, "{files}"`
	}
	return "    - name: " + name + "\n      match: [\"" + glob + "\"]\n      command: [" + args + "]\n"
}

func linesTest(file, title, tag, body string) string {
	return "/** @module-feature fees */\nimport test from 'node:test';\nimport { " + body + " } from '../src/" + file + ".js';\n\n/** @verifies " + tag + " */\ntest('" + title + "', () => { " + body + "(1); });\n"
}

// linesFixture is a committed repo with five promises: a survived enforcer
// with a branch its verifier never takes and two helpers, an enforcer whose
// verifier run also ran another test, a killed enforcer, a SQL function and a
// block no promise owns.
func linesFixture(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"git", "sh", "grep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir := t.TempDir()
	cfg := "break:\n  runners:\n" +
		linesRunnerEntry("fees-run", "tests/fees.test.js", "fees") +
		linesRunnerEntry("guard-run", "tests/guard.test.js", "guard") +
		linesRunnerEntry("cap-run", "tests/cap.test.js", "cap") +
		linesRunnerEntry("db-run", "db/tests/*.sql", "db")
	files := map[string]string{
		".gitignore":                  "lattice/.cache/\nlattice/lattice.json\n",
		"scripts/fake.sh":             linesRunner,
		"lattice/config.yaml":         cfg,
		"lattice/adapters.yaml":       "adapters:\n  postgres:\n    enabled: true\n",
		"lattice/features/fees.yaml":  linesFeature,
		"src/fees.js":                 linesFees,
		"src/guard.js":                linesGuard,
		"src/cap.js":                  linesCap,
		"src/util.js":                 linesUtil,
		"db/migrations/0001_fees.sql": linesSQL,
		"db/tests/fee_test.sql":       "-- @module-feature fees\n\n-- @verifies fees:INV-3\nselect plan(1);\nselect ok(true, 'fee check');\n",
		"tests/fees.test.js":          linesTest("fees", "charge doubles a positive amount", "fees:INV-1", "charge"),
		"tests/guard.test.js":         linesTest("guard", "guard rejects an empty string", "fees:INV-2", "guard"),
		"tests/cap.test.js":           linesTest("cap", "cap clamps to the limit", "fees:INV-4", "cap"),
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

// runAudit runs `lattice audit` with the given flags and returns its output.
func runAudit(t *testing.T, dir string, jsonOut bool, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	io := &IO{Repo: dir, JSON: jsonOut, Out: &out, Err: &errOut}
	cmd := newAuditCommand(io)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("audit %v: %v\n%s%s", args, err, out.String(), errOut.String())
	}
	return out.String()
}

// linesOf runs audit --lines --json and returns the lines section by block.
func linesOf(t *testing.T, dir string, args ...string) (lines.Report, map[string]lines.Block) {
	t.Helper()
	var rep audit.Report
	raw := runAudit(t, dir, true, append([]string{"--lines"}, args...)...)
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("audit output: %v\n%s", err, raw)
	}
	if rep.Lines == nil {
		t.Fatalf("no lines section in\n%s", raw)
	}
	by := map[string]lines.Block{}
	for _, b := range rep.Lines.Blocks {
		by[b.FQN] = b
	}
	return *rep.Lines, by
}

// The lines each promise's verifiers ran are recorded by break --lines and
// read by audit --lines, one level per instrumented line: the enforcer's
// branch no verifier takes is not_run and the rest run, a helper only that
// enforcer calls serves its intent, a run that also ran another test is only
// run_shared, a block no promise owns is other, the lines a killed break edited
// are checked, and a SQL function stays unknown, never run.
// @verifies audit:INV-10
func TestAuditLinesLevels(t *testing.T) {
	dir := linesFixture(t)
	rep, out, err := runBreak(t, dir, "--lines")
	if err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	runs := byPromise(rep)
	if r := runs["fees:INV-4"]; r.Outcome != breaktest.Killed || len(r.Lines) != 2 || r.Lines[0] != 4 || r.Lines[1] != 7 {
		t.Fatalf("fees:INV-4 = %s lines %v, want killed [4 7]: %s", r.Outcome, r.Lines, r.Reason)
	}
	if r := runs["fees:INV-1"]; r.Outcome != breaktest.Survived {
		t.Fatalf("fees:INV-1 = %s, want survived: %s", r.Outcome, r.Reason)
	}

	section, by := linesOf(t, dir)
	if section.Schema != lines.Schema {
		t.Errorf("schema = %q", section.Schema)
	}
	type want struct {
		intent, notRun, shared, other, unknown             string
		checked, run, runShared, otherN, notRunN, unknownN int
	}
	for fqn, w := range map[string]want{
		// A branch no verifier takes: not_run for those lines, run for the rest.
		"src.fees.charge": {intent: "enforces", run: 3, notRunN: 1, notRun: "7"},
		// Only the enforcer calls the helpers.
		"src.fees.round":    {intent: "serves", run: 1},
		"src.fees.negative": {intent: "serves", notRunN: 1, notRun: "17"},
		// The verifier's invocation also ran an unrelated test.
		"src.guard.guard": {intent: "enforces", runShared: 2, shared: "5-6"},
		// A killed break edited these lines.
		"src.cap.cap": {intent: "enforces", checked: 2},
		// No promise owns it: only an unrelated test ran it.
		"src.util.logLine": {intent: "none", otherN: 2, other: "4-5"},
	} {
		b, ok := by[fqn]
		if !ok {
			t.Errorf("%s: no block in %v", fqn, keysOfBlocks(by))
			continue
		}
		got := want{intent: b.Intent.Kind, notRun: b.NotRun, shared: b.RunShared, other: b.Other,
			checked: b.Levels.Checked, run: b.Levels.Run, runShared: b.Levels.RunShared,
			otherN: b.Levels.Other, notRunN: b.Levels.NotRun, unknownN: b.Levels.Unknown}
		if got != w {
			t.Errorf("%s = %+v, want %+v", fqn, got, w)
		}
	}
	// The SQL function has no line data: unknown, with the reason, and the
	// lines it would have are not counted as run.
	sql := by["sql.public.check_fee"]
	if sql.Levels.Unknown == 0 || sql.Levels.Run+sql.Levels.Checked+sql.Levels.RunShared+sql.Levels.NotRun != 0 ||
		sql.Unknown != lines.WhyPgTAP {
		t.Errorf("SQL function = %+v unknown %q, want unknown lines and %q", sql.Levels, sql.Unknown, lines.WhyPgTAP)
	}
	if sql.Intent.Kind != "enforces" {
		t.Errorf("SQL function intent = %+v", sql.Intent)
	}

	// 6 of the 10 known lines of blocks with an intent were run by their own
	// verifiers; the 6 lines of the SQL function are unknown, beside it.
	w := section.Workspace
	if w.WithIntent.Known() != 10 || w.WithIntent.Verified() != 6 || w.WithIntent.Unknown != sql.Levels.Unknown ||
		w.Verified == nil || *w.Verified != 60 {
		t.Errorf("workspace = %+v verified %v", w.WithIntent, w.Verified)
	}
	if w.NoIntent.Other != 2 {
		t.Errorf("no-intent blocks = %+v, want 2 other lines", w.NoIntent)
	}
	if len(section.Features) != 1 || section.Features[0].Feature != "fees" {
		t.Errorf("features = %+v", section.Features)
	}
}

func keysOfBlocks(m map[string]lines.Block) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The text output names each block, its intent, its counts per level and the
// ranges to look at, with the unknown count beside the percentage; without
// --lines the audit prints and returns what it always did.
// @verifies audit:INV-10
func TestAuditLinesText(t *testing.T) {
	dir := linesFixture(t)
	if _, out, err := runBreak(t, dir, "--lines"); err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	text := runAudit(t, dir, false, "--lines")
	for _, want := range []string{
		"workspace  verified lines 60.0% (6 of 10 known lines, 6 unknown)",
		"src/fees.js:4  src.fees.charge  enforces fees:INV-1 (unrequired)",
		"checked 0, run 3, run_shared 0, other 0, not_run 1, unknown 0",
		"not_run: 7\n",
		"src/fees.js:12  src.fees.round  serves fees:INV-1 (unrequired)",
		"run_shared: 5–6",
		"other: 4–5",
		"unknown: " + lines.WhyPgTAP,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("audit --lines output lacks %q:\n%s", want, text)
		}
	}

	// A narrowed audit narrows the lines too.
	narrow, _ := linesOf(t, dir, "--promise", "fees:INV-1")
	if len(narrow.Blocks) != 3 || !narrow.Narrowed {
		t.Errorf("--promise fees:INV-1 kept %d blocks, want charge, round and negative: %+v", len(narrow.Blocks), narrow.Blocks)
	}

	// Without --lines nothing of it appears, in text or JSON.
	plain := runAudit(t, dir, false)
	if strings.Contains(plain, "verified lines") {
		t.Errorf("plain audit mentions lines:\n%s", plain)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal([]byte(runAudit(t, dir, true)), &generic); err != nil {
		t.Fatal(err)
	}
	if _, has := generic["lines"]; has {
		t.Errorf("plain audit JSON has a lines section")
	}
}

// With no verifier-lines.json, or one of another tree, every line of a block
// with an intent reads unknown, with the reason: it is never read as run.
// @verifies audit:INV-10
func TestAuditLinesWithoutVerifierData(t *testing.T) {
	dir := linesFixture(t)
	_, by := linesOf(t, dir)
	b := by["src.fees.charge"]
	if b.Levels.Unknown == 0 || b.Levels.Run+b.Levels.Checked+b.Levels.RunShared != 0 || !strings.Contains(b.Unknown, lines.WhyNotRun) {
		t.Errorf("src.fees.charge without line data = %+v (%q), want only unknown lines", b.Levels, b.Unknown)
	}
	section, _ := linesOf(t, dir)
	if section.Workspace.Verified != nil {
		t.Errorf("a percentage over no known line: %v", *section.Workspace.Verified)
	}
	text := runAudit(t, dir, false, "--lines")
	if !strings.Contains(text, "verified lines no line data (") {
		t.Errorf("text should say there is no line data:\n%s", text)
	}

	// Lines recorded for one tree are not read as another's.
	if _, out, err := runBreak(t, dir, "--lines"); err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "util.js"), []byte(linesUtil+"\n// moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "move")
	_, by = linesOf(t, dir)
	if b := by["src.fees.charge"]; b.Levels.Run != 0 || b.Levels.Unknown == 0 || !strings.Contains(b.Unknown, "tree") {
		t.Errorf("lines of another tree were read: %+v (%q)", b.Levels, b.Unknown)
	}
}

// A plain `lattice break` renders and caches exactly as before: it writes no
// line data, and a baseline made with coverage is a different invocation from
// the plain one, so neither is ever taken for the other.
// @verifies breaktest:INV-13
func TestBreakLinesKeepsPlainBaselinesApart(t *testing.T) {
	dir := linesFixture(t)
	linesPath := filepath.Join(dir, "lattice", ".cache", "results", "verifier-lines.json")

	_, out, err := runBreak(t, dir, "--promise", "fees:INV-4")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if run, _ := baselineCounts(t, out); run == 0 {
		t.Fatalf("plain break ran no baseline:\n%s", out)
	}
	if _, err := os.Stat(linesPath); err == nil {
		t.Errorf("a plain break wrote verifier-lines.json")
	}
	plain := baselineKeys(t, dir)

	// The same promise with --lines takes none of the plain baselines.
	_, out, err = runBreak(t, dir, "--lines", "--promise", "fees:INV-4")
	if err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	if run, _ := baselineCounts(t, out); run == 0 {
		t.Errorf("a --lines run took a plain baseline:\n%s", out)
	}
	both := baselineKeys(t, dir)
	if len(both) != len(plain)+1 {
		t.Errorf("baseline keys %d -> %d: a --lines baseline is a new invocation", len(plain), len(both))
	}
	for k := range plain {
		if !both[k] {
			t.Errorf("--lines dropped the plain baseline %s", k)
		}
	}
	if _, err := os.Stat(linesPath); err != nil {
		t.Fatalf("break --lines wrote no verifier-lines.json: %v", err)
	}

	// A later --lines run on the same tree reuses its line data.
	_, out, err = runBreak(t, dir, "--lines", "--promise", "fees:INV-4")
	if err != nil {
		t.Fatalf("break --lines again: %v\n%s", err, out)
	}
	if run, reused := baselineCounts(t, out); run != 0 || reused == 0 {
		t.Errorf("second --lines run baselines = %d run, %d reused, want all reused", run, reused)
	}
	// And a plain run after --lines still reuses its own.
	_, out, err = runBreak(t, dir, "--promise", "fees:INV-4")
	if run, _ := baselineCounts(t, out); err != nil || run != 0 {
		t.Errorf("plain run after --lines ran %d baselines (%v), want its own reused", run, err)
	}
}

func baselineKeys(t *testing.T, dir string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(breaktest.BaselinesPath(filepath.Join(dir, "lattice")))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Baselines map[string]json.RawMessage `json:"baselines"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k := range f.Baselines {
		out[k] = true
	}
	return out
}

// On a real Go module, --lines runs the verifiers' own `go test` with
// -coverprofile and -coverpkg=./...: only the functions each promise's
// verifier calls are run, and the audit reads the rest as not_run, with the
// lines of the killed break checked.
// @verifies audit:INV-10
func TestBreakLinesGoEndToEnd(t *testing.T) {
	dir := breakFixture(t)
	if _, out, err := runBreak(t, dir, "--lines"); err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	_, by := linesOf(t, dir)
	pos := by["guard.Positive"]
	if pos.Intent.Kind != "enforces" || pos.Levels.Checked != 2 || pos.Levels.NotRun != 0 || pos.Levels.Unknown != 0 {
		t.Errorf("guard.Positive = %+v %+v, want its two body lines checked by the killed break", pos.Intent, pos.Levels)
	}
	// TestNonEmptyCalledOnly runs NonEmpty, and the break of it survived.
	if ne := by["guard.NonEmpty"]; ne.Levels.Run != 2 || ne.Levels.NotRun != 0 {
		t.Errorf("guard.NonEmpty = %+v, want two run lines", ne.Levels)
	}
	// Small's verifier fails at baseline, so nothing of it ran in its own
	// run, and no other run reached it.
	if sm := by["guard.Small"]; sm.Levels.Run != 0 || sm.Levels.NotRun != 2 || sm.NotRun != "17-18" {
		t.Errorf("guard.Small = %+v (%q), want two not_run lines", sm.Levels, sm.NotRun)
	}
}

// validateCodeFindings runs `lattice validate --json` and returns the findings of one
// code.
func validateCodeFindings(t *testing.T, dir, code string) []schema.Violation {
	t.Helper()
	var out, errOut bytes.Buffer
	io := &IO{Repo: dir, JSON: true, Out: &out, Err: &errOut}
	cmd := newValidateCommand(io)
	cmd.SetArgs(nil)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	_ = cmd.Execute() // other findings may fail the run; only this code is read
	var rep struct {
		Violations []schema.Violation `json:"violations"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s%s", err, out.String(), errOut.String())
	}
	var got []schema.Violation
	for _, v := range rep.Violations {
		if v.Code == code {
			got = append(got, v)
		}
	}
	return got
}

// The verified-lines floor is opt-in: validate says nothing about lines until
// coverage.floors.verified_lines is declared, then reports the feature below
// it, with the unknown lines beside the percentage, and "no line data" as info
// when break --lines has not run.
// @verifies audit:INV-10
func TestValidateVerifiedLinesFloor(t *testing.T) {
	dir := linesFixture(t)
	code := schema.CodeVerifiedLinesBelowFloor
	if _, out, err := runBreak(t, dir, "--lines"); err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	if got := validateCodeFindings(t, dir, code); len(got) != 0 {
		t.Fatalf("no floor declared, yet: %+v", got)
	}

	cfg := filepath.Join(dir, "lattice", "config.yaml")
	base, _ := os.ReadFile(cfg)
	if err := os.WriteFile(cfg, append(base, []byte("coverage:\n  floors:\n    verified_lines: 80\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "floor")

	// The commit moved HEAD's tree: the recorded lines are of another tree, so
	// the feature has no line data and says so.
	got := validateCodeFindings(t, dir, code)
	if len(got) != 1 || got[0].Severity != schema.SeverityInfo || !strings.Contains(got[0].Message, "no line data") {
		t.Fatalf("stale line data: %+v", got)
	}

	if _, out, err := runBreak(t, dir, "--lines"); err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	got = validateCodeFindings(t, dir, code)
	if len(got) != 1 || got[0].Severity != schema.SeverityWarning || got[0].Subject != "fees" ||
		!strings.Contains(got[0].Message, "60.0%") || !strings.Contains(got[0].Message, "6 unknown") {
		t.Fatalf("below the floor: %+v", got)
	}

	if err := os.WriteFile(cfg, append(base, []byte("coverage:\n  floors:\n    verified_lines: 50\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-a", "-m", "lower floor")
	if _, out, err := runBreak(t, dir, "--lines"); err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	if got := validateCodeFindings(t, dir, code); len(got) != 0 {
		t.Fatalf("at 60%% over a floor of 50: %+v", got)
	}
}

// A Go module below the code root, whose module path does not name its
// directory, still maps its coverage profile back onto the workspace's files:
// -coverpkg=./... resolves from the module's own directory, and the profile's
// import paths are joined through its go.mod.
// @verifies audit:INV-10
func TestBreakLinesGoSubmodule(t *testing.T) {
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":                  "lattice/.cache/\nlattice/lattice.json\n",
		"svc/go.mod":                  "module example.com/other\n\ngo 1.21\n",
		"svc/guard/guard.go":          breakFixtureGuard,
		"svc/guard/guard_test.go":     breakFixtureTests,
		"lattice/config.yaml":         "{}\n",
		"lattice/features/guard.yaml": breakFixtureFeature,
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

	if _, out, err := runBreak(t, dir, "--lines"); err != nil {
		t.Fatalf("break --lines: %v\n%s", err, out)
	}
	_, by := linesOf(t, dir)
	pos, ok := by["svc.guard.Positive"]
	if !ok {
		t.Fatalf("no block svc.guard.Positive in %v", keysOfBlocks(by))
	}
	if pos.File != "svc/guard/guard.go" || pos.Levels.Checked != 2 || pos.Levels.Unknown != 0 {
		t.Errorf("svc.guard.Positive = %s %+v, want two checked lines", pos.File, pos.Levels)
	}
	if ne := by["svc.guard.NonEmpty"]; ne.Levels.Run != 2 {
		t.Errorf("svc.guard.NonEmpty = %+v, want two run lines", ne.Levels)
	}
}
