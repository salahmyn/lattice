package postgres

import (
	"fmt"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

const wTable = `create table public.jobs (
  id bigint generated always as identity primary key,
  status text not null default 'pending',
  attempts int not null default 0,
  owner_id uuid,
  updated_at timestamptz not null default now()
);
alter table public.jobs add column run_after timestamptz;
`

// resolveModules parses each (path, source) pair and resolves them as one set.
func resolveModules(t *testing.T, files ...string) []ir.Module {
	t.Helper()
	a := New(nil, nil)
	var mods []ir.Module
	for i := 0; i+1 < len(files); i += 2 {
		mods = append(mods, parse(t, a, files[i], files[i+1]))
	}
	return a.Resolve(mods)
}

// writesOf lists a function's writes as "op table cols line".
func writesOf(t *testing.T, mods []ir.Module, fqn string) []string {
	t.Helper()
	for _, m := range mods {
		for _, s := range m.Symbols {
			if s.FQN == fqn {
				var out []string
				for _, w := range s.Writes {
					out = append(out, fmt.Sprintf("%s %s %s %d", w.Op, w.Table, strings.Join(w.Columns, ","), w.Line))
				}
				return out
			}
		}
	}
	t.Fatalf("no symbol %s", fqn)
	return nil
}

func gapsOf(mods []ir.Module) []ir.WriterGap {
	var out []ir.WriterGap
	for _, m := range mods {
		out = append(out, m.WriterGaps...)
	}
	return out
}

func wantWrites(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An UPDATE names the columns of its SET list, however the table is aliased,
// and a lock clause (FOR UPDATE) is not a write.
// @verifies adapters.postgres:INV-7
func TestWritesUpdateAlias(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`create function public.f(p bigint) returns void language plpgsql as $$
begin
  perform 1 from public.jobs j where j.id = p for update;
  update public.jobs j set status = 'x', (attempts, updated_at) = (1, now()) where j.id = p;
  update only public.jobs as jj set run_after = case when p > 1 then now() else null end, "status" = 'y' where jj.id = p;
end $$;
`)
	wantWrites(t, writesOf(t, mods, "sql.public.f"),
		"update public.jobs attempts,status,updated_at 12",
		"update public.jobs run_after,status 13")
	if gaps := gapsOf(mods); len(gaps) != 0 {
		t.Errorf("gaps = %+v", gaps)
	}
}

// An INSERT with no column list writes every column of the table, read from
// its CREATE TABLE and ALTER TABLE ... ADD COLUMN in migration order; a table
// whose columns are not known keeps "*", which is never read as no column.
// @verifies adapters.postgres:INV-7
func TestWritesInsertWithoutColumnList(t *testing.T) {
	mods := resolveModules(t, m1, wTable, m2, `create function public.f() returns void language sql as $$
  insert into public.jobs select * from public.jobs;
  insert into public.jobs (status) values ('a');
  insert into public.other select 1;
$$;
alter table public.jobs drop column attempts;
`)
	wantWrites(t, writesOf(t, mods, "sql.public.f"),
		"insert public.jobs id,owner_id,run_after,status,updated_at 2",
		"insert public.jobs status 3",
		"insert public.other * 4")
}

// ON CONFLICT DO UPDATE SET writes its columns too.
// @verifies adapters.postgres:INV-7
func TestWritesOnConflict(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`create function public.f() returns void language sql as $$
  insert into public.jobs (id, status) values (1, 'a')
  on conflict (id) do update set status = excluded.status, attempts = public.jobs.attempts + 1 where public.jobs.status <> 'done';
  insert into public.jobs (id) values (2) on conflict do nothing;
$$;
`)
	wantWrites(t, writesOf(t, mods, "sql.public.f"),
		"insert public.jobs id,status 10",
		"update public.jobs attempts,status 11",
		"insert public.jobs id 12")
}

