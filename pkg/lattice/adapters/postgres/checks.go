package postgres

// @module-feature adapters.postgres

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// CHECK constraints are enforcers: a rule the database applies to every row
// written, as a policy or a trigger is. The symbol of a named one is
//
//	sql.check.<schema>.<table>.<name>
//
// and comes from either form that names it:
//
//	alter table <t> add constraint <name> check (<expr>)
//	create table <t> ( ..., constraint <name> check (<expr>), ... )
//
// A CHECK with no name (a column's check (...), an unnamed table check) is
// not a symbol: PostgreSQL derives its name, so nothing stable names it. A
// tag, a SQL comment directly above the line the constraint starts on, binds
// it:
//
//	create table public.orders (
//	  total numeric,
//	  -- @enforces orders:INV-3
//	  constraint total_positive check (total >= 0)
//	);
//
// For an ALTER TABLE with one constraint, a tag above the statement binds it
// too, as it binds a function or a policy. alter table ... drop constraint
// removes the symbol, and adding the name again makes the new one the
// effective definition. rename constraint renames the symbol; validate
// constraint is recorded on it (Symbol.Altered), since it touches the object.

var (
	reCreateTable = regexp.MustCompile(`(?is)^create\s+(?:(?:global\s+|local\s+)?(?:temp(?:orary)?\s+)|unlogged\s+)?table\s+(?:if\s+not\s+exists\s+)?` + qname)
	reAlterTable  = regexp.MustCompile(`(?is)^alter\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?` + qname + `(?:\s*\*)?`)
)

func checkFQN(schemaName, table, name string) string {
	return "sql.check." + seg(schemaName) + "." + seg(table) + "." + seg(name)
}

func checkName(schemaName, table, name string) string {
	return "check " + name + " on " + schemaName + "." + table
}

// ctok is one token of a statement's text, by byte offset.
type ctok struct {
	kind byte // 'w' word, 'q' quoted identifier, 's' string, '$' dollar-quoted body, 'p' one byte of punctuation
	off  int
	end  int
}

// lexText tokenises a statement's text. Comments are already gone from it.
func lexText(s string) []ctok {
	var out []ctok
	ln := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v':
			i++
		case c == '\'' || ((c == 'E' || c == 'e') && i+1 < len(s) && s[i+1] == '\''):
			j := skipToken(s, i, &ln)
			out = append(out, ctok{'s', i, j})
			i = j
		case c == '"':
			j := skipToken(s, i, &ln)
			out = append(out, ctok{'q', i, j})
			i = j
		case c == '$':
			if _, ok := dollarTag(s[i:]); ok {
				j := skipToken(s, i, &ln)
				out = append(out, ctok{'$', i, j})
				i = j
				continue
			}
			out = append(out, ctok{'p', i, i + 1})
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80:
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] == '$' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9' || s[j] >= 0x80) {
				j++
			}
			out = append(out, ctok{'w', i, j})
			i = j
		default:
			out = append(out, ctok{'p', i, i + 1})
			i++
		}
	}
	return out
}

// stmtToks is a statement's tokens, with helpers to read them.
type stmtToks struct {
	text string
	toks []ctok
	base int // offset of text in the statement's text, for line arithmetic
}

func (s stmtToks) word(i int, w string) bool {
	return i >= 0 && i < len(s.toks) && s.toks[i].kind == 'w' && strings.EqualFold(s.text[s.toks[i].off:s.toks[i].end], w)
}

func (s stmtToks) punct(i int, p byte) bool {
	return i >= 0 && i < len(s.toks) && s.toks[i].kind == 'p' && s.text[s.toks[i].off] == p
}

// ident reads token i as an identifier, folded as PostgreSQL folds it.
func (s stmtToks) ident(i int) (string, bool) {
	if i < 0 || i >= len(s.toks) || s.toks[i].kind != 'w' && s.toks[i].kind != 'q' {
		return "", false
	}
	return unquote(s.text[s.toks[i].off:s.toks[i].end]), true
}

