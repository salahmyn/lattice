package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

const sqlMigration = `-- @module-feature orders

-- @enforces orders:INV-1
create policy own_orders on public.orders using (owner = current_user);

-- @enforces orders:INV-2
create trigger orders_total before insert on public.orders
  for each row execute function public.check_total();

-- @enforces orders:INV-3
create or replace function public.check_total() returns trigger language plpgsql as $$
begin
  if new.total < 0 then
    raise exception 'negative total';
  end if;
  return new;
end $$;
`

const sqlFeature = `id: orders
version: 1
status: production
purpose: |
  Rules the database enforces on orders.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: A user sees only their own orders.
  - id: INV-2
    statement: Every inserted order is checked.
  - id: INV-3
    statement: An order total is never negative.
`

func pgTAPFile(inv, desc string) string {
	return "-- @module-feature orders\n\n-- @verifies orders:" + inv + "\nselect plan(1);\nselect ok(true, '" + desc + "');\n"
}

// fakePgRunner stands in for a runner that resets a scratch database from the
// worktree's migrations and runs pgTAP: it fails, as pg_prove would, when
// the migration carries marker, the text the break inserts.
func fakePgRunner(name, file, marker string) string {
	script := "if grep -qF " + shellQuoteTest(marker) + " db/migrations/0001_orders.sql; then echo 'not ok 1 - " + name +
		" check'; echo '# Looks like you failed 1 test of 1'; exit 1; fi; echo 'ok 1 - " + name + " check'"
	return "    - name: " + name + "\n      match: [\"" + file + "\"]\n      command: [sh, -c, " + shellYAML(script) + ", sh, \"{files}\"]\n"
}

