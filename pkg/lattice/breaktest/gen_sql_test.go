package breaktest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sqlCase is one enforcer statement, the symbol name the PostgreSQL adapter
// gives it, and either the exact source after the break or the NoBreak
// reason. run is SQL that exercises the broken statement on a real server.
type sqlCase struct {
	label, name, src, want, reason, run string
}

var sqlPolicyCases = []sqlCase{
	{label: "using only", name: "policy owner reads on app.orders",
		src:  `create policy "owner reads" on app.orders for select using (owner = auth.uid());`,
		want: `create policy "owner reads" on app.orders for select using (true OR (owner = auth.uid()));`},
	{label: "with check only", name: "policy insert_own on public.orders",
		src:  `CREATE POLICY insert_own ON orders FOR INSERT TO authenticated WITH CHECK (owner = current_user);`,
		want: `CREATE POLICY insert_own ON orders FOR INSERT TO authenticated WITH CHECK (true OR (owner = current_user));`},
	{label: "both clauses", name: "policy update_own on public.orders",
		src: `create policy update_own on public.orders
  as restrictive
  for update
  using (owner = current_user)
  with check (owner = current_user and total >= 0);`,
		want: `create policy update_own on public.orders
  as restrictive
  for update
  using (true OR (owner = current_user))
  with check (true OR (owner = current_user and total >= 0));`},
	{label: "nested parens and a ) in strings, identifiers and comments", name: "policy p on public.orders",
		src: `create policy p on orders using ((status = ')') or (note like E'%\')%' and exists (select 1 from t where t.x = "a)b")) /* ) */ -- )
);`,
		want: `create policy p on orders using (true OR ((status = ')') or (note like E'%\')%' and exists (select 1 from t where t.x = "a)b")) /* ) */ -- )
));`},
	{label: "no expression", name: "policy bare on public.orders",
		src: `create policy bare on orders;`, reason: "policy has no USING or WITH CHECK expression"},
}

var sqlTriggerCases = []sqlCase{
	{label: "no WHEN", name: "trigger orders_check on public.orders",
		src: `create trigger orders_check
  before insert or update on public.orders
  for each row
  execute function public.check_order();`,
		want: `create trigger orders_check
  before insert or update on public.orders
  for each row WHEN (false)
  execute function public.check_order();`},
	{label: "with WHEN", name: "trigger total_changed on public.orders",
		src:  `create trigger total_changed before update on orders for each row when (old.total is distinct from new.total) execute function check_order();`,
		want: `create trigger total_changed before update on orders for each row when (false AND (old.total is distinct from new.total)) execute function check_order();`},
	{label: "constraint trigger with EXECUTE PROCEDURE", name: "trigger orders_balance on public.orders",
		src:  `create constraint trigger orders_balance after insert on orders deferrable initially deferred for each row execute procedure check_order();`,
		want: `create constraint trigger orders_balance after insert on orders deferrable initially deferred for each row WHEN (false) execute procedure check_order();`},
	{label: "mixed case, statement level", name: "trigger audit_all on app.orders",
		src:  `Create Or Replace Trigger Audit_All AFTER TRUNCATE ON App.Orders FOR EACH STATEMENT Execute Function app.audit();`,
		want: `Create Or Replace Trigger Audit_All AFTER TRUNCATE ON App.Orders FOR EACH STATEMENT WHEN (false) Execute Function app.audit();`},
	{label: "instead of", name: "trigger orders_view_ins on public.orders_view",
		src:    `create trigger orders_view_ins instead of insert on orders_view for each row execute function check_order();`,
		reason: "INSTEAD OF trigger: WHEN is not allowed"},
}

