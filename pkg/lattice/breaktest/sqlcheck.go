package breaktest

// @module-feature breaktest

import (
	"fmt"
	"strings"
)

// A CHECK constraint is broken the way a policy is: CHECK (expr) becomes
// CHECK (true OR (expr)), so the constraint admits every row while expr stays
// in place and still has to resolve. The symbol is sql.check.<schema>.<table>.
// <name>, and its statement is either an ALTER TABLE ... ADD CONSTRAINT or a
// named constraint inside CREATE TABLE.

// isCheckSymbol reports whether the symbol is a CHECK constraint: the
// PostgreSQL adapter names it "check <name> on <schema>.<table>".
func isCheckSymbol(sym Symbol) bool { return strings.HasPrefix(sym.Name, "check ") }

// checkWanted splits a check symbol's name into the constraint's name and the
// table "schema.table", using the name its own statement gave it.
func checkWanted(sym Symbol) (name, table string, ok bool) {
	n := sym.Name
	if sym.DeclaredAs != "" {
		n = sym.DeclaredAs
	}
	rest, found := strings.CutPrefix(n, "check ")
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, " on ")
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+len(" on "):], true
}

// checkAt is a CHECK constraint located in a file.
type checkAt struct {
	st            sqlStmt
	alter         bool // ADD CONSTRAINT of an ALTER TABLE, not a CREATE TABLE
	open, closeAt int  // the "(" after CHECK and its ")" (token indexes)
	// For an ALTER TABLE: where the table's name ends, the token that is the
	// constraint's name, and the action's span from ADD to its last token.
	tableEnd, nameTok int
	lo, hi            int
	// For a CREATE TABLE: the table's name tokens (first and last) and the
	// last token of the constraint's element, after any NO INHERIT.
	tblFrom, tblTo, tailTo int
}

// locateCheck finds the constraint a check symbol names: the clause on the
// symbol's line, in a statement of its table, with its name.
func locateCheck(src []byte, sym Symbol) (checkAt, error) {
	name, table, ok := checkWanted(sym)
	if !ok {
		return checkAt{}, noBreak("%q is not a CHECK constraint symbol; add a script", sym.Name)
	}
	toks, err := sqlLex(src, 0, len(src), 1)
	if err != nil {
		return checkAt{}, noBreak("SQL does not scan: %v; add a script", err)
	}
	for _, st := range sqlStatements(src, toks) {
		if at, found := checkIn(st, name, table, sym.Line); found {
			return at, nil
		}
	}
	return checkAt{}, noBreak("no CHECK constraint %s on %s at line %d", name, table, sym.Line)
}

// tableOf reads schema.table at token i, giving it as the adapter names it.
func tableOf(st sqlStmt, i int) (string, int, bool) {
	sch, tbl, next, ok := st.qname(i)
	return sch + "." + tbl, next, ok
}

