package postgres

// @module-feature adapters.postgres

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Tables, views and table privileges, folded in migration order.
//
//   - Columns: a table's columns come from CREATE TABLE and ALTER TABLE ADD |
//     DROP | RENAME COLUMN. They are used only to expand an INSERT with no
//     column list; a table whose columns are not known keeps the write's
//     columns as "*", which is never read as "no column".
//   - Views: a write to a view writes the tables behind it, which the adapter
//     does not follow, so those tables' writer sets become unknown.
//   - Table privileges: GRANT and REVOKE of INSERT, UPDATE [(columns)] and
//     DELETE (or ALL) on tables, on all tables of a schema, and ALTER DEFAULT
//     PRIVILEGES ... ON TABLES, as the function privileges are read. A role
//     that holds one is a writer the code does not show.

// Annotation kinds for the table events Resolve consumes.
const (
	tableKind         = "sql_table"          // CREATE, ALTER or DROP of a table or view
	tgrantKind        = "sql_tgrant"         // a GRANT or REVOKE of table privileges it understood
	tgrantUnknownKind = "sql_tgrant_unknown" // one it could not resolve
	tdefaultKind      = "sql_tgrant_default" // ALTER DEFAULT PRIVILEGES ... ON TABLES
)

// tableEvent is one CREATE, ALTER or DROP of a table or view.
type tableEvent struct {
	op      string // create, addcol, dropcol, renamecol, drop, rename, move, inherit, view, dropview
	table   string // "<schema>.<table>"
	to      string // the new table, for rename and move
	cols    []string
	col     string
	newCol  string
	unknown string   // why the columns of a created table are not known
	bases   []string // the tables a view reads
	exists  bool     // CREATE ... IF NOT EXISTS
	file    string
	line    int
}

// tgItem is one privilege of a GRANT or REVOKE and its columns.
type tgItem struct {
	op   string
	cols []string
}

// tgEvent is one GRANT or REVOKE of table privileges.
type tgEvent struct {
	verb    string
	tables  []string // "<schema>.<table>"
	schemas []string // ALL TABLES IN SCHEMA, or a default privilege's schemas
	items   []tgItem
	roles   []string
	forRole bool
}

var (
	reDropTable   = regexp.MustCompile(`(?is)^drop\s+table\s+(?:if\s+exists\s+)?(.*)$`)
	reDropView    = regexp.MustCompile(`(?is)^drop\s+view\s+(?:if\s+exists\s+)?(.*)$`)
	reView        = regexp.MustCompile(`(?is)^create\s+(?:or\s+replace\s+)?(?:(?:temp|temporary)\s+)?(?:recursive\s+)?view\s+` + qname)
	reViewFrom    = regexp.MustCompile(`(?is)\b(?:from|join)\s+(?:only\s+)?` + qname)
	reFKAction    = regexp.MustCompile(`(?is)\bon\s+(delete|update)\s+(set\s+null|set\s+default|cascade)\b`)
	reForeign     = regexp.MustCompile(`(?is)\bforeign\s+key\s*\(([^)]*)\)`)
	reAddCol      = regexp.MustCompile(`(?is)^add\s+(?:column\s+)?(?:if\s+not\s+exists\s+)?(` + ident + `)`)
	reDropCol     = regexp.MustCompile(`(?is)^drop\s+(?:column\s+)?(?:if\s+exists\s+)?(` + ident + `)`)
	reRenameCol   = regexp.MustCompile(`(?is)^rename\s+(?:column\s+)?(` + ident + `)\s+to\s+(` + ident + `)`)
	reRenameTbl   = regexp.MustCompile(`(?is)^rename\s+to\s+(` + ident + `)`)
	reSetSchemaTo = regexp.MustCompile(`(?is)^set\s+schema\s+(` + ident + `)`)
	reInherit     = regexp.MustCompile(`(?is)^(?:no\s+)?inherit\b`)
	reTableOpts   = regexp.MustCompile(`(?is)\b(?:inherits|partition\s+of)\b`)
	reRLS         = regexp.MustCompile(`(?is)^(enable|disable|force|no\s+force)\s+row\s+level\s+security\b`)
	reDynRLS      = regexp.MustCompile(`(?is)\b(?:(?:create|alter|drop)\s+policy\b(?:\s+if\s+exists)?\s+(?:` + ident + `)\s+on|alter\s+table\s+(?:only\s+)?(?:if\s+exists\s+)?)\s*` + qname)
	reCtorKind    = regexp.MustCompile(`(?is)^(?:constraint|primary|unique|check|exclude|foreign|like)\b`)
	reDynGrant    = regexp.MustCompile(`(?is)\b(?:grant|revoke)\s+(?:insert|update|delete|all)\b[^;]{0,300}?\bon\s+(?:all\s+tables\s+in\s+schema\b|(?:table\s+)?` + qname + `)`)

	reGrantTable   = regexp.MustCompile(`(?is)^(grant|revoke)\s+(?:grant\s+option\s+for\s+)?(.+?)\s+on\s+(.+)$`)
	reDefaultTable = regexp.MustCompile(`(?is)^alter\s+default\s+privileges\s+(.*?)\b(grant|revoke)\s+(?:grant\s+option\s+for\s+)?(.+?)\s+on\s+tables\s+(?:to|from)\s+(.+)$`)
	reGrantSplit   = regexp.MustCompile(`(?is)^(.+?)\s+(?:to|from)\s+(.+)$`)
	reGrantTail    = regexp.MustCompile(`(?is)\s+(?:with\s+grant\s+option|granted\s+by|cascade|restrict)\b.*$`)
	reAllTables    = regexp.MustCompile(`(?is)^all\s+tables\s+in\s+schema\s+(.+)$`)
	reForRole      = regexp.MustCompile(`(?is)\bfor\s+(?:role|user)\b`)
	reInSchema     = regexp.MustCompile(`(?is)\bin\s+schema\s+((?:` + ident + `)(?:\s*,\s*(?:` + ident + `))*)`)
	reQnameFull    = regexp.MustCompile(`(?is)^` + qname + `$`)
)