var sqlFunctionCases = []sqlCase{
	{label: "returns trigger", name: "public.check_order",
		src: `create or replace function public.check_order() returns trigger
language plpgsql as $$
begin
  if new.total < 0 then
    raise exception 'negative total';
  end if;
  return new;
end;
$$;`,
		want: `create or replace function public.check_order() returns trigger
language plpgsql as $$
begin RETURN COALESCE(NEW, OLD);
  if new.total < 0 then
    raise exception 'negative total';
  end if;
  return new;
end;
$$;`,
		// Without the break the insert raises; with it both rows go through.
		run: `create trigger orders_check before insert or delete on orders for each row execute function public.check_order();
insert into orders (id, total) values (1, -5);
delete from orders;`},
	{label: "void, DECLARE, nested block, $body$, quoted schema-qualified name", name: "app.Refund",
		src: `CREATE OR REPLACE FUNCTION app."Refund"(p_order int) RETURNS void AS $body$
DECLARE
  v_total int := 0; -- begin is not here
BEGIN
  BEGIN
    select total into v_total from orders where id = p_order;
  EXCEPTION WHEN others THEN
    v_total := 0;
  END;
  IF v_total < 0 THEN RAISE EXCEPTION 'begin'; END IF;
END
$body$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public;`,
		want: `CREATE OR REPLACE FUNCTION app."Refund"(p_order int) RETURNS void AS $body$
DECLARE
  v_total int := 0; -- begin is not here
BEGIN RETURN;
  BEGIN
    select total into v_total from orders where id = p_order;
  EXCEPTION WHEN others THEN
    v_total := 0;
  END;
  IF v_total < 0 THEN RAISE EXCEPTION 'begin'; END IF;
END
$body$ LANGUAGE plpgsql SECURITY DEFINER SET search_path = public;`,
		run: `select app."Refund"(1);`},
	{label: "boolean, returns null on null input", name: "public.can_refund",
		src:  `create function can_refund(p_owner text, p_order int) returns boolean returns null on null input language 'plpgsql' stable as $$ begin return exists (select 1 from orders where id = p_order and owner = p_owner); end $$;`,
		want: `create function can_refund(p_owner text, p_order int) returns boolean returns null on null input language 'plpgsql' stable as $$ begin RETURN true; return exists (select 1 from orders where id = p_order and owner = p_owner); end $$;`,
		run:  `do $$ begin assert can_refund('x', 1); end $$;`},
	{label: "setof", name: "public.open_orders",
		src:  "create function open_orders() returns setof orders language plpgsql as $$ begin return query select * from orders; end $$;",
		want: "create function open_orders() returns setof orders language plpgsql as $$ begin RETURN; return query select * from orders; end $$;",
		run:  "select * from open_orders();"},
	{label: "table", name: "public.totals",
		src:  "create function totals() returns table (id int, total int) language plpgsql as $$ begin return query select o.id, o.total from orders o; end $$;",
		want: "create function totals() returns table (id int, total int) language plpgsql as $$ begin RETURN; return query select o.id, o.total from orders o; end $$;",
		run:  "select * from totals();"},
	{label: "scalar, labelled block", name: "public.order_total",
		src: `create function order_total(p int) returns integer language plpgsql as $f$
<<main>>
declare
  v int;
begin
  select total into strict v from orders where id = p;
  return v;
end main
$f$;`,
		want: `create function order_total(p int) returns integer language plpgsql as $f$
<<main>>
declare
  v int;
begin RETURN NULL;
  select total into strict v from orders where id = p;
  return v;
end main
$f$;`,
		run: "select order_total(1);"},
	{label: "boolean array is a scalar", name: "public.flags",
		src:  "create function flags() returns boolean[] language plpgsql as $$ begin return array[false]; end $$;",
		want: "create function flags() returns boolean[] language plpgsql as $$ begin RETURN NULL; return array[false]; end $$;",
		run:  "select flags();"},
	{label: "out parameters, no returns", name: "public.split_total",
		src:  "create function split_total(p int, out n int, out s int) language plpgsql as $$ begin n := 1; s := p; end $$;",
		want: "create function split_total(p int, out n int, out s int) language plpgsql as $$ begin RETURN; n := 1; s := p; end $$;",
		run:  "select * from split_total(1);"},
	{label: "returns record with inout", name: "public.bump",
		src:  "create function bump(in p int, inout n int, out s int) returns record language plpgsql as $$ begin n := n + p; s := 0; end $$;",
		want: "create function bump(in p int, inout n int, out s int) returns record language plpgsql as $$ begin RETURN; n := n + p; s := 0; end $$;",
		run:  "select * from bump(1, 2);"},
	{label: "procedure", name: "app.settle",
		src:  "create procedure app.settle(p int) language plpgsql as $$ begin update orders set total = 0 where id = p; end $$;",
		want: "create procedure app.settle(p int) language plpgsql as $$ begin RETURN; update orders set total = 0 where id = p; end $$;",
		run:  "call app.settle(1);"},
	{label: "sql language", name: "public.is_owner",
		src:    "create function is_owner(p int) returns boolean language sql as $$ select exists (select 1 from orders where id = p) $$;",
		reason: "sql-language function: no generated break; add a script"},
	{label: "sql-standard body", name: "public.one",
		src:    "create function one() returns int begin atomic select 1; end;",
		reason: "sql-language function: no generated break"},
	{label: "c language", name: "public.native",
		src:    "create function native(int) returns int as 'lib', 'native' language c strict;",
		reason: "c-language function: no generated break"},
	{label: "string body", name: "public.quoted",
		src:    "create function quoted() returns int language plpgsql as 'begin return 1; end';",
		reason: "plpgsql body is not dollar-quoted"},
	{label: "event trigger", name: "public.on_ddl",
		src:    "create function on_ddl() returns event_trigger language plpgsql as $$ begin raise notice 'ddl'; end $$;",
		reason: "event trigger function: no generated break"},
	{label: "no top-level BEGIN", name: "public.odd",
		src:    "create function odd() returns int language plpgsql as $$ return 1; $$;",
		reason: "does not start with DECLARE or BEGIN"},
}