// checkIn looks for the named constraint of table that starts on line in one
// statement.
func checkIn(st sqlStmt, name, table string, line int) (checkAt, bool) {
	if len(st.toks) == 0 {
		return checkAt{}, false
	}
	switch {
	case st.is(0, "create"):
		i := 1
		if st.is(i, "global") || st.is(i, "local") {
			i++
		}
		if st.is(i, "temp") || st.is(i, "temporary") || st.is(i, "unlogged") {
			i++
		}
		if !st.is(i, "table") {
			return checkAt{}, false
		}
		i++
		if st.is(i, "if") && st.is(i+1, "not") && st.is(i+2, "exists") {
			i += 3
		}
		tblFrom := i
		t, next, ok := tableOf(st, i)
		if !ok || t != table || !st.punct(next, '(') {
			return checkAt{}, false
		}
		end := st.matchParen(next)
		if end < 0 {
			return checkAt{}, false
		}
		depth := 0
		for j := next; j < end; j++ {
			switch {
			case st.punct(j, '('):
				depth++
			case st.punct(j, ')'):
				depth--
			case depth == 1 && st.is(j, "constraint") && st.is(j+2, "check") && st.punct(j+3, '(') && st.toks[j].line == line:
				if n, ok := st.ident(j + 1); ok && n == name {
					if cl := st.matchParen(j + 3); cl > 0 {
						tail := cl
						for tail+1 < end && !st.punct(tail+1, ',') {
							tail++
						}
						return checkAt{st: st, open: j + 3, closeAt: cl, nameTok: j + 1, tblFrom: tblFrom, tblTo: next - 1, tailTo: tail}, true
					}
				}
			}
		}
	case st.is(0, "alter") && st.is(1, "table"):
		i := 2
		if st.is(i, "if") && st.is(i+1, "exists") {
			i += 2
		}
		if st.is(i, "only") {
			i++
		}
		t, next, ok := tableOf(st, i)
		if !ok || t != table {
			return checkAt{}, false
		}
		tableEnd := next - 1
		if st.punct(next, '*') {
			next++
		}
		for at := next; at < len(st.toks); {
			end, depth := at, 0
			for ; end < len(st.toks); end++ {
				if st.punct(end, '(') {
					depth++
				} else if st.punct(end, ')') {
					depth--
				} else if depth == 0 && st.punct(end, ',') {
					break
				}
			}
			if st.is(at, "add") && st.is(at+1, "constraint") && st.is(at+3, "check") && st.punct(at+4, '(') && st.toks[at].line == line {
				if n, ok := st.ident(at + 2); ok && n == name {
					if cl := st.matchParen(at + 4); cl > 0 && cl < end {
						return checkAt{st: st, alter: true, open: at + 4, closeAt: cl, tableEnd: tableEnd, nameTok: at + 2,
							lo: st.toks[at].start, hi: st.toks[end-1].end}, true
					}
				}
			}
			at = end + 1
		}
	}
	return checkAt{}, false
}

// checkBreak is the break of a located CHECK: its expression always passes.
func (c checkAt) checkBreak() (Break, error) {
	if c.closeAt == c.open+1 {
		return Break{}, noBreak("empty CHECK expression; add a script")
	}
	return Break{Offset: c.st.toks[c.open].end, Text: "true OR (",
		More: []Break{{Offset: c.st.toks[c.closeAt].start, Text: ")"}}}, nil
}

// planCheck is planSQL for a CHECK constraint. The statement that defines it
// is its own: a later drop and add makes that statement the symbol, so the
// symbol's statement is always the effective one. The object swap is offered
// for a constraint that nothing touches later: an ADD CONSTRAINT as the
// ALTER TABLE statement itself, one inside CREATE TABLE as an ALTER TABLE that
// drops and adds it back (see swapCheck). A later DROP, re-add, rename,
// VALIDATE, or ALTER COLUMN of the same table makes the swap unavailable,
// and the full reset (which applies the migrations) is the way to run it.
// @enforces breaktest:INV-17
func planCheck(file string, src []byte, sym Symbol) (breakPlan, error) {
	c, err := locateCheck(src, sym)
	if err != nil {
		return breakPlan{}, err
	}
	brk, err := c.checkBreak()
	if err != nil {
		return breakPlan{}, err
	}
	why := ""
	switch {
	case len(sym.Altered) > 0:
		a := sym.Altered[0]
		why = fmt.Sprintf(": altered later in %s:%d", a.File, a.Line)
	case !c.alter && sym.DeclaredAs != "":
		why = ": the constraint is renamed later"
	case !c.alter:
		if line, found := laterColumnAlter(src, c.st, sym); found {
			why = fmt.Sprintf(": the table is altered later at line %d (ALTER COLUMN)", line)
		}
	}
	return breakPlan{edits: []fileEdit{{file: file, src: src, brk: brk, line: sym.Line}}, swapWhy: why}, nil
}

