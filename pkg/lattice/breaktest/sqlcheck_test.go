package breaktest

// @module-feature breaktest

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

const checkSrc = `create table public.orders (
  id bigint primary key,
  total numeric not null check (total >= 0),
  -- @enforces orders:INV-3
  constraint total_cap check (total < 1000000)
);

alter table public.orders add constraint paid_has_date check (status <> 'paid' or paid_at is not null) not valid;

alter table only public.orders
  add column note text,
  add constraint "refund le total" check (refund <= total),
  add constraint other check (qty > 0);
`

func checkSym(name string, line int) Symbol {
	return Symbol{Name: name, Kind: "function", Line: line}
}

func applyBreak(src string, b Break) string { return string(b.Apply([]byte(src))) }

// A CHECK's expression always passes: CHECK (expr) becomes
// CHECK (true OR (expr)), in a CREATE TABLE and in an ALTER TABLE ADD
// CONSTRAINT, and only the named constraint on the symbol's line is edited.
// @verifies breaktest:INV-1
func TestGenerateCheckBreak(t *testing.T) {
	for _, c := range []struct {
		name string
		line int
		want string
	}{
		{"check total_cap on public.orders", 5, "constraint total_cap check (true OR (total < 1000000))"},
		{"check paid_has_date on public.orders", 8, "check (true OR (status <> 'paid' or paid_at is not null)) not valid;"},
		{`check refund le total on public.orders`, 12, `add constraint "refund le total" check (true OR (refund <= total)),`},
		{"check other on public.orders", 13, "add constraint other check (true OR (qty > 0));"},
	} {
		brk, err := generateSQL([]byte(checkSrc), checkSym(c.name, c.line))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got := applyBreak(checkSrc, brk)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: the break lacks %q:\n%s", c.name, c.want, got)
		}
		if strings.Count(got, "true OR (") != 1 {
			t.Errorf("%s: the break edited another constraint:\n%s", c.name, got)
		}
	}
}

// An ALTER TABLE ADD CONSTRAINT swaps as DROP CONSTRAINT IF EXISTS and the
// ADD CONSTRAINT clause alone (with what follows it in its action), broken
// and as written; the table is named as the migration wrote it, and the other
// actions of the statement stay out.
// @verifies breaktest:INV-15
func TestSwapCheckStatements(t *testing.T) {
	for _, c := range []struct {
		name     string
		line     int
		patched  string
		original string
	}{
		{
			"check paid_has_date on public.orders", 8,
			"alter table public.orders DROP CONSTRAINT IF EXISTS paid_has_date;\nalter table public.orders add constraint paid_has_date check (true OR (status <> 'paid' or paid_at is not null)) not valid;\n",
			"alter table public.orders DROP CONSTRAINT IF EXISTS paid_has_date;\nalter table public.orders add constraint paid_has_date check (status <> 'paid' or paid_at is not null) not valid;\n",
		},
		{
			`check refund le total on public.orders`, 12,
			"alter table only public.orders DROP CONSTRAINT IF EXISTS \"refund le total\";\nalter table only public.orders add constraint \"refund le total\" check (true OR (refund <= total));\n",
			"alter table only public.orders DROP CONSTRAINT IF EXISTS \"refund le total\";\nalter table only public.orders add constraint \"refund le total\" check (refund <= total);\n",
		},
	} {
		sym := checkSym(c.name, c.line)
		brk, err := generateSQL([]byte(checkSrc), sym)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		patched, original, ok := swapStatements([]byte(checkSrc), brk, sym)
		if !ok || patched != c.patched || original != c.original {
			t.Errorf("%s: ok=%v\npatched:  %q\nwant:     %q\noriginal: %q\nwant:     %q", c.name, ok, patched, c.patched, original, c.original)
		}
	}
}

// A CHECK inside CREATE TABLE is swapped as one ALTER TABLE that drops and adds
// it back, with the table and the name as the migration wrote them: patched is
// not valid so existing rows do not stop it, original is validated.
// @verifies breaktest:INV-15
func TestSwapInlineCheck(t *testing.T) {
	for _, c := range []struct {
		name, src, table string
		line             int
		tail             string
	}{
		{"check total_cap on public.orders", checkSrc, "public.orders", 5, ""},
		{"check total_cap on public.orders", "create table orders (\n  total numeric,\n  constraint total_cap check (total < 10)\n);\n", "orders", 3, ""},
		{"check total_cap on public.orders", "create table \"public\".orders (\n  total numeric,\n  constraint total_cap check (total < 10) no inherit,\n  note text\n);\n", `"public".orders`, 3, " no inherit"},
	} {
		sym := checkSym(c.name, c.line)
		if !strings.Contains(c.src, "public.orders") && !strings.Contains(c.src, `"public".orders`) {
			sym.Name = "check total_cap on public.orders"
		}
		brk, err := generateSQL([]byte(c.src), sym)
		if err != nil {
			t.Fatalf("%s: %v", c.table, err)
		}
		patched, original, ok := swapStatements([]byte(c.src), brk, sym)
		expr := "total < 1000000"
		if c.src != checkSrc {
			expr = "total < 10"
		}
		head := "ALTER TABLE " + c.table + " DROP CONSTRAINT IF EXISTS total_cap, ADD CONSTRAINT total_cap CHECK "
		wantP := head + "(true OR (" + expr + "))" + c.tail + " NOT VALID;\n"
		wantO := head + "(" + expr + ")" + c.tail + " NOT VALID;\nALTER TABLE " + c.table + " VALIDATE CONSTRAINT total_cap;\n"
		if !ok || patched != wantP || original != wantO {
			t.Errorf("%s: ok=%v\npatched:  %q\nwant:     %q\noriginal: %q\nwant:     %q", c.table, ok, patched, wantP, original, wantO)
		}
		plan, err := planBreak("postgres", "db/migrations/001.sql", []byte(c.src), sym, nil)
		if err != nil || plan.swapWhy != "" {
			t.Errorf("%s: the swap is offered: %+v, %v", c.table, plan.swapWhy, err)
		}
	}
}

