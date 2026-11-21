package postgres

// @module-feature adapters.postgres

import (
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Condition reads. A promise about a column is decided where the column is
// tested, not where it is merely assigned: a function that sets status and
// updated_at while checking owner_id is about owner_id. The adapter records,
// for each SQL function, the columns its body reads in a condition
// (ir.Symbol.CondReads):
//
//   - the condition of an IF or ELSIF, and of a CASE WHEN;
//   - the WHERE of its own UPDATE, DELETE or SELECT ... INTO;
//   - a column loaded by a simple "SELECT col INTO v ... FROM t" and then
//     tested as v in an IF: no data flow beyond that, in one body;
//   - for a trigger function, NEW.col and OLD.col, as a read on the table of
//     each trigger that runs it;
//   - for a named CHECK, the columns of its expression.
//
// It records only what it parsed. Which names are columns is decided where the
// table's columns are known: a bare name counts only if its table has a column
// of that name.

// condSkip are words that are never a column in a condition.
var condSkip = map[string]bool{"and": true, "or": true, "not": true, "is": true, "null": true, "true": true, "false": true,
	"in": true, "between": true, "like": true, "ilike": true, "similar": true, "to": true, "exists": true, "select": true,
	"from": true, "where": true, "case": true, "when": true, "then": true, "else": true, "end": true, "any": true, "all": true,
	"some": true, "distinct": true, "interval": true, "array": true, "as": true, "at": true, "time": true, "zone": true,
	"current_date": true, "current_time": true, "current_timestamp": true, "localtimestamp": true, "localtime": true,
	"current_user": true, "session_user": true, "unknown": true, "escape": true, "isnull": true, "notnull": true,
	"using": true, "on": true, "join": true, "left": true, "right": true, "inner": true, "outer": true, "cross": true,
	"full": true, "natural": true, "limit": true, "order": true, "by": true, "group": true, "having": true, "for": true,
	"update": true, "strict": true, "into": true, "row": true, "only": true, "with": true, "over": true, "values": true,
	"union": true, "except": true, "intersect": true, "asc": true, "desc": true, "nulls": true, "first": true, "last": true,
	"if": true, "elsif": true, "elseif": true, "matched": true, "others": true}

// aliasStop are the words that can follow a table name and are not its alias.
var aliasStop = map[string]bool{"where": true, "set": true, "on": true, "using": true, "join": true, "left": true,
	"right": true, "inner": true, "outer": true, "cross": true, "full": true, "natural": true, "for": true, "order": true,
	"limit": true, "group": true, "having": true, "returning": true, "values": true, "select": true, "as": true,
	"union": true, "except": true, "intersect": true, "window": true, "offset": true, "fetch": true, "when": true,
	"then": true, "and": true, "or": true, "into": true, "default": true, "overriding": true, "tablesample": true,
	"lateral": true, "with": true, "end": true, "loop": true, "begin": true, "if": true, "else": true, "elsif": true,
	"return": true, "perform": true, "raise": true, "insert": true, "update": true, "delete": true, "merge": true}

var (
	reAliasTable = regexp.MustCompile(`(?is)\b(?:from|join|update|insert\s+into|merge\s+into|using)\s+(?:only\s+)?` + qname)
	reSelectInto = regexp.MustCompile(`(?is)\bselect\s+([^;]*?)\s+into\s+(?:strict\s+)?([^;]*?)\s+from\s+(?:only\s+)?` + qname)
	reCondWord   = regexp.MustCompile(`(?is)\b(if|elsif|elseif|when)\b`)
	reSimpleCol  = regexp.MustCompile(`(?is)^\s*((?:` + ident + `)(?:\s*\.\s*(?:` + ident + `))*)\s*(?:as\s+(?:` + ident + `))?\s*$`)
)

// colRef is a possibly qualified name in an expression.
type colRef struct {
	qual []string
	name string
}

// refsIn lists the names in an expression that may be columns: a word or a
// dotted chain that is not a function call, a cast type, a keyword, a number
// or a $n parameter. text must be masked (strings and comments blanked).
func refsIn(text string) []colRef {
	var out []colRef
	afterCast := false
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == ':' && i+1 < len(text) && text[i+1] == ':':
			afterCast = true
			i += 2
		case c == '$':
			i++
			for i < len(text) && text[i] >= '0' && text[i] <= '9' {
				i++
			}
		case c >= '0' && c <= '9':
			for i < len(text) && (isWordByte(text[i]) || text[i] == '.') {
				i++
			}
		case c == '"' || isWordByte(c):
			var parts []string
			for {
				m := reIdentAt.FindString(text[i:])
				if m == "" {
					break
				}
				parts = append(parts, unquote(m))
				i += len(m)
				j := skipWS(text, i)
				if j < len(text) && text[j] == '.' {
					k := skipWS(text, j+1)
					if reIdentAt.FindString(text[k:]) != "" {
						i = k
						continue
					}
				}
				break
			}
			if len(parts) == 0 {
				i++
				continue
			}
			next := skipWS(text, i)
			call := next < len(text) && text[next] == '('
			cast := afterCast
			afterCast = false
			if call || cast || len(parts) == 1 && condSkip[parts[0]] {
				continue
			}
			out = append(out, colRef{qual: parts[:len(parts)-1], name: parts[len(parts)-1]})
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				afterCast = false
			}
			i++
		}
	}
	return out
}

