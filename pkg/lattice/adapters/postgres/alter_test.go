package postgres

import (
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// resolveFiles parses each file (name, source) and resolves them together,
// in the order given: Resolve sorts them by name itself.
func resolveFiles(t *testing.T, files ...string) []ir.Module {
	t.Helper()
	a := New(nil, nil)
	var mods []ir.Module
	for i := 0; i+1 < len(files); i += 2 {
		mods = append(mods, parse(t, a, files[i], files[i+1]))
	}
	return a.Resolve(mods)
}

// find returns the one symbol with the given FQN across modules.
func find(t *testing.T, mods []ir.Module, fqn string) ir.Symbol {
	t.Helper()
	var got []ir.Symbol
	for _, m := range mods {
		for _, s := range m.Symbols {
			if s.FQN == fqn {
				got = append(got, s)
			}
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d symbols %s, want 1", len(got), fqn)
	}
	return got[0]
}

func hasSymbol(mods []ir.Module, fqn string) bool {
	for _, m := range mods {
		for _, s := range m.Symbols {
			if s.FQN == fqn {
				return true
			}
		}
	}
	return false
}

func setsOf(s ir.Symbol) [][]string {
	var out [][]string
	for _, a := range s.Altered {
		out = append(out, a.Sets)
	}
	return out
}

const createFeeds = `-- @enforces library:INV-9
create policy "own rows" on public.feeds for all using (owner = auth.uid());
`

// An ALTER POLICY after the CREATE is recorded on the CREATE's symbol, with
// the file and line it is on and what it sets, in the order migrations apply
// (file name, then position), whatever order the files were read in.
// @verifies adapters.postgres:INV-3
func TestResolveRecordsAlterPolicy(t *testing.T) {
	mods := resolveFiles(t,
		"db/migrations/005_family.sql", "-- family\nalter policy \"own rows\" on public.feeds using (owner = auth.uid() or is_family(owner));\n",
		"db/migrations/001_init.sql", createFeeds,
		"db/migrations/007_more.sql", "alter policy \"own rows\" on public.feeds to authenticated;\nalter policy \"own rows\" on feeds with check (to_check(owner)) using (owner = 'to');\n",
	)
	s := find(t, mods, "sql.policy.public.feeds.own_rows")
	if s.File != "db/migrations/001_init.sql" || s.Line != 2 {
		t.Errorf("the symbol stays at its CREATE: %s:%d", s.File, s.Line)
	}
	want := []ir.Alteration{
		{File: "db/migrations/005_family.sql", Line: 2, EndLine: 2, Sets: []string{"using"}},
		{File: "db/migrations/007_more.sql", Line: 1, EndLine: 1, Sets: []string{"to"}},
		{File: "db/migrations/007_more.sql", Line: 2, EndLine: 2, Sets: []string{"with_check", "using"}},
	}
	if !reflect.DeepEqual(s.Altered, want) {
		t.Errorf("altered = %+v\nwant     %+v", s.Altered, want)
	}
	for _, m := range mods {
		if len(m.ModuleAnnotations) != 0 {
			t.Errorf("%s: alter markers are consumed by Resolve: %+v", m.File, m.ModuleAnnotations)
		}
	}
}

// A rename gives the symbol its new name and later statements name it so; the
// CREATE's own name is kept for locating its statement.
// @verifies adapters.postgres:INV-3
func TestResolveRenamedPolicy(t *testing.T) {
	mods := resolveFiles(t,
		"db/migrations/001.sql", createFeeds,
		"db/migrations/002.sql", "alter policy \"own rows\" on public.feeds rename to \"family rows\";\n"+
			"alter policy \"family rows\" on public.feeds using (is_family(owner));\n",
	)
	if hasSymbol(mods, "sql.policy.public.feeds.own_rows") {
		t.Error("the old name is gone")
	}
	s := find(t, mods, "sql.policy.public.feeds.family_rows")
	if s.Name != "policy family rows on public.feeds" || s.DeclaredAs != "policy own rows on public.feeds" {
		t.Errorf("name %q declared as %q", s.Name, s.DeclaredAs)
	}
	if got := setsOf(s); !reflect.DeepEqual(got, [][]string{{"rename"}, {"using"}}) {
		t.Errorf("sets = %v", got)
	}
	// The tag is still the CREATE's.
	if len(s.Annotations) == 0 || s.Annotations[0].Kind != "enforces_invariant" {
		t.Errorf("tags = %+v", s.Annotations)
	}
}

// A drop with no later create removes the symbol; a drop followed by a create
// of the same name in a later migration makes that create the symbol, with
// none of the earlier alterations.
// @verifies adapters.postgres:INV-3
func TestResolveDropAndRecreatePolicy(t *testing.T) {
	alter := "alter policy \"own rows\" on public.feeds using (false);\n"
	lone := resolveFiles(t, "db/migrations/001.sql", createFeeds+alter, "db/migrations/002.sql", "drop policy if exists \"own rows\" on public.feeds;\n")
	if hasSymbol(lone, "sql.policy.public.feeds.own_rows") {
		t.Error("a lone DROP POLICY removes the symbol")
	}

	again := resolveFiles(t,
		"db/migrations/001.sql", createFeeds+alter,
		"db/migrations/002.sql", "drop policy \"own rows\" on public.feeds;\ncreate policy \"own rows\" on public.feeds using (owner = auth.uid());\n"+
			"alter policy \"own rows\" on public.feeds to authenticated;\n",
	)
	s := find(t, again, "sql.policy.public.feeds.own_rows")
	if s.File != "db/migrations/002.sql" || s.Line != 2 {
		t.Errorf("the later CREATE is the symbol: %s:%d", s.File, s.Line)
	}
	if got := setsOf(s); !reflect.DeepEqual(got, [][]string{{"to"}}) {
		t.Errorf("only what follows the later CREATE counts: %v", got)
	}
}

// A lone DROP FUNCTION removes the symbol, and an ALTER FUNCTION or ALTER
// TRIGGER is recorded on it.
// @verifies adapters.postgres:INV-3
func TestResolveAlterFunctionAndTrigger(t *testing.T) {
	mods := resolveFiles(t,
		"db/migrations/001.sql", `create function public.f() returns int language sql as $$ select 1 $$;
create function public.g() returns int language sql as $$ select 1 $$;
create trigger t1 before insert on public.orders for each row execute function public.f();
`,
		"db/migrations/002.sql", `alter function public.f() security definer;
drop function public.g();
alter trigger t1 on public.orders rename to t2;
`,
	)
	f := find(t, mods, "sql.public.f")
	if got := setsOf(f); !reflect.DeepEqual(got, [][]string{{"other"}}) {
		t.Errorf("f altered = %v", got)
	}
	if hasSymbol(mods, "sql.public.g") {
		t.Error("a lone DROP FUNCTION removes the symbol")
	}
	tr := find(t, mods, "sql.trigger.public.orders.t2")
	if tr.DeclaredAs != "trigger t1 on public.orders" {
		t.Errorf("declared as %q", tr.DeclaredAs)
	}
}

// Only top-level clauses count: USING and TO inside an expression, a string or
// a quoted name do not.
// @verifies adapters.postgres:INV-3
func TestAlterSets(t *testing.T) {
	for _, c := range []struct {
		rest string
		want []string
		name string
	}{
		{" using (a)", []string{"using"}, ""},
		{" to anon, \"using\" with check (b)", []string{"to", "with_check"}, ""},
		{" using (x = 'to' or y in (select 1 where using_it())) with  check (z)", []string{"using", "with_check"}, ""},
		{" rename to \"New Name\"", []string{"rename"}, "New Name"},
		{" rename to Mixed", []string{"rename"}, "mixed"},
		{" using $$ to ( $$ ", nil, ""},
	} {
		sets, name := alterSets(c.rest, true)
		if c.want == nil {
			c.want = []string{"other"}
		}
		if !reflect.DeepEqual(sets, c.want) || name != c.name {
			t.Errorf("%q: %v %q, want %v %q", c.rest, sets, name, c.want, c.name)
		}
	}
	if sets, _ := alterSets(" to authenticated", false); !strings.EqualFold(strings.Join(sets, ","), "other") {
		t.Errorf("TO means nothing for a function: %v", sets)
	}
}