// nonTableTargets are the words after ON that name something but a table.
var nonTableTargets = map[string]bool{"function": true, "procedure": true, "routine": true, "sequence": true,
	"schema": true, "database": true, "domain": true, "language": true, "type": true, "foreign": true,
	"large": true, "tablespace": true, "parameter": true, "all": true}

func tableEventOf(mod *ir.Module, st statement, ev tableEvent) {
	ev.file, ev.line = mod.File, st.line
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: tableKind, Line: st.line,
		Kwargs: map[string]interface{}{"ev": ev}})
}

// splitTopSpans splits s at commas outside parentheses, as byte spans.
func splitTopSpans(s string) [][2]int {
	var out [][2]int
	depth, last := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, [2]int{last, i})
				last = i + 1
			}
		}
	}
	return append(out, [2]int{last, len(s)})
}

// readTables reads CREATE, ALTER and DROP of tables and views, and records
// the foreign-key actions that write a column by themselves.
func readTables(mod *ir.Module, st statement) {
	mask := maskCode(st.text)
	switch {
	case reCreateTable.MatchString(st.text):
		createTable(mod, st, mask)
	case reAlterTable.MatchString(st.text):
		alterTable(mod, st, mask)
	case reDropTable.MatchString(st.text):
		m := reDropTable.FindStringSubmatch(mask)
		for _, loc := range qnameAll(m[1]) {
			tableEventOf(mod, st, tableEvent{op: "drop", table: loc})
		}
	case reView.MatchString(mask):
		m := reView.FindStringSubmatchIndex(mask)
		ev := tableEvent{op: "view", table: tableOf(mask[m[2]:m[3]], optGroup(mask, m, 2))}
		for _, f := range reViewFrom.FindAllStringSubmatchIndex(mask[m[1]:], -1) {
			sub := mask[m[1]:]
			ev.bases = append(ev.bases, tableOf(sub[f[2]:f[3]], optGroup(sub, f, 2)))
		}
		ev.bases = uniqueSorted(ev.bases)
		tableEventOf(mod, st, ev)
	case reDropView.MatchString(st.text):
		m := reDropView.FindStringSubmatch(mask)
		for _, loc := range qnameAll(m[1]) {
			tableEventOf(mod, st, tableEvent{op: "dropview", table: loc})
		}
	case reRule.MatchString(st.text):
		m := reRule.FindStringSubmatchIndex(mask)
		t := tableOf(mask[m[2]:m[3]], optGroup(mask, m, 2))
		gapEvent(mod, ir.WriterGap{Table: t, File: mod.File, Line: st.line,
			Reason: fmt.Sprintf("a rule at %s:%d rewrites commands on %s, so what writes it is not what its statements say", mod.File, st.line, t)}, "", 0)
	}
}

// optGroup is the text of capture group g (1-based) of a match, or "".
func optGroup(s string, loc []int, g int) string {
	if g*2+1 >= len(loc) || loc[g*2] < 0 {
		return ""
	}
	return s[loc[g*2]:loc[g*2+1]]
}

