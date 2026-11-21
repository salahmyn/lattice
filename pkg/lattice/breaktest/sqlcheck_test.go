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

// A CHECK inside CREATE TABLE cannot be re-applied on its own: its swap is
// not offered, with the reason, though its break is generated. One that a
// later statement touches is not offered either.
// @verifies breaktest:INV-15
func TestSwapCheckUnavailable(t *testing.T) {
	sym := checkSym("check total_cap on public.orders", 5)
	brk, err := generateSQL([]byte(checkSrc), sym)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := swapStatements([]byte(checkSrc), brk, sym); ok {
		t.Error("an inline CHECK was swapped")
	}
	plan, err := planBreak("postgres", "db/migrations/001.sql", []byte(checkSrc), sym, nil)
	if err != nil || len(plan.edits) != 1 || !strings.Contains(plan.swapWhy, "inside CREATE TABLE") {
		t.Errorf("plan = %+v, %v", plan, err)
	}

	alter := checkSym("check paid_has_date on public.orders", 8)
	alter.Altered = []schema.GraphAlteration{{File: "db/migrations/002.sql", Line: 3}}
	plan, err = planBreak("postgres", "db/migrations/001.sql", []byte(checkSrc), alter, nil)
	if err != nil || !strings.Contains(plan.swapWhy, "altered later in db/migrations/002.sql:3") {
		t.Errorf("plan = %+v, %v", plan, err)
	}
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
