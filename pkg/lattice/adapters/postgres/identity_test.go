package postgres

import (
	"testing"
)

// A function that reads no identity itself reads it through the SQL function it
// calls, one level: the outer function cites the helper and the helper's read.
// A builtin, an unknown function, a helper that reads none and a helper two
// calls away are not identity.
// @verifies adapters.postgres:INV-10
func TestCallerIdentityThroughHelper(t *testing.T) {
	helper := "create function public.is_admin() returns boolean language sql as $$\n  select auth.uid() is not null\n$$;\n"
	outer := func(body string) string {
		return "create function public.admin_do(p int) returns int language plpgsql as $$\nbegin\n" + body + "\nreturn 1; end $$;\n"
	}
	for _, tc := range []struct {
		name, body, helpers, via string
		line                     int
	}{
		{"unqualified", "  if not is_admin() then raise exception 'no'; end if;", helper, "via public.is_admin() → auth.uid()", 2},
		{"qualified", "  perform public.is_admin();", helper, "via public.is_admin() → auth.uid()", 2},
		{"unknown function", "  perform public.not_here();", helper, "", 0},
		{"builtin", "  perform now(), lower('x');", helper, "", 0},
		{"helper reads none", "  perform public.plain();", "create function public.plain() returns int language sql as $$ select 1 $$;\n", "", 0},
		{"two levels", "  perform public.mid();", helper + "create function public.mid() returns boolean language sql as $$ select public.is_admin() $$;\n", "", 0},
		{"a string is not a call", "  perform 'is_admin()';", helper, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ex := resolveSymbols(t, m1, tc.helpers, m2, outer(tc.body))["sql.public.admin_do"].Execute
			if tc.via == "" {
				if ex.Identity != nil {
					t.Fatalf("Identity = %+v, want none", ex.Identity)
				}
				return
			}
			if ex.Identity == nil || ex.Identity.Via != tc.via || ex.Identity.File != m1 || ex.Identity.Line != tc.line {
				t.Fatalf("Identity = %+v, want %s at %s:%d", ex.Identity, tc.via, m1, tc.line)
			}
		})
	}
}

// A call to a name created with two argument lists is ambiguous, so it is not
// identity.
// @verifies adapters.postgres:INV-10
func TestCallerIdentityHelperOverloaded(t *testing.T) {
	h1 := "create function public.who() returns uuid language sql as $$ select auth.uid() $$;\n"
	h2 := "create function public.who(x int) returns uuid language sql as $$ select auth.uid() $$;\n"
	outer := "create function public.f() returns int language sql as $$ select 1 where public.who() is not null $$;\n"
	if ex := resolveSymbols(t, m1, h1, m2, h2+outer)["sql.public.f"].Execute; ex.Identity != nil {
		t.Errorf("an overloaded helper is not identity: %+v", ex.Identity)
	}
}

// A hash of a parameter compared with a column in a WHERE or an IF is token
// authentication, reported with the parameter and its line. A hash of a column
// or a literal, a hash only assigned, and a comparison with another parameter
// are not.
// @verifies adapters.postgres:INV-9
func TestHashAuthRead(t *testing.T) {
	fn := func(args, body string) string {
		return "create function public.f(" + args + ") returns int\nlanguage plpgsql as $$\nbegin\n" + body + "\nreturn 1; end $$;\n"
	}
	for _, tc := range []struct {
		name, args, body, param string
		line                    int
	}{
		{"where, encode", "p_token text", "  perform 1 from public.devices d where d.token_hash = encode(digest(p_token, 'sha256'), 'hex');", "p_token", 4},
		{"where, hash on the left", "p_token text", "  perform 1 from public.devices where encode(sha256(p_token::bytea), 'hex') = token_hash;", "p_token", 4},
		{"where, schema-qualified", "p_token text", "  perform 1 from public.devices where token_hash = extensions.digest(p_token, 'sha256')::text;", "p_token", 4},
		{"if, variable", "p_secret text", "  select hash into v from public.k;\n  if crypt(p_secret, v) = v then null; end if;", "p_secret", 5},
		{"md5", "p_pw text", "  perform 1 from public.u where pw = md5(p_pw);", "p_pw", 4},
		{"positional", "text", "  perform 1 from public.u where pw = md5($1);", "$1", 4},
		{"none: hash of a column", "p_token text", "  perform 1 from public.u where pw = md5(salt);", "", 0},
		{"none: hash of a literal", "p_token text", "  perform 1 from public.u where pw = md5('x');", "", 0},
		{"none: assigned only", "p_token text", "  v := md5(p_token);", "", 0},
		{"none: returned", "p_token text", "  return md5(p_token);", "", 0},
		{"none: other parameter", "p_a text, p_b text", "  perform 1 where md5(p_a) = p_b;", "", 0},
		{"none: comment", "p_token text", "  -- where pw = md5(p_token)\n  null;", "", 0},
		{"none: string", "p_token text", "  perform 'where pw = md5(p_token)';", "", 0},
		{"none: qualified name", "token text", "  perform 1 from public.u where pw = md5(u.token);", "", 0},
		{"none: not equality", "p_token text", "  perform 1 from public.u where pw <> md5(p_token);", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ex := resolveSymbols(t, m1, fn(tc.args, tc.body))["sql.public.f"].Execute
			if tc.param == "" {
				if ex.HashAuth != nil {
					t.Fatalf("HashAuth = %+v, want none", ex.HashAuth)
				}
				return
			}
			if ex.HashAuth == nil || ex.HashAuth.Param != tc.param || ex.HashAuth.Line != tc.line || ex.HashAuth.File != m1 {
				t.Fatalf("HashAuth = %+v, want %s at %s:%d", ex.HashAuth, tc.param, m1, tc.line)
			}
			if ex.Identity != nil {
				t.Errorf("a hash is not auth.uid(): Identity = %+v", ex.Identity)
			}
		})
	}
}

// A boolean parameter is one whose declared type is boolean or bool; nothing
// is read from a name. An output parameter, an array and another type are not.
// @verifies adapters.postgres:INV-11
func TestBooleanParameters(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       []string
	}{
		{"boolean", "p_ok boolean", []string{"p_ok"}},
		{"bool, mode, default", "in p_a bool default false, p_b int", []string{"p_a"}},
		{"several", "p_a uuid, p_ok boolean, p_ok2 bool", []string{"p_ok", "p_ok2"}},
		{"unnamed", "uuid, boolean", []string{"$2"}},
		{"named like a verdict, typed otherwise", "p_ok text, is_valid integer, verdict int", nil},
		{"array", "p_flags boolean[]", nil},
		{"out", "p_in int, out p_done boolean", nil},
		{"inout", "inout p_done boolean", []string{"p_done"}},
		{"upper case", "P_OK BOOLEAN", []string{"p_ok"}},
		{"none", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "create function public.f(" + tc.args + ") returns int language sql as $$ select 1 $$;\n"
			got := resolveSymbols(t, m1, src)["sql.public.f"].Execute.BoolParams
			if len(got) != len(tc.want) {
				t.Fatalf("BoolParams = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("BoolParams = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