// qnameAll lists the tables named in "a, public.b [CASCADE]".
func qnameAll(list string) []string {
	list = regexp.MustCompile(`(?is)\s+(?:cascade|restrict)\s*$`).ReplaceAllString(strings.TrimSpace(list), "")
	var out []string
	for _, sp := range splitTopSpans(list) {
		if m := reQnameFull.FindStringSubmatch(strings.TrimSpace(list[sp[0]:sp[1]])); m != nil {
			out = append(out, tableOf(m[1], m[2]))
		}
	}
	return out
}

// fkGap records the columns a foreign key's ON DELETE or ON UPDATE action
// writes when a referenced row changes.
func fkGap(mod *ir.Module, table string, cols []string, item string, off int, st statement) {
	for _, a := range reFKAction.FindAllStringSubmatch(item, -1) {
		line := st.line + strings.Count(st.text[:off], "\n")
		action := strings.Join(strings.Fields(strings.ToLower(a[0])), " ")
		gapEvent(mod, ir.WriterGap{Table: table, Columns: uniqueSorted(cols), File: mod.File, Line: line,
			Reason: fmt.Sprintf("%s on the foreign key of %s (%s) at %s:%d writes it when a referenced row changes",
				strings.ToUpper(action), table, strings.Join(uniqueSorted(cols), ", "), mod.File, line)}, "", 0)
	}
}

func createTable(mod *ir.Module, st statement, mask string) {
	m := reCreateTable.FindStringSubmatchIndex(st.text)
	table := tableOf(mask[m[2]:m[3]], optGroup(mask, m, 2))
	ev := tableEvent{op: "create", table: table, exists: regexp.MustCompile(`(?is)^create\s+.*?\bif\s+not\s+exists\b`).MatchString(st.text[:m[1]])}
	p := skipWS(mask, m[1])
	if p >= len(mask) || mask[p] != '(' {
		ev.unknown = fmt.Sprintf("%s is created at %s:%d with AS, OF or PARTITION OF, so its columns are not known", table, mod.File, st.line)
		tableEventOf(mod, st, ev)
		return
	}
	close := matchParen(mask, p)
	if close < 0 {
		ev.unknown = fmt.Sprintf("the CREATE TABLE of %s at %s:%d could not be read", table, mod.File, st.line)
		tableEventOf(mod, st, ev)
		return
	}
	if reTableOpts.MatchString(mask[close+1:]) {
		ev.unknown = fmt.Sprintf("%s inherits columns (%s:%d), so its columns are not known", table, mod.File, st.line)
	}
	inner := mask[p+1 : close]
	for _, sp := range splitTopSpans(inner) {
		item := inner[sp[0]:sp[1]]
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		off := p + 1 + sp[0] + len(item) - len(strings.TrimLeft(item, " \t\r\n"))
		if reCtorKind.MatchString(trimmed) {
			if strings.HasPrefix(strings.ToLower(trimmed), "like") {
				ev.unknown = fmt.Sprintf("%s is created LIKE another table (%s:%d), so its columns are not known", table, mod.File, st.line)
				continue
			}
			if f := reForeign.FindStringSubmatch(item); f != nil {
				if cols, ok := columnList(f[1]); ok {
					fkGap(mod, table, cols, item, off, st)
				}
			}
			continue
		}
		col, ok := firstIdent(trimmed)
		if !ok {
			ev.unknown = fmt.Sprintf("a column of %s at %s:%d could not be read, so its columns are not known", table, mod.File, st.line)
			continue
		}
		ev.cols = append(ev.cols, col)
		fkGap(mod, table, []string{col}, item, off, st)
	}
	tableEventOf(mod, st, ev)
}

