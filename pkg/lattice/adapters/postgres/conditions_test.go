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

// A column compared with a field of a local record variable, or with a hash of
// a parameter, only picks the row: it is not a condition read. A column
// compared with now() in the same WHERE still is.
// @verifies adapters.postgres:INV-13
func TestCondReadsRowSelectorRecordAndHash(t *testing.T) {
	const devices = `create table public.devices (
  id bigint primary key,
  token_hash text,
  last_seen_at timestamptz
);
`
	for _, tc := range []struct {
		name, args, decl, where string
		reads, notReads         []string
	}{
		{"record field", "p_token text", "v_dev public.devices;",
			"d.id = v_dev.id and (d.last_seen_at is null or d.last_seen_at < now() - interval '1 minute')",
			[]string{"public.devices.last_seen_at"}, []string{"public.devices.id"}},
		{"record field, swapped", "p_token text", "v_dev record;",
			"v_dev.id = d.id and d.last_seen_at < now()",
			[]string{"public.devices.last_seen_at"}, []string{"public.devices.id"}},
		{"record field, IN and ANY", "p_token text", "v_dev record;",
			"d.id in (v_dev.id, v_dev.parent_id) and d.token_hash = any(v_dev.hashes)",
			nil, []string{"public.devices.id", "public.devices.token_hash"}},
		{"hash of a parameter", "p_token text", "v int;",
			"d.token_hash = encode(sha256(convert_to(p_token,'UTF8')),'hex') and d.last_seen_at < now()",
			[]string{"public.devices.last_seen_at"}, []string{"public.devices.token_hash"}},
		{"hash of a parameter, swapped", "p_token text", "v int;",
			"encode(digest(p_token, 'sha256'), 'hex') = d.token_hash",
			nil, []string{"public.devices.token_hash"}},
		{"not a selector: hash of a column", "p_token text", "v int;",
			"d.token_hash = encode(sha256(convert_to(d.salt,'UTF8')),'hex')",
			[]string{"public.devices.token_hash"}, nil},
		{"not a selector: a record that is a table alias", "p_token text", "d public.devices;",
			"d.id = d.parent_id",
			[]string{"public.devices.id"}, nil},
		{"not a selector: an undeclared name", "p_token text", "v int;",
			"d.id = v_other.id",
			[]string{"public.devices.id"}, nil},
		{"not a selector: <>", "p_token text", "v_dev record;",
			"d.id <> v_dev.id",
			[]string{"public.devices.id"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mods := resolveModules(t, "m.sql", devices+"create function public.ping("+tc.args+") returns void\nlanguage plpgsql as $$\ndeclare "+tc.decl+"\nbegin\n  update public.devices d set last_seen_at = now()\n where "+tc.where+";\nend $$;\n")
			got := readsOf(t, mods, "sql.public.ping")
			for _, w := range tc.reads {
				if !hasRead(got, w) {
					t.Errorf("reads %v lack %s", got, w)
				}
			}
			for _, n := range tc.notReads {
				if hasRead(got, n) {
					t.Errorf("reads %v include %s, which only picks the row", got, n)
				}
			}
		})
	}
}

// Two tables with the same column names: a function that reads one of them
// records its reads on that table only, through the alias or the schema
// qualified name, and a bare name goes to the table the statement is on.
// @verifies adapters.postgres:INV-14
func TestCondReadsSameColumnsOnTwoTables(t *testing.T) {
	const tables = `create table public.invites (id bigint primary key, email text, expires_at timestamptz);
create table public.family_invites (id bigint primary key, email text, expires_at timestamptz);
`
	mods := resolveModules(t, "m.sql", tables+`create function public.check_invite(p_id bigint) returns boolean
language plpgsql as $$
declare v_email text;
begin
  select i.email into v_email from public.invites i where i.id = p_id and i.expires_at > now();
  if v_email is null then
    return false;
  end if;
  return true;
end $$;
create function public.close_family_invite(p_id bigint) returns void
language sql as $$
  update public.family_invites set expires_at = now() where id = p_id and email is not null;
$$;
`)
	got := readsOf(t, mods, "sql.public.check_invite")
	for _, want := range []string{"public.invites.expires_at", "public.invites.email"} {
		if !hasRead(got, want) {
			t.Errorf("reads %v lack %s", got, want)
		}
	}
	for _, r := range got {
		if strings.Contains(r, "family_invites") {
			t.Errorf("a read of invites is attributed to family_invites: %v", got)
		}
	}
	other := readsOf(t, mods, "sql.public.close_family_invite")
	if !hasRead(other, "public.family_invites.email") {
		t.Errorf("reads %v lack public.family_invites.email", other)
	}
	for _, r := range other {
		if strings.Contains(r, "public.invites.") {
			t.Errorf("a read of family_invites is attributed to invites: %v", other)
		}
	}
}

