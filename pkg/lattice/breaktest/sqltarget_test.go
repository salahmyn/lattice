package breaktest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// migration is one file of a test change history.
type migration struct{ name, src string }

// planOf reads the migrations the way the graph does (the PostgreSQL
// adapter, then Resolve), takes the symbol with the given FQN and plans its
// break, as the runner does. The edited files are returned as they read after
// the break, by name.
func planOf(t *testing.T, fqn string, ms ...migration) (breakPlan, Symbol, map[string]string) {
	t.Helper()
	a := postgres.New(nil, nil)
	files := map[string][]byte{}
	var mods []ir.Module
	for _, m := range ms {
		files[m.name] = []byte(m.src)
		mod, err := a.Parse(context.Background(), m.name, []byte(m.src))
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, mod)
	}
	var sym Symbol
	var file string
	found := 0
	for _, m := range a.Resolve(mods) {
		for _, s := range m.Symbols {
			if s.FQN != fqn {
				continue
			}
			found++
			file = s.File
			sym = Symbol{Name: s.Name, Kind: string(s.Kind), Line: s.Line, DeclaredAs: s.DeclaredAs}
			for _, al := range s.Altered {
				sym.Altered = append(sym.Altered, schema.GraphAlteration{File: al.File, Line: al.Line, EndLine: al.EndLine, Sets: al.Sets})
			}
		}
	}
	if found != 1 {
		t.Fatalf("%d symbols %s", found, fqn)
	}
	plan, err := planBreak("postgres", file, files[file], sym, func(f string) ([]byte, error) {
		if b, ok := files[f]; ok {
			return b, nil
		}
		return nil, os.ErrNotExist
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	out := map[string]string{}
	for _, e := range plan.edits {
		out[e.file] = string(e.brk.Apply(e.src))
	}
	return plan, sym, out
}

const (
	initFeeds   = "-- @enforces library:INV-9\ncreate policy \"own rows\" on public.feeds for all using (owner = auth.uid());\n"
	feedsFQN    = "sql.policy.public.feeds.own_rows"
	familyAlter = "alter policy \"own rows\" on public.feeds using (owner = auth.uid() or is_family(owner));\n"
)

// A policy changed by a later ALTER POLICY is broken in the ALTER, in the
// file it is in: that is the statement whose USING the database ends up
// with. Breaking the CREATE would be overwritten by the ALTER on every full
// reset.
// @verifies breaktest:INV-16
func TestPolicyBreakGoesOnTheEffectiveAlter(t *testing.T) {
	plan, _, out := planOf(t, feedsFQN,
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/005_family.sql", "-- family members\n" + familyAlter})
	if len(plan.edits) != 1 || plan.edits[0].file != "db/migrations/005_family.sql" || !plan.edits[0].alter || plan.edits[0].line != 2 {
		t.Fatalf("edits = %+v", plan.edits)
	}
	if want := "using (true OR (owner = auth.uid() or is_family(owner)));"; !strings.Contains(out["db/migrations/005_family.sql"], want) {
		t.Errorf("the ALTER lacks %q:\n%s", want, out["db/migrations/005_family.sql"])
	}
	if _, touched := out["db/migrations/001_init.sql"]; touched {
		t.Error("the CREATE was edited")
	}
	if plan.swapWhy != "" {
		t.Errorf("the last ALTER can be swapped: %q", plan.swapWhy)
	}
}

// The last ALTER that sets USING is the target, not the first.
// @verifies breaktest:INV-16
func TestPolicyBreakGoesOnTheLastAlter(t *testing.T) {
	plan, _, _ := planOf(t, feedsFQN,
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/002.sql", "alter policy \"own rows\" on public.feeds using (false);\n"},
		migration{"db/migrations/003.sql", familyAlter})
	if len(plan.edits) != 1 || plan.edits[0].file != "db/migrations/003.sql" {
		t.Fatalf("edits = %+v", plan.edits)
	}
	if plan.swapWhy != "" {
		t.Errorf("the last ALTER is swappable: %q", plan.swapWhy)
	}
	// Break the earlier ALTER's statement instead (a later one also sets
	// USING): not the target, and a swap there would be undone by the later.
	plan, _, _ = planOf(t, feedsFQN,
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/002.sql", familyAlter},
		migration{"db/migrations/003.sql", "alter policy \"own rows\" on public.feeds to authenticated;\n"})
	if len(plan.edits) != 1 || plan.edits[0].file != "db/migrations/002.sql" || plan.swapWhy != ": altered again later in db/migrations/003.sql:1" {
		t.Errorf("an ALTER followed by another touching statement: %+v %q", plan.edits, plan.swapWhy)
	}
}

// An ALTER that changes only TO leaves the break on the CREATE, and the swap
// of the CREATE would lose that ALTER, so it is not offered.
// @verifies breaktest:INV-16
func TestPolicyAlterToOnlyKeepsBreakOnCreate(t *testing.T) {
	plan, _, out := planOf(t, feedsFQN,
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/002_roles.sql", "alter policy \"own rows\" on public.feeds to authenticated;\n"})
	if len(plan.edits) != 1 || plan.edits[0].file != "db/migrations/001_init.sql" || plan.edits[0].alter {
		t.Fatalf("edits = %+v", plan.edits)
	}
	if !strings.Contains(out["db/migrations/001_init.sql"], "using (true OR (owner = auth.uid()))") {
		t.Errorf("CREATE not broken:\n%s", out["db/migrations/001_init.sql"])
	}
	if plan.swapWhy != ": altered later in db/migrations/002_roles.sql:1" {
		t.Errorf("swapWhy = %q", plan.swapWhy)
	}
	r := &runner{wt: &Worktree{tmp: t.TempDir()}}
	f, why, err := r.swapFiles("postgres", false, plan, Symbol{})
	if err != nil || f.ok() || why != plan.swapWhy {
		t.Errorf("swap = %+v %q %v", f, why, err)
	}
}