func alterTable(mod *ir.Module, st statement, mask string) {
	m := reAlterTable.FindStringSubmatchIndex(st.text)
	table := tableOf(mask[m[2]:m[3]], optGroup(mask, m, 2))
	rest := mask[m[1]:]
	for _, sp := range splitTopSpans(rest) {
		item := rest[sp[0]:sp[1]]
		a := strings.TrimSpace(item)
		off := m[1] + sp[0] + len(item) - len(strings.TrimLeft(item, " \t\r\n"))
		word, _ := firstWord(a)
		if r := reRLS.FindStringSubmatch(a); r != nil {
			op := map[string]string{"enable": "rlson", "disable": "rlsoff", "force": "rlsforce"}[strings.ToLower(r[1])]
			if op == "" {
				op = "rlsunforce"
			}
			tableEventOf(mod, st, tableEvent{op: op, table: table})
			continue
		}
		switch word {
		case "add":
			after := strings.TrimSpace(a[3:])
			if reCtorKind.MatchString(strings.TrimPrefix(strings.ToLower(after), "column ")) && !strings.HasPrefix(strings.ToLower(after), "column ") {
				if f := reForeign.FindStringSubmatch(item); f != nil {
					if cols, ok := columnList(f[1]); ok {
						fkGap(mod, table, cols, item, off, st)
					}
				}
				continue
			}
			if c := reAddCol.FindStringSubmatch(a); c != nil {
				col := unquote(c[1])
				tableEventOf(mod, st, tableEvent{op: "addcol", table: table, col: col})
				fkGap(mod, table, []string{col}, item, off, st)
			}
		case "drop":
			if c := reDropCol.FindStringSubmatch(a); c != nil {
				switch strings.ToLower(c[1]) {
				case "constraint", "default", "not", "identity", "expression":
				default:
					tableEventOf(mod, st, tableEvent{op: "dropcol", table: table, col: unquote(c[1])})
				}
			}
		case "rename":
			if c := reRenameCol.FindStringSubmatch(a); c != nil && !strings.EqualFold(c[1], "constraint") {
				tableEventOf(mod, st, tableEvent{op: "renamecol", table: table, col: unquote(c[1]), newCol: unquote(c[2])})
			} else if c := reRenameTbl.FindStringSubmatch(a); c != nil {
				tableEventOf(mod, st, tableEvent{op: "rename", table: table, to: schemaOf(table) + "." + unquote(c[1])})
			}
		case "set":
			if c := reSetSchemaTo.FindStringSubmatch(a); c != nil {
				name := strings.SplitN(table, ".", 2)[1]
				tableEventOf(mod, st, tableEvent{op: "move", table: table, to: unquote(c[1]) + "." + name})
			}
		case "inherit", "no":
			if reInherit.MatchString(a) {
				tableEventOf(mod, st, tableEvent{op: "inherit", table: table,
					unknown: fmt.Sprintf("%s changes its parents at %s:%d, so its columns are not known", table, mod.File, st.line)})
			}
		}
	}
}

// readTableGrants reads a GRANT or REVOKE of table privileges, or an ALTER
// DEFAULT PRIVILEGES ... ON TABLES, into an event. It reports whether the
// statement is a GRANT or REVOKE (of anything).
func readTableGrants(mod *ir.Module, st statement) bool {
	text := strings.TrimSpace(st.text)
	if d := reDefaultTable.FindStringSubmatch(text); d != nil {
		ev := tgEvent{verb: strings.ToLower(d[2]), forRole: reForRole.MatchString(d[1])}
		if s := reInSchema.FindStringSubmatch(d[1]); s != nil {
			for _, id := range strings.Split(s[1], ",") {
				ev.schemas = append(ev.schemas, unquote(strings.TrimSpace(id)))
			}
		}
		var ok bool
		if ev.items, ok = grantItems(d[3]); !ok {
			return true
		}
		if ev.roles, ok = grantRoles(d[4]); !ok {
			tgrantUnknown(mod, st)
			return true
		}
		if strings.HasPrefix(strings.ToLower(d[0]), "alter") && strings.Contains(strings.ToLower(d[3]), "grant option") {
			return true
		}
		mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: tdefaultKind, Line: st.line,
			Kwargs: map[string]interface{}{"ev": ev}})
		return true
	}
	m := reGrantTable.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	verb := strings.ToLower(m[1])
	items, ok := grantItems(m[2])
	target := strings.TrimSpace(m[3])
	if w, _ := firstWord(target); nonTableTargets[w] && !strings.HasPrefix(strings.ToLower(target), "all tables") {
		return true
	}
	if !ok {
		return true // no write privilege: nothing for a writer index
	}
	split := reGrantSplit.FindStringSubmatch(target)
	if split == nil {
		tgrantUnknown(mod, st)
		return true
	}
	roles, good := grantRoles(split[2])
	ev := tgEvent{verb: verb, items: items, roles: roles}
	if a := reAllTables.FindStringSubmatch(split[1]); a != nil {
		for _, id := range strings.Split(a[1], ",") {
			ev.schemas = append(ev.schemas, unquote(strings.TrimSpace(id)))
		}
	} else {
		names := strings.TrimSpace(split[1])
		if w, n := firstWord(names); w == "table" {
			names = strings.TrimSpace(names[n:])
		}
		for _, sp := range splitTopSpans(names) {
			q := reQnameFull.FindStringSubmatch(strings.TrimSpace(names[sp[0]:sp[1]]))
			if q == nil {
				good = false
				break
			}
			ev.tables = append(ev.tables, tableOf(q[1], q[2]))
		}
	}
	if !good {
		tgrantUnknown(mod, st)
		return true
	}
	// REVOKE GRANT OPTION FOR takes no privilege away.
	if verb == "revoke" && regexp.MustCompile(`(?is)^revoke\s+grant\s+option\s+for\b`).MatchString(text) {
		return true
	}
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: tgrantKind, Line: st.line,
		Kwargs: map[string]interface{}{"ev": ev}})
	return true
}