// genSQL breaks the symbol c.name at line 1 of c.src.
func genSQL(c sqlCase) (string, error) {
	b, err := Generate("postgres", "db/migrations/0001.sql", []byte(c.src), Symbol{Name: c.name, Kind: "function", Line: 1})
	if err != nil {
		return "", err
	}
	return string(b.Apply([]byte(c.src))), nil
}

// sqlBalanced checks a broken file structurally: it scans (every string,
// identifier, comment and dollar quote is closed), each statement's
// parentheses balance, and so do those of each dollar-quoted body.
func sqlBalanced(t *testing.T, label, src string) {
	t.Helper()
	b := []byte(src)
	toks, err := sqlLex(b, 0, len(b), 1)
	if err != nil {
		t.Errorf("%s: broken SQL does not scan: %v", label, err)
		return
	}
	check := func(toks []sqlTok) {
		depth := 0
		for _, tk := range toks {
			switch {
			case tk.kind == sqlPunct && b[tk.start] == '(':
				depth++
			case tk.kind == sqlPunct && b[tk.start] == ')':
				depth--
			case tk.kind == sqlPunct && b[tk.start] == ';' && depth != 0:
				t.Errorf("%s: unbalanced parentheses before ; at line %d", label, tk.line)
			}
			if depth < 0 {
				t.Errorf("%s: ) without ( at line %d", label, tk.line)
				return
			}
		}
		if depth != 0 {
			t.Errorf("%s: %d unclosed (", label, depth)
		}
	}
	check(toks)
	for _, tk := range toks {
		if tk.kind != sqlDollar {
			continue
		}
		n := sqlDollarTag(b, tk.start)
		inner, err := sqlLex(b, tk.start+n, tk.end-n, tk.line)
		if err != nil {
			t.Errorf("%s: dollar-quoted body does not scan: %v", label, err)
			continue
		}
		check(inner)
	}
}