// matchParen returns the index of the ")" that closes the "(" at token i, or -1.
func (s stmtToks) matchParen(i int) int {
	depth := 0
	for j := i; j < len(s.toks); j++ {
		switch {
		case s.punct(j, '('):
			depth++
		case s.punct(j, ')'):
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// lineAt is the file line of the byte at offset off of the statement's text.
func (st statement) lineAt(off int) int {
	if off > len(st.text) {
		off = len(st.text)
	}
	return st.line + strings.Count(st.text[:off], "\n")
}

// checkClause is one named CHECK constraint found in a statement.
type checkClause struct {
	name          string
	start, open   int // token indexes: where the clause starts (CONSTRAINT, or ADD) and the "(" after CHECK
	closeAt       int // the ")" that closes it
	line, endLine int
}

// dropClause is one DROP CONSTRAINT, RENAME CONSTRAINT or VALIDATE CONSTRAINT.
type dropClause struct {
	name, to string // to is the new name of a rename
	op       string // "drop", "rename" or "validate"
	at       int    // token index of the action's first token
	line     int
}

// constraintActions reads the CHECK constraints a CREATE TABLE or ALTER TABLE
// statement names, and its DROP, RENAME and VALIDATE CONSTRAINT actions. ok
// is false for any other statement.
func constraintActions(st statement) (schemaName, table string, adds []checkClause, others []dropClause, alter bool, ok bool) {
	if m := reCreateTable.FindStringSubmatch(st.text); m != nil {
		schemaName, table = qualify(m[1], m[2])
		toks := stmtToks{text: st.text, toks: lexText(st.text)}
		// Skip the header's tokens: the column list is the first "(" after it.
		k := 0
		for k < len(toks.toks) && toks.toks[k].off < len(m[0]) {
			k++
		}
		if !toks.punct(k, '(') {
			return schemaName, table, nil, nil, false, true // partition of, of type, as select: no column list
		}
		end := toks.matchParen(k)
		if end < 0 {
			return schemaName, table, nil, nil, false, true
		}
		depth := 0
		for j := k; j < end; j++ {
			switch {
			case toks.punct(j, '('):
				depth++
			case toks.punct(j, ')'):
				depth--
			case depth == 1 && toks.word(j, "constraint") && toks.word(j+2, "check") && toks.punct(j+3, '('):
				if name, isName := toks.ident(j + 1); isName {
					if cl := toks.matchParen(j + 3); cl > 0 {
						adds = append(adds, checkClause{name: name, start: j, open: j + 3, closeAt: cl,
							line: st.lineAt(toks.toks[j].off), endLine: st.lineAt(toks.toks[cl].off)})
					}
				}
			}
		}
		return schemaName, table, adds, nil, false, true
	}
	m := reAlterTable.FindStringSubmatch(st.text)
	if m == nil {
		return "", "", nil, nil, false, false
	}
	schemaName, table = qualify(m[1], m[2])
	toks := stmtToks{text: st.text, toks: lexText(st.text)}
	k := 0
	for k < len(toks.toks) && toks.toks[k].off < len(m[0]) {
		k++
	}
	// Actions are separated by commas outside parentheses.
	for at := k; at < len(toks.toks); {
		end, depth := at, 0
		for ; end < len(toks.toks); end++ {
			if toks.punct(end, '(') {
				depth++
			} else if toks.punct(end, ')') {
				depth--
			} else if depth == 0 && toks.punct(end, ',') {
				break
			}
		}
		line := st.lineAt(toks.toks[at].off)
		switch {
		case toks.word(at, "add") && toks.word(at+1, "constraint") && toks.word(at+3, "check") && toks.punct(at+4, '('):
			if name, isName := toks.ident(at + 2); isName {
				if cl := toks.matchParen(at + 4); cl > 0 && cl < end {
					adds = append(adds, checkClause{name: name, start: at, open: at + 4, closeAt: cl,
						line: line, endLine: st.lineAt(toks.toks[cl].off)})
				}
			}
		case toks.word(at, "drop") && toks.word(at+1, "constraint"):
			i := at + 2
			if toks.word(i, "if") && toks.word(i+1, "exists") {
				i += 2
			}
			if name, isName := toks.ident(i); isName {
				others = append(others, dropClause{name: name, op: "drop", at: at, line: line})
			}
		case toks.word(at, "rename") && toks.word(at+1, "constraint") && toks.word(at+3, "to"):
			from, ok1 := toks.ident(at + 2)
			to, ok2 := toks.ident(at + 4)
			if ok1 && ok2 {
				others = append(others, dropClause{name: from, to: to, op: "rename", at: at, line: line})
			}
		case toks.word(at, "validate") && toks.word(at+1, "constraint"):
			if name, isName := toks.ident(at + 2); isName {
				others = append(others, dropClause{name: name, op: "validate", at: at, line: line})
			}
		}
		at = end + 1
	}
	return schemaName, table, adds, others, true, true
}

// tagsAbove returns the tags directly above a clause that starts on line: the
// run of stand-alone comments inside the statement that ends on the line
// before it. A clause on the statement's own first line takes the comments
// above the statement instead, which is the caller's to pass in.
func tagsAbove(pool []comment, line int) []comment {
	want := line - 1
	var run []comment
	for i := len(pool) - 1; i >= 0; i-- {
		c := pool[i]
		if c.endLine > want {
			continue // after the clause, or a comment of a later clause
		}
		if c.endLine != want {
			break
		}
		run = append([]comment{c}, run...)
		want = c.line - 1
	}
	return run
}

// parseConstraints records the CHECK constraints a CREATE TABLE or ALTER TABLE
// names as symbols, and its DROP, RENAME and VALIDATE actions for Resolve. It
// reports whether the tags above the statement (outer) were put to use, and
// reports the tags inside it that bind nothing as orphaned.
// @enforces adapters.postgres:INV-4
func parseConstraints(mod *ir.Module, st statement, outer []ir.Annotation) (used bool) {
	schemaName, table, adds, others, alter, ok := constraintActions(st)
	if !ok {
		return false
	}
	pool := append([]comment(nil), st.inner...)
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].line < pool[j].line })
	boundInner := map[int]bool{} // inner comments that bound to a clause, by start line
	for _, cl := range adds {
		var anns []ir.Annotation
		switch {
		case cl.line == st.line:
			anns, used = outer, true
		default:
			above := tagsAbove(pool, cl.line)
			for _, c := range above {
				boundInner[c.line] = true
			}
			anns = tags(mod, above)
			if len(anns) == 0 && alter && len(adds) == 1 && len(outer) > 0 {
				// The one constraint of an ALTER TABLE takes the tags above the statement.
				anns, used = outer, true
			}
		}
		fqn := checkFQN(schemaName, table, cl.name)
		sym := ir.Symbol{Name: checkName(schemaName, table, cl.name), FQN: fqn, Kind: ir.KindFunction, File: mod.File,
			Line: cl.line, EndLine: cl.endLine, Exported: true, Root: "check", Annotations: withImpliedFeature(anns)}
		toks := stmtToks{text: st.text, toks: lexText(st.text)}
		expr := st.text[toks.toks[cl.open].end:toks.toks[cl.closeAt].off]
		sym.Refs = callRefs(statement{text: expr, line: st.lineAt(toks.toks[cl.open].end)}, false)
		mod.Symbols = append(mod.Symbols, sym)
		mod.Nodes = append(mod.Nodes, ir.Node{FQN: tableFQN(schemaName, table), Kind: ir.NodeTable, Line: cl.line,
			Refs: []ir.Ref{{Name: sym.Name, Target: sym.FQN, Kind: ir.RefValue, Line: cl.line}}})
	}
	// A tag inside the statement above a line that starts no named CHECK
	// binds nothing; say so, as for a tag above any other statement.
	for _, c := range pool {
		if boundInner[c.line] {
			continue
		}
		if anns := tags(mod, []comment{c}); len(anns) > 0 {
			orphanAt(mod, anns, fmt.Sprintf("a named CHECK constraint (the tag is inside %q, above a line that starts none)", headWords(st.text)))
		}
	}

	for _, d := range others {
		fqn := checkFQN(schemaName, table, d.name)
		switch d.op {
		case "drop":
			if alter && readdsAfter(adds, d) {
				continue // the statement drops it and adds it again: the new one is the definition
			}
			markDrop(mod, fqn, d.line)
		case "rename":
			kw := map[string]interface{}{"sets": []string{setRename}, "end_line": d.line,
				"new_fqn": checkFQN(schemaName, table, d.to), "new_name": checkName(schemaName, table, d.to)}
			mod.ModuleAnnotations = append(mod.ModuleAnnotations,
				ir.Annotation{Kind: alterKind, Args: []interface{}{fqn}, Kwargs: kw, Line: d.line})
		case "validate":
			mod.ModuleAnnotations = append(mod.ModuleAnnotations,
				ir.Annotation{Kind: alterKind, Args: []interface{}{fqn},
					Kwargs: map[string]interface{}{"sets": []string{setOther}, "end_line": d.line}, Line: d.line})
		}
	}
	return used
}

// readdsAfter reports whether the statement adds the dropped constraint's
// name again in an action after the drop.
func readdsAfter(adds []checkClause, d dropClause) bool {
	for _, a := range adds {
		if a.name == d.name && a.start > d.at {
			return true
		}
	}
	return false
}

// headWords is the first words of a statement, for a message.
func headWords(text string) string {
	head := strings.Fields(text)
	if len(head) > 3 {
		head = head[:3]
	}
	return strings.Join(head, " ")
}
