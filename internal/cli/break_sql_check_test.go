package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

const constraintFeature = `id: orders
version: 1
status: production
purpose: |
  Rules the database enforces on orders.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: An order total is never negative.
  - id: INV-2
    statement: A paid order has a payment date.
`

const constraintCreate = `-- @module-feature orders

create table public.orders (
  id bigint primary key,
  total numeric not null,
  status text not null,
  paid_at timestamptz,
  -- @enforces orders:INV-1
  constraint total_not_negative check (total >= 0)
);
`

const constraintAlter = `-- @module-feature orders

-- @enforces orders:INV-2
alter table public.orders add constraint paid_has_date check (status <> 'paid' or paid_at is not null);
`

func constraintFixture(t *testing.T, cfg string) string {
	t.Helper()
	for _, tool := range []string{"git", "sh", "grep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":                       "lattice/.cache/\nlattice/lattice.json\n",
		"lattice/config.yaml":              cfg,
		"lattice/adapters.yaml":            "adapters:\n  postgres:\n    enabled: true\n",
		"lattice/features/orders.yaml":     constraintFeature,
		"db/migrations/0001_orders.sql":    constraintCreate,
		"db/migrations/0002_paid_date.sql": constraintAlter,
		"db/tests/total_test.sql":          pgTAPFile("INV-1", "negative totals are rejected"),
		"db/tests/paid_test.sql":           pgTAPFile("INV-2", "a paid order has a date"),
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

// A runner that resets a scratch database from the migrations and runs pgTAP:
// it fails, as pg_prove would, when a migration carries the break.
func constraintResetRunner() string {
	script := `if cat db/migrations/*.sql | grep -qF 'true OR ('; then echo 'not ok 1 - check'; exit 1; fi; echo 'ok 1 - check'`
	return "break:\n  runners:\n    - name: pgtap\n      match: [\"db/tests/*.sql\"]\n" +
		"      command: [sh, -c, " + shellYAML(script) + ", sh, \"{files}\"]\n"
}

// Named CHECK constraints are enforcers: the one in CREATE TABLE and the one
// an ALTER TABLE adds are both broken (CHECK (true OR (expr))), a full reset
// that applies the migrations kills each, the symbols are in the data layer,
// and the working tree is not edited.
// @verifies breaktest:INV-1
// @verifies adapters.postgres:INV-4
func TestBreakCheckConstraints(t *testing.T) {
	dir := constraintFixture(t, constraintResetRunner())
	rep, out, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	runs := byPromise(rep)
	for promise, want := range map[string]struct{ enforcer, file string }{
		"orders:INV-1": {"sql.check.public.orders.total_not_negative", "db/migrations/0001_orders.sql"},
		"orders:INV-2": {"sql.check.public.orders.paid_has_date", "db/migrations/0002_paid_date.sql"},
	} {
		r, ok := runs[promise]
		if !ok {
			t.Errorf("%s: no run in %+v", promise, rep.Runs)
			continue
		}
		if r.Outcome != breaktest.Killed || r.Baseline != "pass" || r.Enforcer != want.enforcer || r.EnforcerFile != want.file || r.Method != breaktest.MethodEarlyReturn {
			t.Errorf("%s: outcome %s baseline %s enforcer %s file %s (%s), want killed on %s in %s", promise, r.Outcome, r.Baseline, r.Enforcer, r.EnforcerFile, r.Reason, want.enforcer, want.file)
		}
	}
	for name, want := range map[string]string{"db/migrations/0001_orders.sql": constraintCreate, "db/migrations/0002_paid_date.sql": constraintAlter} {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != want {
			t.Errorf("%s in the working tree was edited", name)
		}
	}

	// The layer is the adapter's fact: data.
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	kg, err := buildGraph(context.Background(), ws, false)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, s := range kg.Symbols {
		if strings.HasPrefix(s.FQN, "sql.check.") {
			seen++
			if s.Layer != schema.LayerData || s.LayerSource != schema.LayerSourceAdapter {
				t.Errorf("%s: layer %q from %q, want data from the adapter", s.FQN, s.Layer, s.LayerSource)
			}
		}
	}
	if seen != 2 {
		t.Errorf("%d check symbols in the graph, want 2", seen)
	}
}

// With {patched_sql} and {original_sql}, the ALTER TABLE ADD CONSTRAINT is
// swapped as DROP CONSTRAINT IF EXISTS and the ADD, broken and as written; the
// CHECK inside CREATE TABLE has no statement to swap, so its run reads unknown
// with the reason, and the runner does not run for it.
// @verifies breaktest:INV-15
func TestBreakCheckSwap(t *testing.T) {
	out := filepath.Join(t.TempDir(), "applied.sql")
	dir := constraintFixture(t, swapRunnerConfig(out))
	rep, outText, err := runBreak(t, dir)
	if err != nil {
		t.Fatalf("break: %v\n%s", err, outText)
	}
	alter := byPromise(rep)["orders:INV-2"]
	if alter.Outcome != breaktest.Killed || alter.Baseline != "pass" {
		t.Errorf("ALTER: %s, baseline %s (%s), want killed", alter.Outcome, alter.Baseline, alter.Reason)
	}
	create := byPromise(rep)["orders:INV-1"]
	if create.Outcome != breaktest.Unknown || !strings.HasPrefix(create.Reason, "{patched_sql} not available for this break: the CHECK is inside CREATE TABLE") || create.Baseline != "" {
		t.Errorf("CREATE TABLE: outcome %s baseline %q reason %q, want unknown and nothing run", create.Outcome, create.Baseline, create.Reason)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	applied := string(raw)
	drop := "alter table public.orders DROP CONSTRAINT IF EXISTS paid_has_date;\n"
	add := "alter table public.orders add constraint paid_has_date check (status <> 'paid' or paid_at is not null);\n"
	brk := "alter table public.orders add constraint paid_has_date check (true OR (status <> 'paid' or paid_at is not null));\n"
	for _, want := range []string{
		"== patched\n" + drop + add + "== original\n" + drop + add, // the baseline gets the original for both
		"== patched\n" + drop + brk + "== original\n" + drop + add,
	} {
		if !strings.Contains(applied, want) {
			t.Errorf("applied files lack:\n%s\nin:\n%s", want, applied)
		}
	}
	if n := strings.Count(applied, "== patched"); n != 2 {
		t.Errorf("the runner ran %d times, want 2 (the baseline and the break of the ALTER)", n)
	}
}

// A constraint dropped and added again by a later migration is broken where
// the last statement adds it, and a constraint dropped for good has no
// enforcer to break.
// @verifies breaktest:INV-17
func TestBreakCheckReadded(t *testing.T) {
	dir := constraintFixture(t, constraintResetRunner())
	addMigration(t, dir, "db/migrations/0003_stricter.sql", "-- @enforces orders:INV-2\n"+
		"alter table public.orders drop constraint paid_has_date, add constraint paid_has_date check (status <> 'paid' or paid_at is not null) not valid;\n")
	rep, out, err := runBreak(t, dir, "--promise", "orders:INV-2")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	r := byPromise(rep)["orders:INV-2"]
	if r.Outcome != breaktest.Killed || r.EnforcerFile != "db/migrations/0003_stricter.sql" {
		t.Errorf("outcome %s in %s (%s), want killed in the migration that adds it last", r.Outcome, r.EnforcerFile, r.Reason)
	}

	addMigration(t, dir, "db/migrations/0004_relax.sql", "alter table public.orders drop constraint paid_has_date;\n")
	rep, out, err = runBreak(t, dir, "--promise", "orders:INV-2")
	if err != nil {
		t.Fatalf("break: %v\n%s", err, out)
	}
	if got := byPromise(rep); len(got) != 0 {
		t.Errorf("a dropped constraint was broken: %+v", got)
	}
}