// A write inside a CTE, a DELETE and a MERGE are read.
// @verifies adapters.postgres:INV-7
func TestWritesCTEDeleteMerge(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`create function public.f() returns void language plpgsql as $$
begin
  with moved as (update public.jobs set status = 'moved' where id = 1 returning id)
  delete from public.jobs where id in (select id from moved);
  merge into public.jobs t using public.other o on t.id = o.id
    when matched and o.x then update set attempts = 0, updated_at = case when o.y then now() else t.updated_at end
    when not matched then insert (id, status) values (o.id, 'n')
    when matched then delete;
end $$;
`)
	wantWrites(t, writesOf(t, mods, "sql.public.f"),
		"update public.jobs status 11",
		"delete public.jobs  12",
		"update public.jobs attempts,updated_at 14",
		"insert public.jobs id,status 15",
		"delete public.jobs  16")
}

// A trigger function's assignment to NEW.<column> is a write of that column on
// the trigger's table, for the events the trigger fires on; a comparison on
// NEW is not one.
// @verifies adapters.postgres:INV-7
func TestWritesTriggerNew(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`create function public.stamp() returns trigger language plpgsql as $$
begin
  if new.status = 'done' then
    new.updated_at := now();
  end if;
  new.attempts = old.attempts;
  return new;
end $$;
create trigger stamp before insert or update on public.jobs for each row execute function public.stamp();
create function public.unused() returns trigger language plpgsql as $$ begin new.status = 'x'; return new; end $$;
`)
	wantWrites(t, writesOf(t, mods, "sql.public.stamp"),
		"insert public.jobs updated_at 12",
		"update public.jobs updated_at 12",
		"insert public.jobs attempts 14",
		"update public.jobs attempts 14")
	wantWrites(t, writesOf(t, mods, "sql.public.unused"))
}

// A trigger's guard carries its table, its commands and the columns of an
// UPDATE OF; a policy's carries its command.
// @verifies adapters.postgres:INV-7
func TestGuardFacts(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`create function public.f() returns trigger language plpgsql as $$ begin return new; end $$;
create trigger t1 before update of status, attempts or delete on public.jobs for each row execute function public.f();
create policy p1 on public.jobs for update using (true);
create policy p2 on public.jobs using (true);
`)
	got := map[string]string{}
	for _, m := range mods {
		for _, s := range m.Symbols {
			if g := s.Guard; g != nil {
				got[s.FQN] = fmt.Sprintf("%s %s %v %v %s", g.Kind, g.Table, g.Commands, g.Columns, g.Function)
			}
		}
	}
	want := map[string]string{
		"sql.trigger.public.jobs.t1": "trigger public.jobs [delete update] [attempts status] sql.public.f",
		"sql.policy.public.jobs.p1":  "policy public.jobs [update] [] ",
		"sql.policy.public.jobs.p2":  "policy public.jobs [insert update delete] [] ",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
}

// Top-level DML of a migration and of its DO blocks is written by the
// migration itself; a test file is never a writer.
// @verifies adapters.postgres:INV-7
func TestWritesMigrationAndTests(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`update public.jobs set status = 'done' where id = 1;
do $$ begin
  delete from public.jobs where id = 2;
end $$;
`, "supabase/tests/01_jobs.sql", `begin;
select plan(1);
update public.jobs set attempts = 3;
select ok(true, 'x');
select * from finish();
rollback;
`)
	var got []string
	for _, m := range mods {
		for _, w := range m.Writes {
			got = append(got, fmt.Sprintf("%s %s %s %s %d", m.File, w.Op, w.Table, strings.Join(w.Columns, ","), w.Line))
		}
	}
	wantWrites(t, got,
		m1+" update public.jobs status 9",
		m1+" delete public.jobs  11")
}

