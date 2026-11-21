package postgres

import (
	"fmt"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// resolveSymbols parses each file (name, source) and resolves them as one set.
func resolveSymbols(t *testing.T, files ...string) map[string]ir.Symbol {
	t.Helper()
	a := New(nil, nil)
	var mods []ir.Module
	for i := 0; i+1 < len(files); i += 2 {
		mods = append(mods, parse(t, a, files[i], files[i+1]))
	}
	out := map[string]ir.Symbol{}
	for _, m := range a.Resolve(mods) {
		for _, s := range m.Symbols {
			out[s.FQN] = s
		}
	}
	return out
}

// entry returns the role's entry of a function's Execute fact as
// "state file:line", or "none".
func entry(s ir.Symbol, role string) string {
	if s.Execute == nil {
		return "no fact"
	}
	for _, g := range s.Execute.Roles {
		if g.Role == role {
			d := ""
			if g.Default {
				d = " default"
			}
			return fmt.Sprintf("%s %s:%d%s", g.State, g.File, g.Line, d)
		}
	}
	return "none"
}

const fnSQL = `create function public.finish(p_id uuid, p_ok boolean) returns text
language plpgsql security definer set search_path = '' as $$ begin return 'x'; end $$;
`

const m1 = "supabase/migrations/20260101_a.sql"
const m2 = "supabase/migrations/20260102_b.sql"

// The facts of the unfixed example: PUBLIC and anon revoked, authenticated
// granted, SECURITY DEFINER with its line. A later migration's REVOKE
// replaces the entry and cites its own line.
// @verifies adapters.postgres:INV-5
func TestExecuteFactsGrantThenRevoke(t *testing.T) {
	grant := fnSQL + `revoke all on function public.finish(uuid, boolean) from public, anon;
grant execute on function public.finish(uuid, boolean) to authenticated;
`
	got := resolveSymbols(t, m1, grant)["sql.public.finish"]
	for role, want := range map[string]string{
		"public":        "revoked " + m1 + ":3",
		"anon":          "revoked " + m1 + ":3",
		"authenticated": "granted " + m1 + ":4",
		"service_role":  "none",
	} {
		if g := entry(got, role); g != want {
			t.Errorf("%s = %s, want %s", role, g, want)
		}
	}
	if x := got.Execute; !x.SecurityDefiner || x.DefinerFile != m1 || x.DefinerLine != 2 {
		t.Errorf("security = %+v, want definer at %s:2", x, m1)
	}

	fixed := resolveSymbols(t, m1, grant,
		m2, "revoke execute on function public.finish(uuid, boolean) from authenticated;\n")["sql.public.finish"]
	if g := entry(fixed, "authenticated"); g != "revoked "+m2+":1" {
		t.Errorf("after the fix, authenticated = %s", g)
	}
}

// Without any statement PUBLIC holds EXECUTE by default, cited at the CREATE
// line; INVOKER and a later ALTER FUNCTION change the security.
// @verifies adapters.postgres:INV-5
func TestExecuteFactsDefaultsAndSecurity(t *testing.T) {
	got := resolveSymbols(t, m1, "create function public.f() returns int language sql as 'select 1';\n")["sql.public.f"]
	if g := entry(got, "public"); g != "granted "+m1+":1 default" {
		t.Errorf("public = %s", g)
	}
	if got.Execute.SecurityDefiner {
		t.Errorf("no SECURITY clause is INVOKER: %+v", got.Execute)
	}
	got = resolveSymbols(t, m1, "create function public.f() returns int language sql as 'select 1';\n",
		m2, "alter function public.f() security definer;\n")["sql.public.f"]
	if !got.Execute.SecurityDefiner || got.Execute.DefinerFile != m2 || got.Execute.DefinerLine != 1 {
		t.Errorf("after ALTER: %+v", got.Execute)
	}
	got = resolveSymbols(t, m1, fnSQL, m2, "alter function public.finish(uuid, boolean) security invoker;\n")["sql.public.finish"]
	if got.Execute.SecurityDefiner {
		t.Errorf("ALTER ... SECURITY INVOKER: %+v", got.Execute)
	}
}

// The statement forms: ALL FUNCTIONS IN SCHEMA applies to the functions
// defined at that point, GRANT OPTION FOR takes no privilege away, GRANT with
// the options, quoted roles, and a statement on another object is ignored.
// @verifies adapters.postgres:INV-5
func TestExecuteFactsStatementForms(t *testing.T) {
	src := `create function public.a() returns int language sql as 'select 1';
revoke all on all functions in schema public from public;
create function public.b() returns int language sql as 'select 1';
grant execute on all routines in schema public to "Reader" with grant option granted by owner;
revoke grant option for execute on function public.a() from "Reader" cascade;
grant select on all tables in schema public to anon;
grant usage on schema public to anon;
`
	got := resolveSymbols(t, m1, src)
	if g := entry(got["sql.public.a"], "public"); g != "revoked "+m1+":2" {
		t.Errorf("a public = %s", g)
	}
	if g := entry(got["sql.public.b"], "public"); g != "granted "+m1+":3 default" {
		t.Errorf("b was created after the REVOKE, public = %s", g)
	}
	for _, fn := range []string{"sql.public.a", "sql.public.b"} {
		if g := entry(got[fn], "Reader"); g != "granted "+m1+":4" {
			t.Errorf("%s Reader = %s (REVOKE GRANT OPTION FOR keeps the privilege)", fn, g)
		}
	}
	if g := entry(got["sql.public.a"], "anon"); g != "none" {
		t.Errorf("a table grant is not a function grant: %s", g)
	}
}

// ALTER DEFAULT PRIVILEGES applies to functions created later, in its schema
// or any; with FOR ROLE the roles it names become unknown.
// @verifies adapters.postgres:INV-5
func TestExecuteFactsDefaultPrivileges(t *testing.T) {
	src := `create function public.early() returns int language sql as 'select 1';
alter default privileges in schema public revoke execute on functions from public;
alter default privileges grant execute on functions to authenticated;
create function public.late() returns int language sql as 'select 1';
create function other.late() returns int language sql as 'select 1';
alter default privileges for role admin revoke execute on functions from anon;
create function public.later() returns int language sql as 'select 1';
`
	got := resolveSymbols(t, m1, src)
	if g := entry(got["sql.public.early"], "authenticated"); g != "none" {
		t.Errorf("early (created before) authenticated = %s", g)
	}
	if g := entry(got["sql.public.late"], "public"); g != "revoked "+m1+":2" {
		t.Errorf("late public = %s", g)
	}
	if g := entry(got["sql.public.late"], "authenticated"); g != "granted "+m1+":3" {
		t.Errorf("late authenticated = %s", g)
	}
	// A function of another schema gets only the rule that names no schema.
	if g := entry(got["sql.other.late"], "public"); !strings.HasPrefix(g, "granted") {
		t.Errorf("other.late public = %s (the rule was for schema public)", g)
	}
	if g := entry(got["sql.other.late"], "authenticated"); g != "granted "+m1+":3" {
		t.Errorf("other.late authenticated = %s", g)
	}
	later := got["sql.public.later"]
	if g := entry(later, "anon"); !strings.HasPrefix(g, "unknown") {
		t.Errorf("later anon = %s, want unknown (FOR ROLE)", g)
	}
	if r := later.Execute.Roles; len(r) == 0 || !strings.Contains(roleReason(later, "anon"), "FOR ROLE") {
		t.Errorf("the reason names FOR ROLE: %+v", later.Execute.Roles)
	}
}

func roleReason(s ir.Symbol, role string) string {
	for _, g := range s.Execute.Roles {
		if g.Role == role {
			return g.Reason
		}
	}
	return ""
}

// CREATE OR REPLACE keeps privileges; DROP then CREATE resets them to the
// defaults.
// @verifies adapters.postgres:INV-5
func TestExecuteFactsReplaceAndDrop(t *testing.T) {
	base := fnSQL + "revoke all on function public.finish(uuid, boolean) from public;\n"
	got := resolveSymbols(t, m1, base,
		m2, strings.Replace(fnSQL, "create function", "create or replace function", 1))["sql.public.finish"]
	if g := entry(got, "public"); g != "revoked "+m1+":3" {
		t.Errorf("after CREATE OR REPLACE, public = %s", g)
	}
	got = resolveSymbols(t, m1, base,
		m2, "drop function public.finish(uuid, boolean);\n"+fnSQL)["sql.public.finish"]
	if g := entry(got, "public"); g != "granted "+m2+":2 default" {
		t.Errorf("after DROP and CREATE, public = %s", g)
	}
}

// A name created with different argument lists is unknown; so is a function
// a GRANT or REVOKE inside a DO block, an EXECUTE string or a function body
// reaches, and one the parser could not read.
// @verifies adapters.postgres:INV-5
func TestExecuteUnknownNeverClosed(t *testing.T) {
	cases := map[string]struct {
		sql  string
		fqn  string
		want string
	}{
		"overload": {
			sql: "create function public.f(int) returns int language sql as 'select 1';\n" +
				"create function public.f(int, int) returns int language sql as 'select 1';\n" +
				"revoke all on function public.f(int) from public;\n",
			fqn: "sql.public.f", want: "more than one argument list",
		},
		"do block naming it": {
			sql: "create function public.f() returns int language sql as 'select 1';\n" +
				"do $$ begin revoke all on function public.f() from public; end $$;\n",
			fqn: "sql.public.f", want: "inside the statement",
		},
		"do block naming it before it is created": {
			sql: "do $$ begin grant execute on function public.f() to anon; end $$;\n" +
				"create function public.f() returns int language sql as 'select 1';\n",
			fqn: "sql.public.f", want: "inside the statement",
		},
		"do block with a computed name": {
			sql: "create function public.f() returns int language sql as 'select 1';\n" +
				"do $$ declare r record; begin for r in select 1 loop execute format('revoke all on function %s from public', 'x'); end loop; end $$;\n",
			fqn: "sql.public.f", want: "inside the statement",
		},
		"dynamic all functions in a schema": {
			sql: "create function public.f() returns int language sql as 'select 1';\n" +
				"do $$ begin execute 'revoke all on all functions in schema public from public'; end $$;\n",
			fqn: "sql.public.f", want: "inside the statement",
		},
		"function body": {
			sql: "create function public.f() returns int language sql as 'select 1';\n" +
				"create function public.g() returns void language plpgsql as $$ begin execute 'grant execute on function public.f() to anon'; end $$;\n",
			fqn: "sql.public.f", want: "inside the statement",
		},
		"unreadable grant": {
			sql: "create function public.f() returns int language sql as 'select 1';\n" +
				"grant execute on function to anon;\n",
			fqn: "sql.public.f", want: "could not be read",
		},
	}
	for name, c := range cases {
		got := resolveSymbols(t, m1, c.sql)[c.fqn]
		if got.Execute == nil || !strings.Contains(got.Execute.Unknown, c.want) {
			t.Errorf("%s: Unknown = %+v, want it to contain %q", name, got.Execute, c.want)
		}
	}
	// A DO block that only touches another function leaves this one known.
	got := resolveSymbols(t, m1, "create function public.f() returns int language sql as 'select 1';\n"+
		"create function public.g() returns int language sql as 'select 1';\n"+
		"do $$ begin revoke all on function public.g() from public; end $$;\n")
	if got["sql.public.f"].Execute.Unknown != "" || got["sql.public.g"].Execute.Unknown == "" {
		t.Errorf("f = %q, g = %q", got["sql.public.f"].Execute.Unknown, got["sql.public.g"].Execute.Unknown)
	}
}

// A statement that is neither a GRANT nor a REVOKE on functions (comments,
// a table grant, a plain DO block) leaves every function as it was.
// @verifies adapters.postgres:INV-5
func TestExecuteNoFalseUnknown(t *testing.T) {
	got := resolveSymbols(t, m1, fnSQL+
		"-- grant execute on function public.finish() to anon\n"+
		"grant select on public.t to anon;\n"+
		"do $$ begin perform 1; end $$;\n"+
		"comment on function public.finish(uuid, boolean) is 'grant me';\n")["sql.public.finish"]
	if got.Execute.Unknown != "" {
		t.Errorf("Unknown = %q", got.Execute.Unknown)
	}
}

// memberships resolves the files and returns the folded role memberships as
// "member>role state file:line".
func memberships(t *testing.T, files ...string) []string {
	t.Helper()
	a := New(nil, nil)
	var mods []ir.Module
	for i := 0; i+1 < len(files); i += 2 {
		mods = append(mods, parse(t, a, files[i], files[i+1]))
	}
	var out []string
	for _, m := range a.Resolve(mods) {
		for _, x := range m.Memberships {
			out = append(out, fmt.Sprintf("%s>%s %s %s:%d", x.Member, x.Role, x.State, x.File, x.Line))
		}
	}
	return out
}

func wantMembers(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n%s\nwant:\n%s", name, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Role membership is read from GRANT role TO role, REVOKE role FROM role and
// CREATE ROLE ... IN ROLE | ROLE | ADMIN, in apply order; a membership that
// does not pass privileges on automatically, or a statement the adapter
// cannot read, is unknown, never revoked.
// @verifies adapters.postgres:INV-5
func TestMembershipFacts(t *testing.T) {
	wantMembers(t, "grant, with options", memberships(t, m1,
		"grant app_writer, auditor to authenticated, \"Svc\" with admin option granted by owner;\n"),
		"Svc>app_writer granted "+m1+":1", "Svc>auditor granted "+m1+":1",
		"authenticated>app_writer granted "+m1+":1", "authenticated>auditor granted "+m1+":1")
	wantMembers(t, "revoke removes it", memberships(t, m1, "grant app_writer to authenticated;\n",
		m2, "revoke app_writer from authenticated cascade;\n"),
		"authenticated>app_writer revoked "+m2+":1")
	wantMembers(t, "revoke admin option leaves it", memberships(t, m1, "grant app_writer to authenticated;\nrevoke admin option for app_writer from authenticated;\n"),
		"authenticated>app_writer granted "+m1+":1")
	wantMembers(t, "create role", memberships(t, m1,
		"create role reporter login in role app_writer, auditor role helper admin boss;\n"),
		"boss>reporter granted "+m1+":1", "helper>reporter granted "+m1+":1",
		"reporter>app_writer granted "+m1+":1", "reporter>auditor granted "+m1+":1")
	wantMembers(t, "inherit false", memberships(t, m1, "grant app_writer to authenticated with inherit false;\n"),
		"authenticated>app_writer unknown "+m1+":1")
	got := memberships(t, m1, "create role x noinherit in role app_writer;\n")
	if len(got) != 1 || !strings.HasPrefix(got[0], "x>app_writer unknown") {
		t.Errorf("a NOINHERIT member: %v", got)
	}
	got = memberships(t, m1, "create role x in role app_writer;\nalter role x noinherit;\n", m2, "alter role x inherit;\n")
	wantMembers(t, "alter role inherit", got, "x>app_writer granted "+m1+":1")
}

// A membership statement inside a DO block, a function body or an EXECUTE
// string leaves the roles it targets unknown; one that names none leaves every
// role unknown; so does one the parser cannot read.
// @verifies adapters.postgres:INV-5
func TestMembershipUnknown(t *testing.T) {
	for name, c := range map[string]struct{ sql, want string }{
		"do block":    {"do $$ begin grant app_writer to authenticated; end $$;\n", "authenticated>* unknown"},
		"execute":     {"do $$ begin execute 'revoke app_writer from anon'; end $$;\n", "anon>* unknown"},
		"body":        {"create function public.g() returns void language plpgsql as $$ begin grant a to b; end $$;\n", "b>* unknown"},
		"computed":    {"do $$ begin execute format('grant %s to %s', 'a', 'b'); end $$;\n", "*>* unknown"},
		"create role": {"do $$ begin create role x in role app_writer; end $$;\n", "x>* unknown"},
		"unreadable":  {"grant app_writer;\n", "*>* unknown"},
	} {
		got := memberships(t, m1, c.sql)
		if len(got) != 1 || !strings.HasPrefix(got[0], c.want) {
			t.Errorf("%s: %v, want %s", name, got, c.want)
		}
	}
	// An object grant inside a DO block is not a membership statement.
	if got := memberships(t, m1, "do $$ begin grant execute on function public.f() to anon; end $$;\n"); len(got) != 0 {
		t.Errorf("object grant read as membership: %v", got)
	}
}
