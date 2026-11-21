package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

const (
	alterMigration = "db/migrations/0005_family.sql"
	familyPolicy   = "alter policy own_orders on public.orders using (owner = current_user or is_family(owner));\n"
)

// addMigration commits one more migration to the SQL fixture.
func addMigration(t *testing.T, dir, name, src string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "migration")
}

// fullResetRunner stands in for a runner that resets a scratch database by
// applying db/migrations in file order and runs pgTAP. What the database ends
// up with for the policy is its last CREATE or ALTER POLICY statement, so the
// runner fails, as pg_prove would, when that statement carries the break.
func fullResetRunner() string {
	script := `last=$(cat db/migrations/*.sql | tr '\n' ' ' | grep -oE '(create|alter) policy own_orders[^;]*;' | tail -1); ` +
		`if printf '%s' "$last" | grep -qF 'true OR ('; then echo 'not ok 1 - own orders'; exit 1; fi; echo 'ok 1 - own orders'`
	return "break:\n  runners:\n    - name: pgtap\n      match: [\"db/tests/*.sql\"]\n" +
		"      command: [sh, -c, " + shellYAML(script) + ", sh, \"{files}\"]\n"
}

// A policy created in one migration and changed by ALTER POLICY in a later
// one is broken in the ALTER, so a full reset, which applies the ALTER after
// the CREATE, runs the broken policy: the break is killed, not a false
// survived, and the run reports the file that was edited.
// @verifies breaktest:INV-16
func TestBreakSQLPolicyAlteredLater(t *testing.T) {
	dir := sqlFixtureWithConfig(t, fullResetRunner())
	addMigration(t, dir, alterMigration, familyPolicy)
	rep, out, err := runBreak(t, dir, "--promise", "orders:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	r := byPromise(rep)["orders:INV-1"]
	if r.Outcome != breaktest.Killed || r.Baseline != "pass" {
		t.Errorf("outcome %s, baseline %s (%s), want killed", r.Outcome, r.Baseline, r.Reason)
	}
	if r.EnforcerFile != alterMigration {
		t.Errorf("enforcer_file = %q, want the file with the ALTER", r.EnforcerFile)
	}
	for name, want := range map[string]string{"db/migrations/0001_orders.sql": sqlMigration, alterMigration: familyPolicy} {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != want {
			t.Errorf("%s in the working tree was edited", name)
		}
	}
}

// The object swap of a break in an ALTER is that statement, broken and
// unbroken, with no DROP; for a CREATE that a later ALTER touches (here one
// that changes only the roles) it is not offered, and the run reads unknown
// without running the command.
// @verifies breaktest:INV-15
// @verifies breaktest:INV-16
func TestBreakSQLObjectSwapAfterAlter(t *testing.T) {
	out := filepath.Join(t.TempDir(), "applied.sql")
	dir := sqlFixtureWithConfig(t, swapRunnerConfig(out))
	addMigration(t, dir, alterMigration, familyPolicy)
	rep, outText, err := runBreak(t, dir, "--promise", "orders:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, outText)
	}
	if r := byPromise(rep)["orders:INV-1"]; r.Outcome != breaktest.Killed || r.EnforcerFile != alterMigration {
		t.Errorf("outcome %s in %s (%s)", r.Outcome, r.EnforcerFile, r.Reason)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	applied := string(raw)
	for _, want := range []string{
		"== patched\nalter policy own_orders on public.orders using (owner = current_user or is_family(owner));\n== original\nalter policy own_orders on public.orders using (owner = current_user or is_family(owner));\n", // baseline
		"alter policy own_orders on public.orders using (true OR (owner = current_user or is_family(owner)));\n== original\nalter policy own_orders on public.orders using (owner = current_user or is_family(owner));\n",
	} {
		if !strings.Contains(applied, want) {
			t.Errorf("applied files lack:\n%s\nin:\n%s", want, applied)
		}
	}
	if strings.Contains(applied, "DROP POLICY") {
		t.Errorf("an ALTER swap needs no DROP:\n%s", applied)
	}

	// An ALTER that changes only TO: the break stays on the CREATE.
	out2 := filepath.Join(t.TempDir(), "applied.sql")
	dir2 := sqlFixtureWithConfig(t, swapRunnerConfig(out2))
	addMigration(t, dir2, alterMigration, "alter policy own_orders on public.orders to authenticated;\n")
	rep, outText, err = runBreak(t, dir2, "--promise", "orders:INV-1")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, outText)
	}
	r := byPromise(rep)["orders:INV-1"]
	if want := "{patched_sql} not available for this break: altered later in " + alterMigration + ":1"; r.Outcome != breaktest.Unknown || r.Reason != want {
		t.Errorf("outcome %s, reason %q, want unknown, %q", r.Outcome, r.Reason, want)
	}
	if _, err := os.Stat(out2); err == nil {
		t.Error("the runner ran with {patched_sql} unfilled")
	}
}