// A write the adapter cannot read leaves tables unknown, never empty: an
// EXECUTE of a string built at run time or naming no table (every table), one
// that names its table literally (that table), a write through a view, and a
// foreign key's ON DELETE SET NULL (its referencing column). A string that
// holds no write writes nothing.
// @verifies adapters.postgres:INV-7
func TestWriterGaps(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`create view public.open_jobs as select * from public.jobs where status = 'open';
create function public.dyn(t text, v_sql text) returns void language plpgsql as $$
begin
  execute v_sql;
  execute format('update %I set status = ''x''', t);
  execute 'update public.other set a = 1';
  execute 'create index on public.jobs (status)';
  update public.open_jobs set status = 'y';
end $$;
create table public.child (id int, job_id bigint references public.jobs (id) on delete set null, note text);
`)
	got := map[string]string{}
	for _, g := range gapsOf(mods) {
		got[fmt.Sprintf("%s [%s] %d", g.Table, strings.Join(g.Columns, ","), g.Line)] = g.Reason
	}
	for key, frag := range map[string]string{
		"* [] 12":                  "builds its statement at run time",
		"* [] 13":                  "names no table literally",
		"public.other [] 14":       "writes public.other",
		"public.jobs [] 16":        "open_jobs, a view at",
		"public.child [job_id] 18": "ON DELETE SET NULL on the foreign key of public.child (job_id)",
	} {
		reason, ok := got[key]
		if !ok || !strings.Contains(reason, frag) {
			t.Errorf("gap %q = %q, want it to contain %q", key, reason, frag)
		}
	}
	if len(got) != 5 {
		t.Errorf("gaps = %v, want exactly 5 (a CREATE INDEX string writes nothing)", got)
	}
}

// A function replaced by a later migration takes its gaps with it.
// @verifies adapters.postgres:INV-7
func TestWriterGapsFollowTheEffectiveDefinition(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`create function public.f(t text) returns void language plpgsql as $$
begin execute 'update ' || t || ' set a = 1'; end $$;
`, m2, `create or replace function public.f(t text) returns void language plpgsql as $$
begin update public.jobs set status = 'x'; end $$;
`)
	if gaps := gapsOf(mods); len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none once the dynamic function is replaced", gaps)
	}
}

// Table privileges are folded in migration order: a column-level UPDATE grant,
// ALL, a revoke and ALL TABLES IN SCHEMA, with PUBLIC kept apart.
// @verifies adapters.postgres:INV-7
func TestTableGrants(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`grant update (status, attempts) on public.jobs to authenticated;
grant insert, delete on table public.jobs to anon, public;
revoke delete on public.jobs from anon;
grant all on all tables in schema public to service_role;
revoke update (attempts) on public.jobs from authenticated;
grant select on public.jobs to anon;
`)
	var got []string
	for _, g := range mods[0].TableGrants {
		got = append(got, fmt.Sprintf("%s %s %s [%s] %s %d", g.Table, g.Role, g.Op, strings.Join(g.Columns, ","), g.State, g.Line))
	}
	want := []string{
		"public.jobs anon delete [] revoked 11",
		"public.jobs anon insert [] granted 10",
		"public.jobs authenticated update [attempts] revoked 13",
		"public.jobs authenticated update [status] granted 9",
		"public.jobs public delete [] granted 10",
		"public.jobs public insert [] granted 10",
		"public.jobs service_role delete [] granted 12",
		"public.jobs service_role insert [] granted 12",
		"public.jobs service_role update [] granted 12",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("grants:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A GRANT inside a DO block, or one the parser cannot read, leaves who can
// write unknown.
// @verifies adapters.postgres:INV-7
func TestTableGrantGaps(t *testing.T) {
	mods := resolveModules(t, m1, wTable+`do $$ begin
  grant update on public.jobs to authenticated;
  execute 'grant all on all tables in schema public to anon';
end $$;
`)
	got := map[string]bool{}
	for _, g := range gapsOf(mods) {
		got[g.Table] = strings.Contains(g.Reason, "GRANT or REVOKE of table privileges")
	}
	if !got["public.jobs"] || !got["*"] {
		t.Errorf("gaps = %v, want one on public.jobs and one on every table", got)
	}
}