// A function that writes nothing is read-only: the WHERE of its own SELECT is
// a condition read. In a function that writes, a plain SELECT is not read, as
// before.
// @verifies adapters.postgres:INV-12
func TestCondReadsOfReadOnlyFunction(t *testing.T) {
	const videos = `create table public.videos (id bigint primary key, ai_pending boolean, title text, owner_id uuid);
`
	mods := resolveModules(t, "m.sql", videos+`create function public.visible(p_owner uuid) returns setof public.videos
language sql stable as $$
  select v.* from public.videos v where not v.ai_pending and v.owner_id = p_owner;
$$;
create function public.visible_plpgsql() returns setof public.videos
language plpgsql stable as $$
begin
  return query select * from public.videos where not ai_pending;
end $$;
create function public.rename(p_id bigint) returns void
language plpgsql as $$
begin
  perform 1 from public.videos v where not v.ai_pending;
  update public.videos set title = 'x' where id = p_id;
end $$;
`)
	for _, fqn := range []string{"sql.public.visible", "sql.public.visible_plpgsql"} {
		got := readsOf(t, mods, fqn)
		if !hasRead(got, "public.videos.ai_pending") {
			t.Errorf("%s: reads %v lack public.videos.ai_pending", fqn, got)
		}
	}
	if got := readsOf(t, mods, "sql.public.visible"); hasRead(got, "public.videos.owner_id") {
		t.Errorf("reads %v include owner_id, a row selector by a parameter", got)
	}
	if got := readsOf(t, mods, "sql.public.rename"); len(got) != 0 {
		t.Errorf("a function that writes reads %v, want only the WHERE of its own UPDATE (a selector)", got)
	}
}

// An alias belongs to its statement: the same alias on two tables in one body
// names each table in its own statement.
// @verifies adapters.postgres:INV-14
func TestCondReadsAliasReusedOnTwoTables(t *testing.T) {
	const tables = `create table public.invites (id bigint primary key, email text, expires_at timestamptz);
create table public.family_invites (id bigint primary key, email text, expires_at timestamptz, accepted boolean);
`
	mods := resolveModules(t, "m.sql", tables+`create function public.accept(p_id bigint) returns boolean
language plpgsql as $$
declare v_email text;
begin
  select i.email into v_email from public.invites i where i.id = p_id and i.expires_at > now();
  if v_email is null then
    return false;
  end if;
  update public.family_invites i set accepted = true where i.id = p_id and i.expires_at < now() and i.email = v_email;
  return true;
end $$;
`)
	got := readsOf(t, mods, "sql.public.accept")
	for _, want := range []string{"public.invites.email", "public.invites.expires_at", "public.family_invites.expires_at"} {
		if !hasRead(got, want) {
			t.Errorf("reads %v lack %s", got, want)
		}
	}
	// The SELECT's own read stays on invites (line 7), as does the variable it
	// loaded and the IF that tests it (line 8); the UPDATE's reads are on
	// family_invites (line 11), where the loaded variable is still invites.email.
	want := map[string]bool{"public.invites.expires_at q 7": true, "public.invites.email q 8": true,
		"public.invites.email q 11": true, "public.family_invites.expires_at q 11": true, "public.family_invites.email q 11": true}
	for _, r := range got {
		if !want[r] {
			t.Errorf("unexpected read %q in %v", r, got)
		}
		delete(want, r)
	}
	for r := range want {
		t.Errorf("reads %v lack %q", got, r)
	}
}