// laterColumnAlter finds an ALTER TABLE ... ALTER [COLUMN] on the table of a
// CHECK inside CREATE TABLE, in a statement after it in the same file.
func laterColumnAlter(src []byte, create sqlStmt, sym Symbol) (int, bool) {
	_, table, ok := checkWanted(sym)
	if !ok {
		return 0, false
	}
	toks, err := sqlLex(src, 0, len(src), 1)
	if err != nil {
		return 0, false
	}
	for _, st := range sqlStatements(src, toks) {
		if len(st.toks) == 0 || st.toks[0].start <= create.toks[0].start || !st.is(0, "alter") || !st.is(1, "table") {
			continue
		}
		i := 2
		if st.is(i, "if") && st.is(i+1, "exists") {
			i += 2
		}
		if st.is(i, "only") {
			i++
		}
		t, next, ok := tableOf(st, i)
		if !ok || t != table {
			continue
		}
		depth := 0
		for j := next; j < len(st.toks); j++ {
			switch {
			case st.punct(j, '('):
				depth++
			case st.punct(j, ')'):
				depth--
			case depth == 0 && st.is(j, "alter"):
				return st.toks[j].line, true
			}
		}
	}
	return 0, false
}

// swapCheck is swapStatements for a CHECK added by ALTER TABLE: the table's
// DROP CONSTRAINT IF EXISTS, then the ADD CONSTRAINT clause (with whatever
// follows it in that action, such as NOT VALID), broken or as written.
func swapCheck(src []byte, brk Break, sym Symbol) (patched, original string, ok bool) {
	c, err := locateCheck(src, sym)
	if err != nil {
		return "", "", false
	}
	if !c.alter {
		return swapInlineCheck(src, brk, c)
	}
	all := append([]Break{{Offset: brk.Offset, Text: brk.Text}}, brk.More...)
	for _, b := range all {
		if b.Offset < c.lo || b.Offset > c.hi {
			return "", "", false
		}
	}
	patched, ok = applyWithin(src, brk, c.lo, c.hi)
	if !ok {
		return "", "", false
	}
	st := c.st
	prefix := string(src[st.toks[0].start:st.toks[c.tableEnd].end])
	name := string(src[st.toks[c.nameTok].start:st.toks[c.nameTok].end])
	drop := fmt.Sprintf("%s DROP CONSTRAINT IF EXISTS %s;\n", prefix, name)
	return drop + prefix + " " + patched + ";\n", drop + prefix + " " + string(src[c.lo:c.hi]) + ";\n", true
}

// swapInlineCheck is the swap of a CHECK inside CREATE TABLE. The table is
// already there when the swap runs, so the constraint is replaced with an
// ALTER TABLE, with the table and the name as the migration wrote them:
//
//   - patched adds CHECK (true OR (expr)) NOT VALID, so rows that exist do
//     not stop it from being added;
//   - original adds the constraint back as written and then validates it, so
//     the restored constraint is checked against the rows, as the inline
//     definition is.
func swapInlineCheck(src []byte, brk Break, c checkAt) (patched, original string, ok bool) {
	st := c.st
	lo, hi := st.toks[c.open].start, st.toks[c.closeAt].end
	all := append([]Break{{Offset: brk.Offset, Text: brk.Text}}, brk.More...)
	for _, b := range all {
		if b.Offset < lo || b.Offset > hi {
			return "", "", false
		}
	}
	broken, ok := applyWithin(src, brk, lo, hi)
	if !ok {
		return "", "", false
	}
	tail := strings.TrimSpace(string(src[hi:st.toks[c.tailTo].end]))
	if tail != "" {
		tail = " " + tail
	}
	table := string(src[st.toks[c.tblFrom].start:st.toks[c.tblTo].end])
	name := string(src[st.toks[c.nameTok].start:st.toks[c.nameTok].end])
	head := fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s, ADD CONSTRAINT %s CHECK ", table, name, name)
	patched = head + broken + tail + " NOT VALID;\n"
	original = head + string(src[lo:hi]) + tail + " NOT VALID;\n" +
		fmt.Sprintf("ALTER TABLE %s VALIDATE CONSTRAINT %s;\n", table, name)
	return patched, original, true
}