// A later statement that touches an inline CHECK makes its swap unavailable,
// with the reason: a drop or re-add (the symbol moves to that statement), a
// rename, a VALIDATE (both recorded on the symbol), and an ALTER COLUMN of the
// same table. An ALTER TABLE ADD CONSTRAINT that nothing touches is unchanged.
// @verifies breaktest:INV-15
func TestSwapCheckUnavailable(t *testing.T) {
	sym := checkSym("check total_cap on public.orders", 5)
	for _, c := range []struct {
		label, why string
		sym        Symbol
		src        string
	}{
		{"validated or altered later", "altered later in db/migrations/002.sql:3", withAltered(sym), checkSrc},
		{"renamed", "renamed later", withDeclared(sym), checkSrc},
		{"alter column", "ALTER COLUMN", sym, checkSrc + "\nalter table public.orders\n  alter column total set default 0;\n"},
		{"alter column, only", "ALTER COLUMN", sym, checkSrc + "\nalter table only public.orders alter total type bigint;\n"},
	} {
		plan, err := planBreak("postgres", "db/migrations/001.sql", []byte(c.src), c.sym, nil)
		if err != nil || len(plan.edits) != 1 || !strings.Contains(plan.swapWhy, c.why) {
			t.Errorf("%s: plan = %+v, %v", c.label, plan.swapWhy, err)
		}
	}
	// Another table's ALTER COLUMN does not touch it.
	other := checkSrc + "\nalter table public.items alter column qty set default 1;\n"
	if plan, err := planBreak("postgres", "db/migrations/001.sql", []byte(other), sym, nil); err != nil || plan.swapWhy != "" {
		t.Errorf("another table's ALTER COLUMN blocked the swap: %q, %v", plan.swapWhy, err)
	}
	brk, err := generateSQL([]byte(checkSrc), sym)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := swapStatements([]byte(checkSrc), brk, sym); !ok {
		t.Error("an inline CHECK was not swapped")
	}

	alter := checkSym("check paid_has_date on public.orders", 8)
	alter.Altered = []schema.GraphAlteration{{File: "db/migrations/002.sql", Line: 3}}
	plan, err := planBreak("postgres", "db/migrations/001.sql", []byte(checkSrc), alter, nil)
	if err != nil || !strings.Contains(plan.swapWhy, "altered later in db/migrations/002.sql:3") {
		t.Errorf("plan = %+v, %v", plan, err)
	}
}

func withAltered(s Symbol) Symbol {
	s.Altered = []schema.GraphAlteration{{File: "db/migrations/002.sql", Line: 3}}
	return s
}

func withDeclared(s Symbol) Symbol {
	s.DeclaredAs = s.Name
	s.Name = "check total_limit on public.orders"
	return s
}

// A symbol the file does not hold at that line, a renamed one found by the
// name its statement gave it, and an empty CHECK are handled without guessing.
// @verifies breaktest:INV-1
func TestCheckBreakNotFound(t *testing.T) {
	if _, err := generateSQL([]byte(checkSrc), checkSym("check total_cap on public.orders", 6)); err == nil {
		t.Error("a CHECK was found on the wrong line")
	}
	if _, err := generateSQL([]byte(checkSrc), checkSym("check nope on public.orders", 5)); err == nil {
		t.Error("a CHECK was found under another name")
	}
	if _, err := generateSQL([]byte(checkSrc), checkSym("check total_cap on public.other", 5)); err == nil {
		t.Error("a CHECK was found on another table")
	}
	renamed := checkSym("check total_limit on public.orders", 5)
	renamed.DeclaredAs = "check total_cap on public.orders"
	if _, err := generateSQL([]byte(checkSrc), renamed); err != nil {
		t.Errorf("a renamed CHECK is found by the name its statement gave it: %v", err)
	}
	empty := "alter table t add constraint c check ();\n"
	if _, err := generateSQL([]byte(empty), checkSym("check c on public.t", 1)); err == nil {
		t.Error("an empty CHECK was broken")
	}
}