func tgrantUnknown(mod *ir.Module, st statement) {
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: tgrantUnknownKind, Line: st.line,
		Kwargs: map[string]interface{}{"reason": fmt.Sprintf("the GRANT or REVOKE of table privileges at %s:%d could not be read, so who can write any table is unknown", mod.File, st.line)}})
}

// grantItems reads "insert, update (a, b)" and the like into the write
// privileges it names. ok is false when it names none.
func grantItems(list string) ([]tgItem, bool) {
	var items []tgItem
	for _, sp := range splitTopSpans(list) {
		it := strings.TrimSpace(list[sp[0]:sp[1]])
		w, n := firstWord(it)
		var cols []string
		if open := strings.IndexByte(it[n:], '('); open >= 0 {
			if close := matchParen(it, n+open); close > 0 {
				cols, _ = columnList(it[n+open+1 : close])
			}
		}
		switch w {
		case "insert", "update", "delete":
			items = append(items, tgItem{op: w, cols: uniqueSorted(cols)})
		case "all":
			for _, op := range []string{ir.WriteInsert, ir.WriteUpdate, ir.WriteDelete} {
				items = append(items, tgItem{op: op, cols: uniqueSorted(cols)})
			}
		}
	}
	return items, len(items) > 0
}

// grantRoles reads the grantee list of a GRANT or REVOKE.
func grantRoles(s string) ([]string, bool) {
	s = reGrantTail.ReplaceAllString(strings.TrimSpace(s), "")
	return readRoles(&tokens{t: privTokens(s)})
}

// scanDynamicTableGrant finds a GRANT or REVOKE of table privileges inside a
// statement that is not one (a DO block, a function body, an EXECUTE string):
// the table it names literally, or every table, has an unknown set of API
// role writers.
func scanDynamicTableGrant(mod *ir.Module, st statement) {
	for _, loc := range reDynGrant.FindAllStringSubmatchIndex(st.text, -1) {
		table := ir.WriteAll
		if loc[2] >= 0 {
			first := st.text[loc[2]:loc[3]]
			if nonTableTargets[strings.ToLower(first)] && loc[4] < 0 {
				continue
			}
			table = tableOf(first, optGroup(st.text, loc, 2))
		}
		line := st.line + strings.Count(st.text[:loc[0]], "\n")
		gapEvent(mod, ir.WriterGap{Table: table, File: mod.File, Line: line,
			Reason: fmt.Sprintf("a GRANT or REVOKE of table privileges inside the statement at %s:%d is not a plain statement the adapter reads, so who can write %s is unknown", mod.File, line, tableName(table))}, "", 0)
	}
}

func tableName(t string) string {
	if t == ir.WriteAll {
		return "any table"
	}
	return t
}

// tgKey identifies one privilege of one role on one table.
type tgKey struct{ table, role, op string }

type tgRec struct {
	state  string
	file   string
	line   int
	reason string
}

type tgState struct {
	whole *tgRec
	cols  map[string]tgRec
}

type tgRule struct {
	ev   tgEvent
	file string
	line int
}

// tableFold applies table events in the order migrations apply.
type tableFold struct {
	tables map[string]*tableCols
	views  map[string]tableEvent
	grants map[tgKey]*tgState
	rules  []tgRule
	gaps   []ir.WriterGap
	rls    map[string]*ir.TableRLS
}

type tableCols struct {
	cols    []string
	unknown string
}

func newTableFold() *tableFold {
	return &tableFold{tables: map[string]*tableCols{}, views: map[string]tableEvent{}, grants: map[tgKey]*tgState{}, rls: map[string]*ir.TableRLS{}}
}

func (f *tableFold) hasCol(t *tableCols, c string) bool {
	for _, x := range t.cols {
		if x == c {
			return true
		}
	}
	return false
}

func schemaOf(table string) string { return strings.SplitN(table, ".", 2)[0] }