// A predicate that compares one column with the caller's identity, either side
// first, is read as such: auth.uid() (also in a sub-select, or cast), a variable
// assigned only from it, and a function called with no arguments for Resolve to
// settle. A comparison with anything else, or between two columns, is not.
// @verifies adapters.postgres:INV-15
func TestOwnerCompare(t *testing.T) {
	idVars, callVars := assignedVars(maskCode(`declare v_uid uuid := auth.uid(); v_other uuid; v_family uuid := public.current_family(); v_rec public.devices;
begin v_other := p_x; v_uid2 := auth.uid(); v_uid2 := p_y; v_fam2 := current_family(); v_fam2 := public.other(); v_mix := auth.uid(); v_mix := public.current_family(); end`))
	if got := callVars["v_family"]; got != "sql.public.current_family" {
		t.Errorf("v_family assigned only from public.current_family() = %q, want the call", got)
	}
	for _, n := range []string{"v_fam2", "v_mix", "v_other", "v_rec"} {
		if _, ok := callVars[n]; ok {
			t.Errorf("%s is not assigned only from one call: %v", n, callVars)
		}
	}
	if _, ok := idVars["v_mix"]; ok {
		t.Errorf("v_mix is also assigned a call: %v", idVars)
	}
	if _, ok := idVars["v_uid"]; !ok {
		t.Fatalf("v_uid assigned only from auth.uid() is an identity variable: %v", idVars)
	}
	if _, ok := idVars["v_other"]; ok {
		t.Errorf("v_other is assigned a parameter: %v", idVars)
	}
	if _, ok := idVars["v_uid2"]; ok {
		t.Errorf("v_uid2 is also assigned a parameter: %v", idVars)
	}
	for _, tc := range []struct {
		pred, col, via, call, callVar string
		ok                            bool
	}{
		{"d.parent_id = auth.uid()", "parent_id", "auth.uid()", "", "", true},
		{"auth.uid() = d.parent_id", "parent_id", "auth.uid()", "", "", true},
		{"d.parent_id = (select auth.uid())", "parent_id", "auth.uid()", "", "", true},
		{"parent_id = auth.uid()::uuid", "parent_id", "auth.uid()", "", "", true},
		{"d.parent_id <> auth.uid()", "parent_id", "auth.uid()", "", "", true},
		{"d.parent_id is distinct from auth.uid()", "parent_id", "auth.uid()", "", "", true},
		{"d.parent_id = v_uid", "parent_id", "v_uid, assigned only from auth.uid()", "", "", true},
		{"d.parent_id = public.current_family()", "parent_id", "", "sql.public.current_family", "", true},
		{"d.parent_id = current_family()", "parent_id", "", "sql.public.current_family", "", true},
		{"d.parent_id = v_other", "", "", "", "", false},
		{"d.parent_id = v_family", "parent_id", "", "sql.public.current_family", "v_family", true},
		{"d.parent_id = v_rec.parent_id", "", "", "", "", false},
		{"k.parent_id = v_dev.parent_id", "", "", "", "", false},
		{"d.parent_id = d.owner_id", "", "", "", "", false},
		{"d.kind = 'a'", "", "", "", "", false},
		{"d.parent_id >= auth.uid()", "", "", "", "", false},
		{"d.parent_id in (auth.uid())", "", "", "", "", false},
		{"coalesce(d.parent_id, auth.uid()) = auth.uid()", "", "", "", "", false},
	} {
		ref, via, call, callVar, ok := ownerCompare(maskCode(tc.pred), idVars, callVars)
		if ok != tc.ok || ok && (ref.name != tc.col || via != tc.via || call != tc.call || callVar != tc.callVar) {
			t.Errorf("%q: got (%v, %q, %q, %q, %v), want (%q, %q, %q, %q, %v)", tc.pred, ref, via, call, callVar, ok, tc.col, tc.via, tc.call, tc.callVar, tc.ok)
		}
	}
}

