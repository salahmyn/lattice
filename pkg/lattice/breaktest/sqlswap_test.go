package breaktest

// @module-feature breaktest

import (
	"context"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

const swapSrc = `-- two neighbours the swap must leave out
create function other() returns int language sql as $$ select 1 $$;

create function public.plain(n int) returns boolean language plpgsql as $$
begin
  return n > 0;
end $$;

create policy "own rows" on app.orders for select using (owner = current_user);

create or replace trigger orders_total before insert on public.orders
  for each row when (new.total > 0) execute function public.check_total();

create constraint trigger "late check" after insert on orders deferrable
  for each row execute function public.check_total();
`

func swapOf(t *testing.T, name, sub string) (patched, original string, ok bool) {
	t.Helper()
	src := []byte(swapSrc)
	sym := Symbol{Name: name, Line: lineOf(t, swapSrc, sub)}
	brk, err := generateSQL(src, sym)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return swapStatements(src, brk, sym)
}

// Each SQL break gives the broken object's own statement and the unbroken
// one: a function is CREATE OR REPLACE, a policy or trigger is preceded by a
// DROP ... IF EXISTS, and neighbouring statements are left out.
// @verifies breaktest:INV-15
func TestSwapStatements(t *testing.T) {
	for _, c := range []struct {
		name, sub, patched, original string
	}{
		{
			"public.plain", "create function public.plain",
			"create OR REPLACE function public.plain(n int) returns boolean language plpgsql as $$\nbegin RETURN true;\n  return n > 0;\nend $$;\n",
			"create OR REPLACE function public.plain(n int) returns boolean language plpgsql as $$\nbegin\n  return n > 0;\nend $$;\n",
		},
		{
			"policy own rows on app.orders", "create policy",
			"DROP POLICY IF EXISTS \"own rows\" ON app.orders;\ncreate policy \"own rows\" on app.orders for select using (true OR (owner = current_user));\n",
			"DROP POLICY IF EXISTS \"own rows\" ON app.orders;\ncreate policy \"own rows\" on app.orders for select using (owner = current_user);\n",
		},
		{
			"trigger orders_total on public.orders", "create or replace trigger",
			"DROP TRIGGER IF EXISTS orders_total ON public.orders;\ncreate or replace trigger orders_total before insert on public.orders\n  for each row when (false AND (new.total > 0)) execute function public.check_total();\n",
			"DROP TRIGGER IF EXISTS orders_total ON public.orders;\ncreate or replace trigger orders_total before insert on public.orders\n  for each row when (new.total > 0) execute function public.check_total();\n",
		},
		{
			"trigger late check on public.orders", "create constraint trigger",
			"DROP TRIGGER IF EXISTS \"late check\" ON orders;\ncreate constraint trigger \"late check\" after insert on orders deferrable\n  for each row WHEN (false) execute function public.check_total();\n",
			"DROP TRIGGER IF EXISTS \"late check\" ON orders;\ncreate constraint trigger \"late check\" after insert on orders deferrable\n  for each row execute function public.check_total();\n",
		},
	} {
		patched, original, ok := swapOf(t, c.name, c.sub)
		if !ok || patched != c.patched || original != c.original {
			t.Errorf("%s: ok=%v\npatched:  %q\nwant:     %q\noriginal: %q\nwant:     %q", c.name, ok, patched, c.patched, original, c.original)
		}
	}
}

// A statement that cannot be located gives nothing to swap.
// @verifies breaktest:INV-15
func TestSwapStatementsNotIsolated(t *testing.T) {
	src := []byte(swapSrc)
	sym := Symbol{Name: "public.plain", Line: 1}
	if _, _, ok := swapStatements(src, Break{Offset: 10, Text: "x"}, sym); ok {
		t.Error("a symbol that is not at its line was isolated")
	}
	sym = Symbol{Name: "public.plain", Line: lineOf(t, swapSrc, "create function public.plain")}
	if _, _, ok := swapStatements(src, Break{Offset: 0, Text: "x"}, sym); ok {
		t.Error("a break outside the statement was accepted")
	}
}

func swapGroups() (runner, shell group) {
	cmd := []string{"sh", "-c", `psql -f "$1" && pg_prove "$3"; psql -f "$2"`, "sh", "{patched_sql}", "{original_sql}", "{files}"}
	runner = group{name: "pgtap", kind: kindRunner, runner: config.Runner{Name: "pgtap", Command: cmd},
		language: "sql", tests: []results.Selector{sel("t.a", "db/t.sql", "sql")}}
	shell = group{name: "sql", kind: kindShell, language: "sql", tmpl: "psql -f {patched_sql} && pg_prove {files}; psql -f {original_sql}",
		tests: []results.Selector{sel("t.a", "db/t.sql", "sql")}}
	return
}

// The placeholders are filled for a runner and a template, and left as
// written, never filled with an empty path, when there are no files.
// @verifies breaktest:INV-15
func TestRenderSQLPlaceholders(t *testing.T) {
	rn, sh := swapGroups()
	files := sqlFiles{patched: "/tmp/x/patched-1.sql", original: "/tmp/x/original-1.sql"}
	if _, argv := render(rn, "/wt", "", files); argv[4] != files.patched || argv[5] != files.original || argv[6] != "db/t.sql" {
		t.Errorf("runner argv = %q", argv)
	}
	if _, argv := render(sh, "/wt", "", files); argv[2] != "psql -f '/tmp/x/patched-1.sql' && pg_prove 'db/t.sql'; psql -f '/tmp/x/original-1.sql'" {
		t.Errorf("template command = %q", argv[2])
	}
	if _, argv := render(rn, "/wt", "", sqlFiles{}); argv[4] != "{patched_sql}" {
		t.Errorf("without files the placeholder was filled: %q", argv)
	}
	if !rn.usesSQLFiles() || !sh.usesSQLFiles() || (group{kind: kindRunner, runner: config.Runner{Command: []string{"x", "{files}"}}}).usesSQLFiles() {
		t.Error("usesSQLFiles is wrong")
	}
}

// A baseline has no patched object: the original statement stands for both.
// @verifies breaktest:INV-15
func TestSQLFilesAsBaseline(t *testing.T) {
	f := sqlFiles{patched: "/p", original: "/o"}.asBaseline()
	if f.patched != "/o" || f.original != "/o" {
		t.Errorf("baseline files = %+v", f)
	}
}

// A command that names the SQL files is never run without them.
// @verifies breaktest:INV-15
func TestInvokeWithoutSQLFiles(t *testing.T) {
	rn, _ := swapGroups()
	rn.runner.Command = []string{"sh", "-c", "touch " + t.TempDir() + "/ran", "sh", "{patched_sql}"}
	r := &runner{opts: Options{Timeout: 5e9}, wt: &Worktree{root: t.TempDir(), Dir: t.TempDir(), tmp: t.TempDir()}}
	inv := r.invoke(context.Background(), rn, sqlFiles{})
	if inv.res.status != statusError || inv.res.reason != "{patched_sql} not available for this break" {
		t.Errorf("result = %+v", inv.res)
	}
}

// The temporary paths of the SQL files never enter a baseline key, so
// promises that run the same command still share one baseline.
// @verifies breaktest:INV-15
func TestInvocationKeyIgnoresSQLPaths(t *testing.T) {
	rn, sh := swapGroups()
	for _, g := range []group{rn, sh} {
		key := invocationKey(g)
		if key != invocationKey(g) {
			t.Fatal("the key is not stable")
		}
		_, withPaths := render(g, worktreeToken, junitToken, sqlFiles{patched: "/tmp/a/patched-7.sql", original: "/tmp/a/original-7.sql"})
		_, withKey := render(g, worktreeToken, junitToken, keySQL)
		if strings.Join(withPaths, " ") == strings.Join(withKey, " ") {
			t.Errorf("%s: paths and key render alike, so the test proves nothing", g.name)
		}
		if strings.Contains(strings.Join(withKey, " "), "/tmp/") || !strings.Contains(strings.Join(withKey, " "), "{patched_sql}") {
			t.Errorf("%s: the key renders %q", g.name, withKey)
		}
	}
}
