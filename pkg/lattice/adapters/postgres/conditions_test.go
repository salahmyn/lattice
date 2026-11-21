package postgres

import (
	"fmt"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// readsOf lists a function's condition reads as "table.column q|b line".
func readsOf(t *testing.T, mods []ir.Module, fqn string) []string {
	t.Helper()
	for _, m := range mods {
		for _, s := range m.Symbols {
			if s.FQN == fqn {
				var out []string
				for _, c := range s.CondReads {
					q := "b"
					if c.Qualified {
						q = "q"
					}
					out = append(out, fmt.Sprintf("%s.%s %s %d", c.Table, c.Column, q, c.Line))
				}
				return out
			}
		}
	}
	t.Fatalf("no symbol %s", fqn)
	return nil
}

func hasRead(reads []string, want string) bool {
	for _, r := range reads {
		if strings.HasPrefix(r, want+" ") {
			return true
		}
	}
	return false
}

// A column loaded by SELECT col INTO v and tested as v in an IF is a read of
// col; the WHERE of the function's own UPDATE and SELECT ... INTO is a read of
// the columns it names, through the table's alias. A column that is only
// assigned is not a read.
// @verifies adapters.postgres:INV-12
func TestCondReadsFollowSelectInto(t *testing.T) {
	mods := resolveModules(t, "m.sql", wTable+`create function public.fail_job(p_id bigint) returns text
language plpgsql as $$
declare v_attempts int;
begin
  select j.attempts into v_attempts from public.jobs j where j.id = p_id for update;
  if v_attempts >= 3 then
    update public.jobs j set status = 'failed', updated_at = now() where j.owner_id is null and j.id = p_id;
    return 'failed';
  end if;
  return 'pending';
end $$;
`)
	got := readsOf(t, mods, "sql.public.fail_job")
	for _, want := range []string{"public.jobs.attempts", "public.jobs.owner_id"} {
		if !hasRead(got, want) {
			t.Errorf("reads %v lack %s", got, want)
		}
	}
	for _, not := range []string{"public.jobs.id", "public.jobs.status", "public.jobs.updated_at"} {
		if hasRead(got, not) {
			t.Errorf("reads %v include %s, which is only assigned", got, not)
		}
	}
}

// CASE WHEN inside an UPDATE reads the updated table's bare columns; an IF
// that tests a parameter or a function result reads no column; a NEW.col read
// in a trigger function lands on the table of the trigger.
// @verifies adapters.postgres:INV-12
func TestCondReadsCaseTriggerAndCheck(t *testing.T) {
	mods := resolveModules(t, "m.sql", wTable+`create function public.bump(p_id bigint, p_ok boolean) returns void
language plpgsql as $$
begin
  if p_ok and now() > now() then return; end if;
  update public.jobs set status = case when attempts >= 3 then 'failed' else 'pending' end where id = p_id;
end $$;
create function public.guard_jobs() returns trigger language plpgsql as $$
begin
  if new.attempts > 3 then raise exception 'too many'; end if;
  new.updated_at := now();
  return new;
end $$;
create trigger jobs_guard before update on public.jobs for each row execute function public.guard_jobs();
alter table public.jobs add constraint cap check (attempts <= 3 and status <> 'x');
`)
	got := readsOf(t, mods, "sql.public.bump")
	if !hasRead(got, "public.jobs.attempts") || hasRead(got, "public.jobs.id") || hasRead(got, "public.jobs.status") {
		t.Errorf("bump reads = %v, want attempts, not the id selector or status", got)
	}
	for _, r := range got {
		if strings.Contains(r, "p_ok") || strings.Contains(r, ".now ") {
			t.Errorf("a parameter or a call is not a column read: %v", got)
		}
	}
	if g := readsOf(t, mods, "sql.public.guard_jobs"); !hasRead(g, "public.jobs.attempts") {
		t.Errorf("guard_jobs reads = %v, want NEW.attempts on the trigger's table", g)
	}
	if c := readsOf(t, mods, "sql.check.public.jobs.cap"); !hasRead(c, "public.jobs.attempts") || !hasRead(c, "public.jobs.status") {
		t.Errorf("check reads = %v, want attempts and status", c)
	}
}

// A column compared by = (or IN, = ANY) with a parameter, or with a variable
// assigned only from parameters, selects the row the function acts on and is
// not a condition read. Literals, other columns, other functions and the
// ordering operators still count, and an IF is untouched.
// @verifies adapters.postgres:INV-12
func TestCondReadsSkipRowSelectors(t *testing.T) {
	mods := resolveModules(t, "m.sql", wTable+`create function public.a(p_id bigint, p_ids bigint[]) returns void
language plpgsql as $$
declare v_id bigint := p_id; v_mixed bigint := p_id;
begin
  v_mixed := 7;
  update public.jobs set updated_at = now()
   where status = 'running' and id = p_id and attempts >= 3 and owner_id in (p_id, p_id) and id = any(p_ids)
     and run_after < now() and id = v_id and attempts <> v_mixed;
end $$;
create function public.b(p_id bigint) returns void language sql as $$
  delete from public.jobs j where j.id = p_id;
$$;
create function public.c(bigint) returns void language sql as $$
  update public.jobs set updated_at = now() where id = $1;
$$;
create function public.d(p_id bigint) returns void language plpgsql as $$
begin
  if exists (select 1 from public.jobs where id = p_id) then return; end if;
end $$;
`)
	a := readsOf(t, mods, "sql.public.a")
	for _, want := range []string{"public.jobs.status", "public.jobs.attempts", "public.jobs.run_after"} {
		if !hasRead(a, want) {
			t.Errorf("a reads %v lack %s", a, want)
		}
	}
	for _, not := range []string{"public.jobs.id", "public.jobs.owner_id"} {
		if hasRead(a, not) {
			t.Errorf("a reads %v include the selector %s", a, not)
		}
	}
	if b := readsOf(t, mods, "sql.public.b"); len(b) != 0 {
		t.Errorf("a WHERE with only a row selector reads no column: %v", b)
	}
	if c := readsOf(t, mods, "sql.public.c"); len(c) != 0 {
		t.Errorf("= $1 is a row selector: %v", c)
	}
	if d := readsOf(t, mods, "sql.public.d"); !hasRead(d, "public.jobs.id") {
		t.Errorf("an IF is not filtered: %v", d)
	}
}