func runSQLCases(t *testing.T, cases []sqlCase) {
	t.Helper()
	for _, c := range cases {
		got, err := genSQL(c)
		if c.reason != "" {
			var nb *NoBreak
			if !errors.As(err, &nb) || !strings.Contains(nb.Reason, c.reason) {
				t.Errorf("%s: want NoBreak %q, got %q, %v", c.label, c.reason, got, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.label, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.label, got, c.want)
		}
		sqlBalanced(t, c.label, got)
	}
}

// A policy's USING and WITH CHECK expressions become true OR (expr).
// @verifies breaktest:INV-1
func TestGenerateSQLPolicy(t *testing.T) { runSQLCases(t, sqlPolicyCases) }

// A trigger gets WHEN (false), or its WHEN (cond) becomes false AND (cond).
// @verifies breaktest:INV-1
func TestGenerateSQLTrigger(t *testing.T) { runSQLCases(t, sqlTriggerCases) }

// A PL/pgSQL function returns first thing in its top-level block, with a
// return that fits its declared result; other languages read unknown.
// @verifies breaktest:INV-1
func TestGenerateSQLFunction(t *testing.T) { runSQLCases(t, sqlFunctionCases) }

// The enforcer is the statement that starts on the symbol's line and has
// its name: a redefinition later in the file, or another statement on that
// line, is not broken by mistake. "sql" is accepted as the language too.
// @verifies breaktest:INV-1
func TestGenerateSQLLocatesByLine(t *testing.T) {
	const src = `-- @enforces orders:INV-1
create policy own on orders using (owner = current_user);

create function f() returns boolean language plpgsql as $$ begin return false; end $$; create policy other on orders using (false);

-- redefined: the graph points at the effective definition
create or replace function f() returns boolean language plpgsql as $$
begin
  return false;
end $$;
`
	gen := func(lang, sub, name string) (string, error) {
		b, err := Generate(lang, "m.sql", []byte(src), Symbol{Name: name, Line: lineOf(t, src, sub)})
		if err != nil {
			return "", err
		}
		return string(b.Apply([]byte(src))), nil
	}
	got, err := gen("postgres", "create or replace function f", "public.f")
	if err != nil || !strings.Contains(got, "$$\nbegin RETURN true;\n  return false;") ||
		strings.Contains(got, "$$ begin RETURN") {
		t.Errorf("redefinition: %v\n%s", err, got)
	}
	got, err = gen("sql", "create policy other", "policy other on public.orders")
	if err != nil || !strings.Contains(got, "create policy other on orders using (true OR (false));") ||
		strings.Contains(got, "(true OR (owner") {
		t.Errorf("second statement on a line: %v\n%s", err, got)
	}
	var nb *NoBreak
	if _, err := gen("postgres", "create policy own", "policy gone on public.orders"); !errors.As(err, &nb) ||
		!strings.Contains(nb.Reason, "no SQL function, policy or trigger policy gone on public.orders at line 2") {
		t.Errorf("wrong name: %v", err)
	}
	if _, err := Generate("postgres", "m.sql", []byte("create function g() returns int language plpgsql as $$ begin"),
		Symbol{Name: "public.g", Line: 1}); !errors.As(err, &nb) || !strings.Contains(nb.Reason, "does not scan") {
		t.Errorf("unterminated body: %v", err)
	}
}

// pgSetup is the schema the broken statements refer to.
const pgSetup = `create schema app;
create schema auth;
create role authenticated;
create function auth.uid() returns text language sql as $$ select current_user::text $$;
create table public.orders (id int, owner text, total int, status text, note text);
create table app.orders (id int, owner text, total int);
create table t (x text, "a)b" text);
create view orders_view as select * from orders;
create function public.check_order() returns trigger language plpgsql as $$ begin return new; end $$;
create function app.audit() returns trigger language plpgsql as $$ begin return null; end $$;
insert into orders (id, owner, total) values (1, 'x', 5);
`

// psqlCommand returns a psql command line for a scratch server: the one the
// libpq environment (PGHOST, PGUSER, ...) names when LATTICE_TEST_PSQL is
// set, else a throwaway cluster made with initdb when the tools are on PATH
// or under pg_config --bindir. Otherwise the test is skipped.
func psqlCommand(t *testing.T) []string {
	t.Helper()
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Skip("psql not on PATH")
	}
	if os.Getenv("LATTICE_TEST_PSQL") != "" {
		return []string{psql, "-X", "-q", "-v", "ON_ERROR_STOP=1"}
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb refuses to run as root; set LATTICE_TEST_PSQL=1 and PG* variables to use a server")
	}
	bin := func(name string) string {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		if out, err := exec.Command("pg_config", "--bindir").Output(); err == nil {
			if p := filepath.Join(strings.TrimSpace(string(out)), name); fileExists(p) {
				return p
			}
		}
		return ""
	}
	initdb, pgctl := bin("initdb"), bin("pg_ctl")
	if initdb == "" || pgctl == "" {
		t.Skip("initdb or pg_ctl not found")
	}
	dir, err := os.MkdirTemp("", "lpg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	data := filepath.Join(dir, "d")
	if out, err := exec.Command(initdb, "-D", data, "-A", "trust", "-U", "postgres").CombinedOutput(); err != nil {
		t.Skipf("initdb: %v\n%s", err, out)
	}
	opts := "-c listen_addresses='' -k " + dir
	if out, err := exec.Command(pgctl, "-D", data, "-o", opts, "-l", filepath.Join(dir, "log"), "-w", "start").CombinedOutput(); err != nil {
		t.Skipf("pg_ctl start: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(pgctl, "-D", data, "-m", "immediate", "-w", "stop").Run() })
	return []string{psql, "-X", "-q", "-v", "ON_ERROR_STOP=1", "-h", dir, "-U", "postgres", "-d", "postgres"}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Every generated break is accepted by a real PostgreSQL server, and the
// broken functions run. Each case runs in a transaction that is rolled back.
// @verifies breaktest:INV-1
func TestGenerateSQLParsesInPostgres(t *testing.T) {
	psql := psqlCommand(t)
	var all []sqlCase
	all = append(append(append(all, sqlPolicyCases...), sqlTriggerCases...), sqlFunctionCases...)
	for _, c := range all {
		if c.reason != "" {
			continue
		}
		got, err := genSQL(c)
		if err != nil {
			t.Errorf("%s: %v", c.label, err)
			continue
		}
		script := "begin;\n" + pgSetup + got + "\n" + c.run + "\nrollback;\n"
		cmd := exec.Command(psql[0], psql[1:]...)
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: server rejects the broken SQL: %v\n%s\n%s", c.label, err, out, got)
		}
	}
}
