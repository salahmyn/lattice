package postgres

import (
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

const checksMigration = `-- @module-feature orders

create table public.orders (
  id bigint primary key,
  total numeric not null check (total >= 0),
  /* a note
     over two lines */
  -- @enforces orders:INV-3
  constraint total_cap check (total < 1000000),
  constraint status_known check (status in ('new', 'paid')),
  -- not a tag
  qty int constraint qty_positive check (qty > 0)
);

-- @enforces orders:INV-4
alter table public.orders add constraint paid_has_date check (status <> 'paid' or paid_at is not null) not valid;

alter table only public.orders
  -- @enforces orders:INV-5
  add constraint refund_le_total check (refund <= total),
  add check (qty < 100000);
`

func enforcedBy(s ir.Symbol) []string {
	var out []string
	for _, a := range s.Annotations {
		if a.Kind == "enforces_invariant" {
			for _, x := range a.Args {
				out = append(out, x.(string))
			}
		}
	}
	return out
}

// A named CHECK is a symbol in both forms, sql.check.<schema>.<table>.<name>,
// on the line its clause starts, bound by the tag directly above that line (or
// above the statement, for the ALTER's one constraint). An unnamed CHECK is
// not a symbol.
// @verifies adapters.postgres:INV-4
func TestParseNamedChecks(t *testing.T) {
	mod := parse(t, New(nil, nil), "db/migrations/0001_orders.sql", checksMigration)
	got := map[string]ir.Symbol{}
	for _, s := range mod.Symbols {
		got[s.FQN] = s
	}
	want := map[string]struct {
		name      string
		line      int
		endLine   int
		enforces  []string
		inCreate  bool
		firstCall string
	}{
		"sql.check.public.orders.total_cap":       {"check total_cap on public.orders", 9, 9, []string{"orders:INV-3"}, true, ""},
		"sql.check.public.orders.status_known":    {"check status_known on public.orders", 10, 10, nil, true, ""},
		"sql.check.public.orders.qty_positive":    {"check qty_positive on public.orders", 12, 12, nil, true, ""},
		"sql.check.public.orders.paid_has_date":   {"check paid_has_date on public.orders", 16, 16, []string{"orders:INV-4"}, false, ""},
		"sql.check.public.orders.refund_le_total": {"check refund_le_total on public.orders", 20, 20, []string{"orders:INV-5"}, false, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("symbols = %v, want the %d named checks", symbolFQNs(mod), len(want))
	}
	for fqn, w := range want {
		s, ok := got[fqn]
		if !ok {
			t.Errorf("no symbol %s in %v", fqn, symbolFQNs(mod))
			continue
		}
		if s.Name != w.name || s.Line != w.line || s.EndLine != w.endLine || s.Kind != ir.KindFunction || !s.Exported || s.Root != "check" {
			t.Errorf("%s = name %q lines %d-%d kind %s root %q", fqn, s.Name, s.Line, s.EndLine, s.Kind, s.Root)
		}
		if g := enforcedBy(s); !reflect.DeepEqual(g, w.enforces) {
			t.Errorf("%s enforces %v, want %v", fqn, g, w.enforces)
		}
	}
	for _, d := range mod.Diagnostics {
		t.Errorf("diagnostic %+v: every tag found its constraint", d)
	}
	// The table node reaches each check, as it reaches a policy.
	n := 0
	for _, nd := range mod.Nodes {
		if nd.FQN == "sql.table.public.orders" && nd.Kind == ir.NodeTable {
			n += len(nd.Refs)
		}
	}
	if n != len(want) {
		t.Errorf("the table node refs %d checks, want %d", n, len(want))
	}
}

// A CHECK with no name has no stable name to bind a symbol to, so it is
// skipped, and a tag above it attaches to nothing and is reported.
// @verifies adapters.postgres:INV-4
func TestUnnamedChecksAreSkipped(t *testing.T) {
	mod := parse(t, New(nil, nil), "db/migrations/0001.sql", `create table public.t (
  a int check (a > 0),
  -- @enforces orders:INV-1
  check (a < 10),
  b int
);

-- @enforces orders:INV-2
alter table public.t add check (b > 0);
alter table public.t add constraint c unique (a);
`)
	if len(mod.Symbols) != 0 {
		t.Errorf("symbols = %v, want none", symbolFQNs(mod))
	}
	if len(mod.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v, want the two orphaned tags", mod.Diagnostics)
	}
	for _, d := range mod.Diagnostics {
		if d.Code != schema.CodeAnnotationOrphaned {
			t.Errorf("diagnostic %+v", d)
		}
	}
	if !strings.Contains(mod.Diagnostics[0].Message, "named CHECK") {
		t.Errorf("message = %q", mod.Diagnostics[0].Message)
	}
}

// A constraint dropped by a later migration is gone; adding the name again
// makes the new statement the effective definition, with the tags that
// statement carries (the earlier definition's are not inherited), and
// SQL_TAG_DROPPED reports a redefinition that loses them.
// @verifies adapters.postgres:INV-4
func TestResolveDropAndReaddChecks(t *testing.T) {
	create := "-- @enforces orders:INV-3\nalter table public.orders add constraint total_cap check (total < 100);\n"
	mods := resolveFiles(t,
		"db/migrations/001.sql", create+"-- @enforces orders:INV-4\nalter table public.orders add constraint gone check (a > 0);\n",
		"db/migrations/002.sql", "alter table public.orders drop constraint if exists gone;\n"+
			"alter table public.orders drop constraint total_cap, add constraint total_cap check (total < 500);\n",
		"db/migrations/003.sql", "-- @enforces orders:INV-3\nalter table public.orders add constraint again check (b > 0);\n"+
			"alter table public.orders drop constraint again;\n"+
			"-- @enforces orders:INV-3\nalter table public.orders add constraint again check (b > 1);\n",
	)
	if hasSymbol(mods, "sql.check.public.orders.gone") {
		t.Error("a dropped constraint is still a symbol")
	}
	s := find(t, mods, "sql.check.public.orders.total_cap")
	if s.File != "db/migrations/002.sql" || s.Line != 2 || len(s.Annotations) != 0 {
		t.Errorf("total_cap = %s:%d with tags %v: the statement that drops and adds it again is the definition", s.File, s.Line, s.Annotations)
	}
	var lost bool
	for _, m := range mods {
		for _, d := range m.Diagnostics {
			if d.Code == CodeTagDropped && m.File == "db/migrations/002.sql" && strings.Contains(d.Message, "total_cap") {
				lost = true
			}
		}
	}
	if !lost {
		t.Error("redefining total_cap without its tag raised no SQL_TAG_DROPPED")
	}
	a := find(t, mods, "sql.check.public.orders.again")
	if a.File != "db/migrations/003.sql" || a.Line != 5 || !reflect.DeepEqual(enforcedBy(a), []string{"orders:INV-3"}) {
		t.Errorf("again = %s:%d %v: the last add after a drop is the definition", a.File, a.Line, enforcedBy(a))
	}
}

// A rename gives the symbol its new name and keeps the CREATE's; a validate
// is recorded on it, since a later statement touches the object.
// @verifies adapters.postgres:INV-4
func TestResolveRenameAndValidateCheck(t *testing.T) {
	mods := resolveFiles(t,
		"db/migrations/001.sql", "-- @enforces orders:INV-3\nalter table public.orders add constraint total_cap check (total < 100) not valid;\n",
		"db/migrations/002.sql", "alter table public.orders validate constraint total_cap;\n"+
			"alter table public.orders rename constraint total_cap to total_limit;\n",
	)
	if hasSymbol(mods, "sql.check.public.orders.total_cap") {
		t.Error("the old name is gone")
	}
	s := find(t, mods, "sql.check.public.orders.total_limit")
	if s.Name != "check total_limit on public.orders" || s.DeclaredAs != "check total_cap on public.orders" {
		t.Errorf("name %q declared as %q", s.Name, s.DeclaredAs)
	}
	if got := setsOf(s); !reflect.DeepEqual(got, [][]string{{"other"}, {"rename"}}) {
		t.Errorf("sets = %v", got)
	}
}

// A call inside the expression is a ref of the check, not of every other
// clause of the table.
// @verifies adapters.postgres:INV-4
func TestCheckRefsAreItsOwnExpression(t *testing.T) {
	mod := parse(t, New(nil, nil), "db/migrations/001.sql", `create table public.t (
  a int default next_a(),
  constraint ok check (is_ok(a))
);
`)
	if len(mod.Symbols) != 1 {
		t.Fatalf("symbols = %v", symbolFQNs(mod))
	}
	var names []string
	for _, r := range mod.Symbols[0].Refs {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"is_ok"}) {
		t.Errorf("refs = %v, want only is_ok", names)
	}
}

// A block comment inside a statement keeps its lines, so the line of a clause
// after it is the line in the file.
// @verifies adapters.postgres:INV-4
func TestLinesAcrossBlockCommentsInsideStatements(t *testing.T) {
	mod := parse(t, New(nil, nil), "db/migrations/001.sql", "create table public.t (\n  a int,\n  /* one\n     two\n     three */\n  constraint c check (a > 0)\n);\n")
	if len(mod.Symbols) != 1 || mod.Symbols[0].Line != 6 {
		t.Errorf("symbols = %+v, want the check on line 6", mod.Symbols)
	}
}
