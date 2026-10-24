package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func parse(t *testing.T, a *Adapter, path, src string) ir.Module {
	t.Helper()
	mod, err := a.Parse(context.Background(), path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return mod
}

func symbolFQNs(mod ir.Module) []string {
	var out []string
	for _, s := range mod.Symbols {
		out = append(out, s.FQN)
	}
	return out
}

// A semicolon inside a dollar-quoted body, a string or a comment does not end
// the statement; line numbers stay right across them.
func TestScanStatements(t *testing.T) {
	src := `-- header
create function f() returns int language plpgsql as $body$
begin
  perform 'a;b'; -- inner;
  return 1;
end $body$;

/* block; comment */
select 'it''s; fine', E'\'; still';
create policy "p" on t using (x = ';');`
	stmts := scan(src)
	if len(stmts) != 3 {
		t.Fatalf("got %d statements: %+v", len(stmts), stmts)
	}
	if stmts[0].line != 2 || stmts[0].endLine != 6 || stmts[1].line != 9 || stmts[2].line != 10 {
		t.Errorf("lines = %d-%d, %d, %d", stmts[0].line, stmts[0].endLine, stmts[1].line, stmts[2].line)
	}
	if len(stmts[1].comments) != 1 || !strings.Contains(stmts[1].comments[0].text, "block") {
		t.Errorf("comments before statement 2 = %+v", stmts[1].comments)
	}
}

const migration = `-- @module-feature billing
-- @enforces billing.refunds:INV-2
create or replace function public.refund(p_id uuid) returns void
language plpgsql security definer set search_path = '' as $$
begin
  if exists (select 1 from refunds where id = p_id) then raise exception 'twice;'; end if;
end $$;

-- @enforces billing.refunds:INV-3
create policy "Owners read own refunds" on public.refunds for select using (owner = auth.uid());

-- @enforces billing.accounts:INV-1
create trigger enforce_limit before insert or update of amount on auth.users
  for each row execute function public.check_limit();

-- not a tag: there is a blank line below

create function Helper() returns int as $$ select 1 $$ language sql;

-- @enforces billing.refunds:INV-9
alter table public.refunds enable row level security;
`

func TestParseMigration(t *testing.T) {
	mod := parse(t, New(nil, nil), "db/migrations/0001_refunds.sql", migration)
	if !mod.ChangeHistory {
		t.Error("a file under migrations/ is change history")
	}
	want := []string{
		"sql.public.refund",
		"sql.policy.public.refunds.Owners_read_own_refunds",
		"sql.trigger.auth.users.enforce_limit",
		"sql.public.helper",
	}
	if got := symbolFQNs(mod); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("symbols = %v, want %v", got, want)
	}
	for i, inv := range []string{"billing.refunds:INV-2", "billing.refunds:INV-3", "billing.accounts:INV-1"} {
		s := mod.Symbols[i]
		if len(s.Annotations) != 2 || s.Annotations[0].Kind != "enforces_invariant" || s.Annotations[0].Args[0] != inv {
			t.Errorf("%s annotations = %+v, want @enforces %s", s.FQN, s.Annotations, inv)
		}
		// The @enforces ref names the feature, so the symbol is attributed to it.
		if f := s.Annotations[1]; f.Kind != "feature" || f.Args[0] != strings.SplitN(inv, ":", 2)[0] {
			t.Errorf("%s implied feature = %+v", s.FQN, f)
		}
		if s.Kind != ir.KindFunction || !s.Exported {
			t.Errorf("%s: kind %s exported %v", s.FQN, s.Kind, s.Exported)
		}
	}
	if len(mod.Symbols[3].Annotations) != 0 {
		t.Error("a comment separated by a blank line does not attach")
	}
	if len(mod.ModuleAnnotations) != 1 || mod.ModuleAnnotations[0].Kind != "module_feature" {
		t.Errorf("module annotations = %+v", mod.ModuleAnnotations)
	}
	if len(mod.Diagnostics) != 1 || mod.Diagnostics[0].Code != schema.CodeAnnotationOrphaned {
		t.Errorf("a tag above alter table is orphaned: %+v", mod.Diagnostics)
	}
}

func TestClassify(t *testing.T) {
	def := New(nil, nil)
	for p, want := range map[string]fileKind{
		"supabase/migrations/1_init.sql": kindMigration,
		"supabase/tests/rls_test.sql":    kindTest,
		"supabase/schemas/core.sql":      kindDefinitions,
	} {
		if got := def.classify(p); got != want {
			t.Errorf("default classify(%s) = %d, want %d", p, got, want)
		}
	}
	custom := New([]string{"db/changes/*.sql"}, []string{"db/checks/**/*.sql"})
	for p, want := range map[string]fileKind{
		"db/changes/1.sql":      kindMigration,
		"db/checks/a/b.sql":     kindTest,
		"supabase/tests/x.sql":  kindDefinitions, // globs replace the defaults
		"db/migrations/old.sql": kindDefinitions,
	} {
		if got := custom.classify(p); got != want {
			t.Errorf("glob classify(%s) = %d, want %d", p, got, want)
		}
	}
}