// apply folds a table event.
func (f *tableFold) apply(ev tableEvent) {
	switch ev.op {
	case "create":
		if _, ok := f.tables[ev.table]; ok && ev.exists {
			return
		}
		cols := append([]string(nil), ev.cols...)
		f.tables[ev.table] = &tableCols{cols: cols, unknown: ev.unknown}
		delete(f.views, ev.table)
		for _, r := range f.rules {
			if len(r.ev.schemas) > 0 && !contains(r.ev.schemas, schemaOf(ev.table)) {
				continue
			}
			f.set(r.ev, ev.table, r.file, r.line, true)
		}
	case "addcol":
		if t := f.tables[ev.table]; t != nil && !f.hasCol(t, ev.col) {
			t.cols = append(t.cols, ev.col)
		}
	case "dropcol":
		if t := f.tables[ev.table]; t != nil {
			var out []string
			for _, c := range t.cols {
				if c != ev.col {
					out = append(out, c)
				}
			}
			t.cols = out
		}
	case "renamecol":
		if t := f.tables[ev.table]; t != nil {
			for i, c := range t.cols {
				if c == ev.col {
					t.cols[i] = ev.newCol
				}
			}
		}
	case "drop":
		delete(f.tables, ev.table)
		delete(f.rls, ev.table)
		for k := range f.grants {
			if k.table == ev.table {
				delete(f.grants, k)
			}
		}
	case "rename":
		if t := f.tables[ev.table]; t != nil {
			delete(f.tables, ev.table)
			f.tables[ev.to] = t
		}
		if r := f.rls[ev.table]; r != nil {
			delete(f.rls, ev.table)
			r.Table = ev.to
			f.rls[ev.to] = r
		}
		for k, v := range f.grants {
			if k.table == ev.table {
				delete(f.grants, k)
				k.table = ev.to
				f.grants[k] = v
			}
		}
	case "move":
		delete(f.tables, ev.table)
		f.tables[ev.to] = &tableCols{unknown: fmt.Sprintf("%s was moved to %s at %s:%d, which the adapter does not follow", ev.table, ev.to, ev.file, ev.line)}
		f.gaps = append(f.gaps, ir.WriterGap{Table: ev.to, File: ev.file, Line: ev.line,
			Reason: fmt.Sprintf("ALTER TABLE ... SET SCHEMA at %s:%d moves %s to %s, which the adapter does not follow", ev.file, ev.line, ev.table, ev.to)})
	case "inherit":
		if t := f.tables[ev.table]; t != nil {
			t.unknown = ev.unknown
		}
	case "rlson", "rlsoff", "rlsforce", "rlsunforce":
		r := f.rls[ev.table]
		if r == nil {
			r = &ir.TableRLS{Table: ev.table}
			f.rls[ev.table] = r
		}
		switch ev.op {
		case "rlson":
			r.Enabled, r.File, r.Line = true, ev.file, ev.line
		case "rlsoff":
			r.Enabled, r.File, r.Line = false, ev.file, ev.line
		case "rlsforce":
			r.Forced = true
		default:
			r.Forced = false
		}
	case "view":
		f.views[ev.table] = ev
	case "dropview":
		delete(f.views, ev.table)
	}
}

// applyGrant folds a GRANT or REVOKE of table privileges.
func (f *tableFold) applyGrant(ev tgEvent, file string, line int) {
	targets := append([]string(nil), ev.tables...)
	if len(ev.schemas) > 0 {
		for t := range f.tables {
			if contains(ev.schemas, schemaOf(t)) {
				targets = append(targets, t)
			}
		}
	}
	for _, t := range targets {
		f.set(ev, t, file, line, false)
	}
}