// An ownership comparison is recorded on the read, and a function called with
// no arguments is settled only when it is a function of the workspace, created
// once, that reads the caller's identity itself.
// @verifies adapters.postgres:INV-15
func TestOwnerReadsResolve(t *testing.T) {
	mods := resolveModules(t, "m.sql", `create table public.devices (id bigint primary key, parent_id uuid, kind text);
create function public.current_family() returns uuid language sql stable as $$ select auth.uid() $$;
create function public.constant() returns uuid language sql stable as $$ select null::uuid $$;
create function public.mine() returns setof public.devices language plpgsql stable as $$
declare v_uid uuid := auth.uid();
begin
  return query select * from public.devices d where d.parent_id = public.current_family();
  return query select * from public.devices d where d.parent_id = v_uid;
  return query select * from public.devices d where d.parent_id = public.constant();
  return query select * from public.devices d where d.kind = 'a';
end $$;
`)
	var got []string
	for _, m := range mods {
		for _, s := range m.Symbols {
			if s.FQN == "sql.public.mine" {
				for _, c := range s.CondReads {
					got = append(got, c.Column+"|"+c.OwnerVia+"|"+c.OwnerCall)
				}
			}
		}
	}
	want := []string{"parent_id|public.current_family() → auth.uid()|", "parent_id|v_uid, assigned only from auth.uid()|",
		"parent_id||", "kind||"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("reads = %q, want %q", got, want)
	}
}

// A guard clause is an IF or ELSIF whose branch, at its own level, raises or
// returns before the end of the body. A notice, a raise only in a nested branch, an
// IF with no raise or return, and a RETURN that is the end of the body are not.
// @verifies adapters.postgres:INV-16
func TestGuardClause(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"raise", `begin if not public.is_admin() then raise exception 'forbidden'; end if; return 1; end`, true},
		{"raise with no level", `begin if x then raise 'no'; end if; return 1; end`, true},
		{"notice", `begin if x then raise notice 'hi'; end if; return 1; end`, false},
		{"early return", `begin if x then return null; end if; select 1; return 2; end`, true},
		{"return query before the end", `begin if x then return query select 1; return; end if; return query select 2; end`, true},
		{"return that ends the body", `begin if x then return 1; end if; end`, false},
		{"return next", `begin if x then return next; end if; return; end`, false},
		{"no raise or return", `begin if x is null then x := 1; end if; return x; end`, false},
		{"raise in a nested branch only", `begin if x then if y then null; end if; y := 1; end if; return 1; end`, false},
		{"elsif raises", `begin if x then y := 1; elsif z then raise exception 'a'; end if; return 1; end`, true},
		{"case when", `begin return case when x then raise_it() else 1 end; end`, false},
	} {
		_, got := readConditionSet(tc.body, 1, nil, true)
		if got != tc.want {
			t.Errorf("%s: guard clause = %v, want %v", tc.name, got, tc.want)
		}
	}
	reads, _ := readConditionSet(`begin
  if (select s.locked from public.settings s) then raise exception 'locked'; end if;
  return (select count(*) from public.devices d where d.last_seen_at > now());
end`, 1, nil, true)
	var guard, payload []string
	for _, c := range reads {
		if c.Column == "s" {
			continue // the alias after a table name; Resolve drops it, the table has no such column
		}
		if c.Guard {
			guard = append(guard, c.Column)
		} else {
			payload = append(payload, c.Column)
		}
	}
	if strings.Join(guard, ",") != "locked" || strings.Join(payload, ",") != "last_seen_at" {
		t.Errorf("guard reads = %v, payload reads = %v", guard, payload)
	}
}

// A condition read is cited on the line of the reference itself, not on the
// line of its statement's WHERE or of the IF: a predicate of a sub-select on a
// later line is cited there.
// @verifies adapters.postgres:INV-17
func TestCondReadsCiteTheirOwnLine(t *testing.T) {
	reads, _ := readConditionSet(`begin
  delete from public.requests r
   where r.status = 'pending'
     and r.device_id in (select d.id from public.devices d
                          where d.paired_by = p_user);
  if exists (select 1 from public.devices k
              where k.kind = 'tv') then
    raise exception 'one tv';
  end if;
end`, 1, map[string]bool{"p_user": true}, false)
	want := map[string]int{
		"public.requests.status":   3,
		"public.devices.paired_by": 5,
		"public.devices.kind":      7,
	}
	got := map[string]int{}
	for _, r := range reads {
		if _, ok := want[r.Table+"."+r.Column]; ok {
			got[r.Table+"."+r.Column] = r.Line
		}
	}
	for k, line := range want {
		if got[k] != line {
			t.Errorf("%s cited at line %d, want %d (reads: %+v)", k, got[k], line, reads)
		}
	}
}