func shellQuoteTest(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// sqlBreakFixture is a committed repo with a policy, a trigger and a PL/pgSQL
// function, each enforcing a promise with a pgTAP verifier.
func sqlBreakFixture(t *testing.T) string {
	t.Helper()
	cfg := "break:\n  runners:\n" +
		fakePgRunner("policy", "db/tests/policy_test.sql", "true OR (") +
		// This verifier cannot tell the trigger never fires.
		fakePgRunner("trigger", "db/tests/trigger_test.sql", "no break inserts this") +
		fakePgRunner("function", "db/tests/function_test.sql", "RETURN COALESCE(NEW, OLD);")
	return sqlFixtureWithConfig(t, cfg)
}

// sqlFixtureWithConfig is sqlBreakFixture with the given lattice/config.yaml.
func sqlFixtureWithConfig(t *testing.T, cfg string) string {
	t.Helper()
	for _, tool := range []string{"git", "sh", "grep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":                    "lattice/.cache/\nlattice/lattice.json\n",
		"lattice/config.yaml":           cfg,
		"lattice/adapters.yaml":         "adapters:\n  postgres:\n    enabled: true\n",
		"lattice/features/orders.yaml":  sqlFeature,
		"db/migrations/0001_orders.sql": sqlMigration,
		"db/tests/policy_test.sql":      pgTAPFile("INV-1", "owner sees only own orders"),
		"db/tests/trigger_test.sql":     pgTAPFile("INV-2", "insert is accepted"),
		"db/tests/function_test.sql":    pgTAPFile("INV-3", "negative total is rejected"),
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

// SQL enforcers get a generated break, so a pgTAP verifier reads killed or
// survived instead of unknown with "no generated break for postgres".
// @verifies breaktest:INV-1
func TestBreakSQLEnforcers(t *testing.T) {
	dir := sqlBreakFixture(t)
	rep, out, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	runs := byPromise(rep)
	for promise, want := range map[string]struct{ outcome, enforcer string }{
		"orders:INV-1": {breaktest.Killed, "sql.policy.public.orders.own_orders"},
		"orders:INV-2": {breaktest.Survived, "sql.trigger.public.orders.orders_total"},
		"orders:INV-3": {breaktest.Killed, "sql.public.check_total"},
	} {
		r, ok := runs[promise]
		if !ok {
			t.Errorf("%s: no run in %+v", promise, rep.Runs)
			continue
		}
		if r.Outcome != want.outcome || r.Baseline != "pass" || r.Enforcer != want.enforcer {
			t.Errorf("%s: outcome %s baseline %s enforcer %s (%s), want %s by %s",
				promise, r.Outcome, r.Baseline, r.Enforcer, r.Reason, want.outcome, want.enforcer)
		}
		if strings.Contains(r.Reason, "no generated break") {
			t.Errorf("%s: %s", promise, r.Reason)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "db", "migrations", "0001_orders.sql")); string(got) != sqlMigration {
		t.Error("the migration in the working tree was edited")
	}
}

// swapRunnerConfig is one runner for every pgTAP file. It applies only the
// broken object: it records {patched_sql} and {original_sql} in out, and fails
// as pg_prove would when the patched statement carries a break. It names no
// {files}, so the three promises render the same command.
func swapRunnerConfig(out string) string {
	script := `{ echo "== patched"; cat "$1"; echo "== original"; cat "$2"; } >> ` + shellQuoteTest(out) + `; ` +
		`if grep -qE 'true OR \(|WHEN \(false\)|RETURN COALESCE\(NEW, OLD\);' "$1"; then echo 'not ok 1 - check'; exit 1; fi; ` +
		`echo 'ok 1 - check'`
	return "break:\n  runners:\n    - name: pgtap\n      match: [\"db/tests/*.sql\"]\n" +
		"      command: [sh, -c, " + shellYAML(script) + ", sh, \"{patched_sql}\", \"{original_sql}\"]\n"
}

// A SQL break fills {patched_sql} and {original_sql} with the broken object's
// own statement, a policy and a trigger preceded by their DROP ... IF EXISTS;
// the baseline gets the original for both; and the temporary paths do not
// make the baselines of the three promises differ.
// @verifies breaktest:INV-15
func TestBreakSQLObjectSwap(t *testing.T) {
	out := filepath.Join(t.TempDir(), "applied.sql")
	dir := sqlFixtureWithConfig(t, swapRunnerConfig(out))
	rep, outText, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, outText)
	}
	for _, p := range []string{"orders:INV-1", "orders:INV-2", "orders:INV-3"} {
		if r := byPromise(rep)[p]; r.Outcome != breaktest.Killed || r.Baseline != "pass" {
			t.Errorf("%s: %s, baseline %s (%s), want killed", p, r.Outcome, r.Baseline, r.Reason)
		}
	}
	if run, reused := baselineCounts(t, outText); run != 1 || reused != 2 {
		t.Errorf("baselines: %d run, %d reused; want 1 and 2: the temp paths must not enter the key", run, reused)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	applied := string(raw)
	for _, want := range []string{
		// policy
		"DROP POLICY IF EXISTS own_orders ON public.orders;\ncreate policy own_orders on public.orders using (true OR (owner = current_user));\n",
		"== original\nDROP POLICY IF EXISTS own_orders ON public.orders;\ncreate policy own_orders on public.orders using (owner = current_user);\n",
		// trigger
		"DROP TRIGGER IF EXISTS orders_total ON public.orders;\ncreate trigger orders_total before insert on public.orders\n  for each row WHEN (false) execute function public.check_total();\n",
		"== original\nDROP TRIGGER IF EXISTS orders_total ON public.orders;\ncreate trigger orders_total before insert on public.orders\n  for each row execute function public.check_total();\n",
		// function: its own statement, nothing of its neighbours
		"create or replace function public.check_total() returns trigger language plpgsql as $$\nbegin RETURN COALESCE(NEW, OLD);\n  if new.total < 0 then",
	} {
		if !strings.Contains(applied, want) {
			t.Errorf("applied files lack:\n%s\nin:\n%s", want, applied)
		}
	}
	// The baseline saw the original statement for both placeholders.
	if !strings.HasPrefix(applied, "== patched\n") || strings.Contains(strings.SplitN(applied, "== patched\n", 3)[1], "true OR (") {
		t.Errorf("the first call, the baseline, was given a broken statement:\n%s", applied)
	}
}

// A command that names {patched_sql} for a break that has no SQL statement to
// give does not run, and the break reads unknown.
// @verifies breaktest:INV-15
func TestBreakSQLObjectSwapUnavailable(t *testing.T) {
	dir := breakFixture(t)
	ran := filepath.Join(t.TempDir(), "ran")
	cfg := "break:\n  runners:\n    - name: gotest\n      match: [\"guard/*_test.go\"]\n" +
		"      command: [sh, -c, " + shellYAML("touch "+shellQuoteTest(ran)) + ", sh, \"{patched_sql}\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "lattice", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "runner")
	rep, out, err := runBreak(t, dir, "--promise", "guard:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	r := byPromise(rep)["guard:INV-1"]
	if r.Outcome != breaktest.Unknown || r.Reason != "{patched_sql} not available for this break" {
		t.Errorf("outcome %s, reason %q", r.Outcome, r.Reason)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the runner ran with {patched_sql} unfilled")
	}
}