// set applies the privileges of an event to one table. For a default
// privilege, def is true: a rule for a role that is not the creator is unknown.
func (f *tableFold) set(ev tgEvent, table, file string, line int, def bool) {
	for _, it := range ev.items {
		for _, role := range ev.roles {
			k := tgKey{table, role, it.op}
			st := f.grants[k]
			if st == nil {
				st = &tgState{cols: map[string]tgRec{}}
				f.grants[k] = st
			}
			rec := tgRec{state: ir.ExecGranted, file: file, line: line}
			if ev.verb == "revoke" {
				rec.state = ir.ExecRevoked
			}
			if def && ev.forRole {
				rec = tgRec{state: ir.ExecUnknown, file: file, line: line,
					reason: fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE at %s:%d: the role that creates the table is not known", file, line)}
			}
			switch {
			case len(it.cols) == 0:
				st.whole = &rec
				if rec.state != ir.ExecGranted {
					st.cols = map[string]tgRec{}
				}
			case st.whole == nil || st.whole.state != ir.ExecGranted:
				for _, c := range it.cols {
					st.cols[c] = rec
				}
			}
		}
	}
}

// expand replaces a write's "*" with the table's columns when they are known.
func (f *tableFold) expand(w ir.TableWrite) ir.TableWrite {
	if len(w.Columns) != 1 || w.Columns[0] != ir.WriteAll || w.Op == ir.WriteDelete {
		return w
	}
	t := f.tables[w.Table]
	if t == nil || t.unknown != "" || len(t.cols) == 0 {
		return w
	}
	w.Columns = uniqueSorted(t.cols)
	return w
}

// finalGrants is the table privileges left in force, sorted.
func (f *tableFold) finalGrants() []ir.TableGrant {
	var out []ir.TableGrant
	for k, st := range f.grants {
		if st.whole != nil {
			out = append(out, ir.TableGrant{Table: k.table, Role: k.role, Op: k.op, State: st.whole.state,
				File: st.whole.file, Line: st.whole.line, Reason: st.whole.reason})
		}
		if st.whole != nil && st.whole.state == ir.ExecGranted {
			continue
		}
		for c, rec := range st.cols {
			out = append(out, ir.TableGrant{Table: k.table, Role: k.role, Op: k.op, Columns: []string{c}, State: rec.state,
				File: rec.file, Line: rec.line, Reason: rec.reason})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Op != b.Op {
			return a.Op < b.Op
		}
		return strings.Join(a.Columns, ",") < strings.Join(b.Columns, ",")
	})
	return out
}

// viewGaps marks the tables behind each view that a write or a privilege
// reaches.
func (f *tableFold) viewGap(view, file string, line int, what string) []ir.WriterGap {
	v, ok := f.views[view]
	if !ok {
		return nil
	}
	bases := v.bases
	if len(bases) == 0 {
		bases = []string{ir.WriteAll}
	}
	var out []ir.WriterGap
	for _, b := range bases {
		out = append(out, ir.WriterGap{Table: b, File: file, Line: line,
			Reason: fmt.Sprintf("%s %s, a view at %s:%d, and the adapter does not follow a write through a view to the tables behind it", what, view, v.file, v.line)})
	}
	return out
}

type pendingGap struct {
	mod int
	an  ir.Annotation
}

// applyTableEvent folds one table or privilege event of file.
func (f *tableFold) applyTableEvent(an ir.Annotation, file string) {
	switch an.Kind {
	case tableKind:
		if ev, ok := an.Kwargs["ev"].(tableEvent); ok {
			f.apply(ev)
		}
	case tgrantKind:
		if ev, ok := an.Kwargs["ev"].(tgEvent); ok {
			f.applyGrant(ev, file, an.Line)
		}
	case tdefaultKind:
		if ev, ok := an.Kwargs["ev"].(tgEvent); ok {
			f.rules = append(f.rules, tgRule{ev: ev, file: file, line: an.Line})
		}
	case tgrantUnknownKind:
		reason, _ := an.Kwargs["reason"].(string)
		f.gaps = append(f.gaps, ir.WriterGap{Table: ir.WriteAll, Reason: reason, File: file, Line: an.Line})
	}
}

// resolveWrites finishes the writer facts once every file is folded: a
// trigger function's NEW assignments become writes on the table of each
// trigger that runs it, "*" is expanded to a table's columns, a write or a
// privilege on a view leaves the tables behind it unknown, and the gaps of a
// function that a later definition replaced are dropped with it. eff is the
// effective definition of each symbol, as (module, symbol) indexes.
// @enforces adapters.postgres:INV-7
func resolveWrites(mods []ir.Module, order []int, eff map[string][2]int, pending []pendingGap, tf *tableFold) {
	fqns := make([]string, 0, len(eff))
	for fqn := range eff {
		fqns = append(fqns, fqn)
	}
	sort.Strings(fqns)
	symOf := func(fqn string) *ir.Symbol { d := eff[fqn]; return &mods[d[0]].Symbols[d[1]] }

	triggers := map[string][]*ir.Guard{}
	for _, fqn := range fqns {
		if g := symOf(fqn).Guard; g != nil && g.Kind == ir.GuardTrigger && g.Function != "" {
			triggers[g.Function] = append(triggers[g.Function], g)
		}
	}
	gaps := append([]ir.WriterGap(nil), tf.gaps...)
	for _, fqn := range fqns {
		s := symOf(fqn)
		if len(s.Writes) == 0 {
			continue
		}
		var kept []ir.TableWrite
		seen := map[string]bool{}
		add := func(w ir.TableWrite) {
			key := fmt.Sprintf("%s|%s|%d|%s", w.Table, w.Op, w.Line, strings.Join(w.Columns, ","))
			if !seen[key] {
				seen[key] = true
				kept = append(kept, w)
			}
		}
		for _, w := range s.Writes {
			if w.Table != newTable {
				add(w)
				continue
			}
			for _, g := range triggers[fqn] {
				for _, op := range []string{ir.WriteInsert, ir.WriteUpdate} {
					if contains(g.Commands, op) {
						add(ir.TableWrite{Table: g.Table, Op: op, Columns: w.Columns, Line: w.Line})
					}
				}
			}
		}
		s.Writes = nil
		for _, w := range kept {
			w = tf.expand(w)
			s.Writes = append(s.Writes, w)
			gaps = append(gaps, tf.viewGap(w.Table, s.File, w.Line, s.FQN+" writes")...)
		}
		sort.SliceStable(s.Writes, func(i, j int) bool { return s.Writes[i].Line < s.Writes[j].Line })
	}
	for _, mi := range order {
		m := &mods[mi]
		for i, w := range m.Writes {
			m.Writes[i] = tf.expand(w)
			gaps = append(gaps, tf.viewGap(w.Table, m.File, w.Line, m.File+" writes")...)
		}
	}
	grants := tf.finalGrants()
	for _, g := range grants {
		if g.State == ir.ExecGranted {
			gaps = append(gaps, tf.viewGap(g.Table, g.File, g.Line, fmt.Sprintf("role %s is granted %s on", g.Role, g.Op))...)
		}
	}
	for _, p := range pending {
		kw := p.an.Kwargs
		owner, _ := kw["owner"].(string)
		if owner != "" {
			d, ok := eff[owner]
			line, _ := kw["owner_line"].(int)
			if !ok || d[0] != p.mod || mods[d[0]].Symbols[d[1]].Line != line {
				continue
			}
		}
		table, _ := kw["table"].(string)
		cols, _ := kw["columns"].([]string)
		reason, _ := kw["reason"].(string)
		file, _ := kw["file"].(string)
		gaps = append(gaps, ir.WriterGap{Table: table, Columns: cols, Reason: reason, File: file, Line: p.an.Line})
	}
	sort.SliceStable(gaps, func(i, j int) bool {
		a, b := gaps[i], gaps[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Reason < b.Reason
	})
	var uniq []ir.WriterGap
	for i, g := range gaps {
		if i > 0 && g.Table == gaps[i-1].Table && g.File == gaps[i-1].File && g.Line == gaps[i-1].Line && g.Reason == gaps[i-1].Reason {
			continue
		}
		uniq = append(uniq, g)
	}
	if len(order) > 0 {
		first := &mods[order[0]]
		first.TableGrants = grants
		var rls []ir.TableRLS
		for _, r := range tf.rls {
			rls = append(rls, *r)
		}
		sort.Slice(rls, func(i, j int) bool { return rls[i].Table < rls[j].Table })
		first.RLS = rls
		var tables []ir.TableColumns
		for name, t := range tf.tables {
			tables = append(tables, ir.TableColumns{Table: name, Columns: uniqueSorted(t.cols), Unknown: t.unknown})
		}
		sort.Slice(tables, func(i, j int) bool { return tables[i].Table < tables[j].Table })
		first.Tables = tables
		first.WriterGaps = uniq
	}
}

// scanDynamicRLS finds a policy or row-level security statement inside a
// statement that is not one (a DO block, a function body, an EXECUTE string):
// the table it names literally has unknown role writers.
func scanDynamicRLS(mod *ir.Module, st statement) {
	if reAlterTable.MatchString(st.text) || rePolicy.MatchString(st.text) || reAlterPolicy.MatchString(st.text) || reDropPolicy.MatchString(st.text) {
		return
	}
	tailRLS := regexp.MustCompile(`(?is)^[^;]{0,120}?\brow\s+level\s+security\b`)
	for _, loc := range reDynRLS.FindAllStringSubmatchIndex(st.text, -1) {
		table := tableOf(st.text[loc[2]:loc[3]], optGroup(st.text, loc, 2))
		if strings.HasPrefix(strings.ToLower(st.text[loc[0]:loc[1]]), "alter") && !tailRLS.MatchString(st.text[loc[1]:]) {
			continue
		}
		line := st.line + strings.Count(st.text[:loc[0]], "\n")
		gapEvent(mod, ir.WriterGap{Table: table, File: mod.File, Line: line,
			Reason: fmt.Sprintf("a policy or row-level security statement for %s inside the statement at %s:%d is not a plain statement the adapter reads, so which roles can write it is unknown", table, mod.File, line)}, "", 0)
	}
}