// findWord returns the offset of the first word in words at parenthesis depth
// 0 within s[from:end], or -1. A ")" that closes a group opened before from
// ends the search.
func findWord(s string, from, end int, words ...string) int {
	depth := 0
	for i := from; i < end; {
		c := s[i]
		switch {
		case c == '(':
			depth++
			i++
		case c == ')':
			depth--
			if depth < 0 {
				return -1
			}
			i++
		case c == '"':
			ln := 0
			i = skipToken(s, i, &ln)
		case isWordByte(c) && (i == 0 || !isWordByte(s[i-1])):
			j := i
			for j < end && isWordByte(s[j]) {
				j++
			}
			if depth == 0 {
				w := strings.ToLower(s[i:j])
				for _, x := range words {
					if w == x {
						return i
					}
				}
			}
			i = j
		default:
			i++
		}
	}
	return -1
}

// aliasAfter reads the alias that follows a table name ending at pos.
func aliasAfter(mask string, pos int) string {
	j := skipWS(mask, pos)
	if j == pos {
		return ""
	}
	if w, n := firstWord(mask[j:]); w == "as" {
		j = skipWS(mask, j+n)
	}
	m := reIdentAt.FindString(mask[j:])
	if m == "" || aliasStop[strings.ToLower(m)] {
		return ""
	}
	return unquote(m)
}

type condStmt struct {
	start, end int
	table      string
}

type condVar struct{ table, column string }