// A policy renamed and then altered under its new name is still found: the
// ALTER of the new name is the target, and the CREATE is located by the
// name it gave.
// @verifies breaktest:INV-16
func TestPolicyRenamedThenAltered(t *testing.T) {
	plan, sym, out := planOf(t, "sql.policy.public.feeds.family_rows",
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/002.sql", "alter policy \"own rows\" on public.feeds rename to \"family rows\";\n" +
			"alter policy \"family rows\" on public.feeds using (is_family(owner));\n"})
	if sym.DeclaredAs != "policy own rows on public.feeds" {
		t.Errorf("declared as %q", sym.DeclaredAs)
	}
	if len(plan.edits) != 1 || plan.edits[0].file != "db/migrations/002.sql" || plan.edits[0].line != 2 {
		t.Fatalf("edits = %+v", plan.edits)
	}
	if !strings.Contains(out["db/migrations/002.sql"], "using (true OR (is_family(owner)))") {
		t.Errorf("not broken:\n%s", out["db/migrations/002.sql"])
	}
	// Renamed and not altered otherwise: the CREATE is broken, found by its
	// declared name; the swap is not offered.
	plan, _, out = planOf(t, "sql.policy.public.feeds.family_rows",
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/002.sql", "alter policy \"own rows\" on public.feeds rename to \"family rows\";\n"})
	if plan.edits[0].file != "db/migrations/001_init.sql" || !strings.Contains(out["db/migrations/001_init.sql"], "true OR (") || plan.swapWhy == "" {
		t.Errorf("rename only: %+v", plan)
	}
}

// A policy dropped and created again in a later migration is that migration's
// CREATE: the earlier one is not the symbol and nothing breaks it.
// @verifies breaktest:INV-16
func TestPolicyDroppedAndRecreated(t *testing.T) {
	plan, _, out := planOf(t, feedsFQN,
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/004_redo.sql", "drop policy \"own rows\" on public.feeds;\ncreate policy \"own rows\" on public.feeds using (owner = auth.uid() and active);\n"})
	if len(plan.edits) != 1 || plan.edits[0].file != "db/migrations/004_redo.sql" || plan.edits[0].line != 2 {
		t.Fatalf("edits = %+v", plan.edits)
	}
	if !strings.Contains(out["db/migrations/004_redo.sql"], "using (true OR (owner = auth.uid() and active))") {
		t.Errorf("not broken:\n%s", out["db/migrations/004_redo.sql"])
	}
	if plan.swapWhy != "" {
		t.Errorf("nothing follows the CREATE: %q", plan.swapWhy)
	}
}

// A function defined by two CREATE OR REPLACE is broken in the last one, and
// a trigger dropped and created again in the last CREATE.
// @verifies breaktest:INV-16
func TestFunctionAndTriggerBreakOnLastDefinition(t *testing.T) {
	fn := func(body string) string {
		return "create or replace function public.check_total() returns trigger language plpgsql as $$\nbegin\n" + body + "\n  return new;\nend $$;\n"
	}
	plan, _, out := planOf(t, "sql.public.check_total",
		migration{"db/migrations/001.sql", fn("  if new.total < 0 then raise exception 'neg'; end if;")},
		migration{"db/migrations/009.sql", fn("  if new.total <= 0 then raise exception 'nonpositive'; end if;")})
	if len(plan.edits) != 1 || plan.edits[0].file != "db/migrations/009.sql" {
		t.Fatalf("function edits = %+v", plan.edits)
	}
	if !strings.Contains(out["db/migrations/009.sql"], "begin RETURN COALESCE(NEW, OLD);") || plan.swapWhy != "" {
		t.Errorf("function: %q %q", out["db/migrations/009.sql"], plan.swapWhy)
	}

	// A later ALTER FUNCTION touches it: the break stays on the last CREATE
	// and the swap is not offered.
	plan, _, _ = planOf(t, "sql.public.check_total",
		migration{"db/migrations/001.sql", fn("")},
		migration{"db/migrations/002.sql", "alter function public.check_total() security definer;\n"})
	if plan.swapWhy != ": altered later in db/migrations/002.sql:1" {
		t.Errorf("swapWhy = %q", plan.swapWhy)
	}

	trg := "create trigger orders_total before insert on public.orders for each row execute function public.check_total();\n"
	plan, _, out = planOf(t, "sql.trigger.public.orders.orders_total",
		migration{"db/migrations/001.sql", trg},
		migration{"db/migrations/003.sql", "drop trigger orders_total on public.orders;\n" + strings.Replace(trg, "for each row", "for each row when (new.total > 0)", 1)})
	if plan.edits[0].file != "db/migrations/003.sql" || !strings.Contains(out["db/migrations/003.sql"], "when (false AND (new.total > 0))") || plan.swapWhy != "" {
		t.Errorf("trigger: %+v %q", plan.edits[0].file, out["db/migrations/003.sql"])
	}
}