// Migrations apply in filename order: the last create wins, a drop removes
// the symbol, and a redefinition that loses tags is reported on the
// effective definition.
// @verifies adapters.postgres:INV-1
func TestResolveEffectiveDefinitions(t *testing.T) {
	a := New(nil, nil)
	m1 := parse(t, a, "db/migrations/0001.sql", `
-- @enforces auth.invites:INV-1
create or replace function public.enforce_invite() returns trigger as $$ begin return new; end $$ language plpgsql;
-- @enforces auth.invites:INV-2
create policy p on public.feeds using (true);
create function public.gone() returns int as $$ select 1 $$ language sql;`)
	m2 := parse(t, a, "db/migrations/0002.sql", `
create or replace function public.enforce_invite() returns trigger as $$ begin if new.x then raise exception 'expired'; end if; return new; end $$ language plpgsql;
drop policy if exists p on public.feeds;
drop function if exists public.gone();`)
	schemaFile := parse(t, a, "db/schemas/core.sql", `
-- @enforces core:INV-1
create function public.core_fn() returns int as $$ select 1 $$ language sql;`)

	out := a.Resolve([]ir.Module{m2, m1, schemaFile})
	m2, m1, schemaFile = out[0], out[1], out[2]
	if got := symbolFQNs(m1); len(got) != 0 {
		t.Errorf("0001 keeps no effective definition, got %v", got)
	}
	if got := symbolFQNs(m2); len(got) != 1 || got[0] != "sql.public.enforce_invite" {
		t.Errorf("0002 holds the effective enforce_invite, got %v", got)
	}
	if len(m2.Symbols[0].Annotations) != 0 {
		t.Error("the effective definition carries no tags, so none count")
	}
	if len(m2.Diagnostics) != 1 || m2.Diagnostics[0].Code != CodeTagDropped ||
		!strings.Contains(m2.Diagnostics[0].Message, "@enforces auth.invites:INV-1") ||
		!strings.Contains(m2.Diagnostics[0].Message, "db/migrations/0001.sql") {
		t.Errorf("want SQL_TAG_DROPPED naming the lost tag and the old file, got %+v", m2.Diagnostics)
	}
	if len(m2.ModuleAnnotations) != 0 {
		t.Error("drop markers are consumed by Resolve")
	}
	if got := symbolFQNs(schemaFile); len(got) != 1 {
		t.Errorf("a definitions file keeps its symbol, got %v", got)
	}
}

const pgtap = `begin;
-- @verifies billing.refunds:INV-2
select plan(4);

select tests.create_user('a');
-- @verifies brd.billing:SC-3
select is((select count(*) from refunds), 1::bigint, 'a refund is recorded once');
select throws_ok($$ select public.refund('x') $$, 'P0001', 'twice;', 'a second refund is rejected');
select ok(true, 'a refund is recorded once');
-- @verifies billing.refunds:INV-9
select ok(true);
select * from finish();
rollback;`

func TestParsePgTAP(t *testing.T) {
	mod := parse(t, New(nil, nil), "supabase/tests/refunds_test.sql", pgtap)
	want := []string{
		"supabase.tests.refunds_test.test.a-refund-is-recorded-once",
		"supabase.tests.refunds_test.test.a-second-refund-is-rejected",
		"supabase.tests.refunds_test.test.a-refund-is-recorded-once_2",
	}
	if got := symbolFQNs(mod); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("tests = %v, want %v", got, want)
	}
	first := mod.Symbols[0]
	if !first.IsTest || len(first.Annotations) != 2 {
		t.Fatalf("first assertion: test %v, annotations %+v (want the file tag and its own)", first.IsTest, first.Annotations)
	}
	if first.Annotations[0].Args[0] != "billing.refunds:INV-2" || first.Annotations[1].Args[0] != "brd.billing:SC-3" {
		t.Errorf("annotations = %+v", first.Annotations)
	}
	if len(mod.Symbols[1].Annotations) != 1 {
		t.Errorf("the file-level tag applies to every assertion: %+v", mod.Symbols[1].Annotations)
	}
	if len(mod.Diagnostics) != 1 || !strings.Contains(mod.Diagnostics[0].Message, "no description") {
		t.Errorf("a tagged assertion without a description is reported: %+v", mod.Diagnostics)
	}
}

// An explicit @feature wins; refs to two features or to a BRD imply nothing.
// @verifies adapters.postgres:INV-2
func TestImpliedFeature(t *testing.T) {
	enf := func(refs ...interface{}) ir.Annotation { return ir.Annotation{Kind: "enforces_invariant", Args: refs} }
	for name, tc := range map[string]struct {
		anns []ir.Annotation
		want string
	}{
		"one feature":   {[]ir.Annotation{enf("a:INV-1", "a:INV-2")}, "a"},
		"two features":  {[]ir.Annotation{enf("a:INV-1"), enf("b:INV-1")}, ""},
		"brd criterion": {[]ir.Annotation{enf("brd.x:SC-1")}, ""},
		"explicit":      {[]ir.Annotation{{Kind: "feature", Args: []interface{}{"z"}}, enf("a:INV-1")}, "z"},
	} {
		got := ""
		for _, a := range withImpliedFeature(tc.anns) {
			if a.Kind == "feature" {
				got = a.Args[0].(string)
			}
		}
		if got != tc.want {
			t.Errorf("%s: feature = %q, want %q", name, got, tc.want)
		}
	}
}