// readConditions reads the condition reads of a function body that starts at
// file line base. A read of NEW or OLD is on the table newTable, which Resolve
// turns into the table of each trigger that runs the function.
// @enforces adapters.postgres:INV-9
func readConditions(code string, base int) []ir.CondRead {
	mask := maskCode(code)
	lines := newLineIndex(code)
	line := func(off int) int { return base + lines.before(off) }

	aliases := map[string]string{}
	for _, loc := range reAliasTable.FindAllStringSubmatchIndex(mask, -1) {
		first, second := mask[loc[2]:loc[3]], ""
		if loc[4] >= 0 {
			second = mask[loc[4]:loc[5]]
		}
		if second == "" && aliasStop[strings.ToLower(first)] {
			continue
		}
		table := tableOf(first, second)
		sch, name := qualify(first, second)
		if a := aliasAfter(mask, loc[1]); a != "" {
			aliases[a] = table
		}
		if _, ok := aliases[name]; !ok {
			aliases[name] = table
		}
		aliases[sch+"."+name] = table
	}

	var stmts []condStmt
	for _, loc := range reUpdateSet.FindAllStringSubmatchIndex(mask, -1) {
		first, second := mask[loc[2]:loc[3]], ""
		if loc[4] >= 0 {
			second = mask[loc[4]:loc[5]]
		}
		stmts = append(stmts, condStmt{loc[0], stmtEnd(mask, loc[1]), tableOf(first, second)})
	}
	for _, loc := range reDeleteFrom.FindAllStringSubmatchIndex(mask, -1) {
		first, second := mask[loc[2]:loc[3]], ""
		if loc[4] >= 0 {
			second = mask[loc[4]:loc[5]]
		}
		stmts = append(stmts, condStmt{loc[0], stmtEnd(mask, loc[1]), tableOf(first, second)})
	}
	vars := map[string]condVar{}
	for _, loc := range reSelectInto.FindAllStringSubmatchIndex(mask, -1) {
		first, second := mask[loc[6]:loc[7]], ""
		if loc[8] >= 0 {
			second = mask[loc[8]:loc[9]]
		}
		table := tableOf(first, second)
		stmts = append(stmts, condStmt{loc[0], stmtEnd(mask, loc[1]), table})
		items := splitTop(mask[loc[2]:loc[3]])
		var targets []string
		for _, t := range splitTop(mask[loc[4]:loc[5]]) {
			t = strings.TrimSpace(t)
			if w, n := firstWord(t); w == "strict" {
				t = strings.TrimSpace(t[n:])
			}
			targets = append(targets, t)
		}
		if len(items) != len(targets) {
			continue
		}
		for i, it := range items {
			m := reSimpleCol.FindStringSubmatch(it)
			target, ok := firstIdent(targets[i])
			if m == nil || !ok {
				continue
			}
			refs := refsIn(m[1])
			if len(refs) != 1 {
				continue
			}
			t, col, _, good := resolveRef(refs[0], table, aliases, nil)
			if good {
				vars[target] = condVar{t, col}
			}
		}
	}
	sort.SliceStable(stmts, func(i, j int) bool { return stmts[i].start < stmts[j].start })

	var out []ir.CondRead
	seen := map[ir.CondRead]bool{}
	emit := func(text string, primary string, off int) {
		for _, ref := range refsIn(text) {
			t, col, qualified, ok := resolveRef(ref, primary, aliases, vars)
			if !ok {
				continue
			}
			cr := ir.CondRead{Table: t, Column: col, Qualified: qualified, Line: line(off)}
			if !seen[cr] {
				seen[cr] = true
				out = append(out, cr)
			}
		}
	}

	// The WHERE of the function's own UPDATE, DELETE and SELECT ... INTO.
	for _, st := range stmts {
		w := findWord(mask, st.start, st.end, "where")
		if w < 0 {
			continue
		}
		stop := findWord(mask, w+5, st.end, "returning", "order", "limit", "for", "group", "having", "union", "except", "intersect", "window")
		if stop < 0 {
			stop = st.end
		}
		emit(mask[w+5:stop], st.table, w)
	}

	// IF, ELSIF and CASE WHEN conditions.
	for _, loc := range reCondWord.FindAllStringIndex(mask, -1) {
		kw := strings.ToLower(mask[loc[0]:loc[1]])
		if kw != "when" && !statementStart[prevWord(mask, loc[0])] {
			continue
		}
		end := len(mask)
		if semi := strings.IndexByte(mask[loc[1]:], ';'); semi >= 0 {
			end = loc[1] + semi
		}
		then := findWord(mask, loc[1], end, "then")
		if then < 0 {
			continue
		}
		cond := mask[loc[1]:then]
		primary := ""
		if kw == "when" {
			for _, st := range stmts {
				if st.start <= loc[0] && loc[0] < st.end {
					primary = st.table
				}
			}
		}
		if primary == "" {
			if m := reAliasTable.FindStringSubmatchIndex(cond); m != nil {
				second := ""
				if m[4] >= 0 {
					second = cond[m[4]:m[5]]
				}
				primary = tableOf(cond[m[2]:m[3]], second)
			}
		}
		emit(cond, primary, loc[0])
	}
	return out
}

// resolveRef says which table and column a name in a condition stands for.
// qualified is true when the text named the table.
func resolveRef(ref colRef, primary string, aliases map[string]string, vars map[string]condVar) (table, column string, qualified, ok bool) {
	switch len(ref.qual) {
	case 0:
		if v, found := vars[ref.name]; found {
			return v.table, v.column, true, true
		}
		if primary != "" {
			return primary, ref.name, false, true
		}
		return "", "", false, false
	default:
		prefix := strings.Join(ref.qual, ".")
		if prefix == "new" || prefix == "old" {
			return newTable, ref.name, true, true
		}
		if t, found := aliases[prefix]; found {
			return t, ref.name, true, true
		}
	}
	return "", "", false, false
}

// checkReads are the columns of a CHECK expression, as reads of its table.
func checkReads(expr string, table string, line int) []ir.CondRead {
	var out []ir.CondRead
	seen := map[string]bool{}
	for _, ref := range refsIn(maskCode(expr)) {
		if len(ref.qual) > 0 || seen[ref.name] {
			continue
		}
		seen[ref.name] = true
		out = append(out, ir.CondRead{Table: table, Column: ref.name, Line: line})
	}
	return out
}