// USING set by one statement and WITH CHECK by another: both are broken, each
// where it is set, and a swap of two statements is not offered.
// @verifies breaktest:INV-16
func TestPolicyBreakInTwoStatements(t *testing.T) {
	create := "create policy \"own rows\" on public.feeds for all using (owner = auth.uid()) with check (owner = auth.uid());\n"
	plan, _, out := planOf(t, feedsFQN,
		migration{"db/migrations/001.sql", create},
		migration{"db/migrations/002.sql", familyAlter})
	if len(plan.edits) != 2 {
		t.Fatalf("edits = %+v", plan.edits)
	}
	if !strings.Contains(out["db/migrations/002.sql"], "using (true OR (owner = auth.uid() or is_family(owner)))") ||
		!strings.Contains(out["db/migrations/001.sql"], "with check (true OR (owner = auth.uid()))") ||
		strings.Contains(out["db/migrations/001.sql"], "using (true OR") {
		t.Errorf("USING and WITH CHECK are broken where they are set:\n%v", out)
	}
	if !strings.HasPrefix(plan.swapWhy, ": the break edits 2 statements") {
		t.Errorf("swapWhy = %q", plan.swapWhy)
	}
}

// For a break in an ALTER the swap files are that statement, broken and
// unbroken, with no DROP: it sets the expression on a policy that exists.
// @verifies breaktest:INV-15
// @verifies breaktest:INV-16
func TestSwapOfAlterTarget(t *testing.T) {
	plan, _, _ := planOf(t, feedsFQN,
		migration{"db/migrations/001_init.sql", initFeeds},
		migration{"db/migrations/005_family.sql", "-- family\n" + familyAlter + "select 1;\n"})
	r := &runner{wt: &Worktree{tmp: t.TempDir()}}
	f, why, err := r.swapFiles("postgres", false, plan, Symbol{})
	if err != nil || !f.ok() || why != "" {
		t.Fatalf("swap = %+v %q %v", f, why, err)
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got, want := read(f.patched), "alter policy \"own rows\" on public.feeds using (true OR (owner = auth.uid() or is_family(owner)));\n"; got != want {
		t.Errorf("patched = %q", got)
	}
	if got := read(f.original); got != familyAlter {
		t.Errorf("original = %q", got)
	}
	if strings.Contains(read(f.patched)+read(f.original), "DROP") {
		t.Error("an ALTER target needs no DROP")
	}
	if filepath.Dir(f.patched) != r.wt.tmp {
		t.Errorf("swap files belong beside the worktree: %s", f.patched)
	}
}

// Statements of a policy that is not altered are planned exactly as before: one
// edit of its own file, swappable.
// @verifies breaktest:INV-16
func TestPlainSQLPlanUnchanged(t *testing.T) {
	plan, _, out := planOf(t, feedsFQN, migration{"db/migrations/001_init.sql", initFeeds})
	if len(plan.edits) != 1 || plan.edits[0].alter || plan.swapWhy != "" || !strings.Contains(out["db/migrations/001_init.sql"], "using (true OR (") {
		t.Errorf("plan = %+v", plan)
	}
}

// Two edits in one file are written together: USING set by an ALTER and WITH
// CHECK by the CREATE above it, in the same migration.
// @verifies breaktest:INV-16
func TestPolicyBreakInTwoStatementsOfOneFile(t *testing.T) {
	src := "create policy \"own rows\" on public.feeds for all using (owner = auth.uid()) with check (owner = auth.uid());\n" + familyAlter
	plan, _, _ := planOf(t, feedsFQN, migration{"db/migrations/001.sql", src})
	if len(plan.edits) != 2 {
		t.Fatalf("edits = %+v", plan.edits)
	}
	merged := byFile(plan.edits)
	if len(merged) != 1 {
		t.Fatalf("merged = %+v", merged)
	}
	got := string(merged[0].brk.Apply(merged[0].src))
	if !strings.Contains(got, "with check (true OR (owner = auth.uid()))") || !strings.Contains(got, "using (true OR (owner = auth.uid() or is_family(owner)))") ||
		strings.Contains(got, "using (true OR (owner = auth.uid()) with") {
		t.Errorf("both statements are broken in the one file:\n%s", got)
	}
}
