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
// A row selector is not a condition read. A column compared by plain = (or IN,
// or = ANY) with a parameter of the function, with a variable whose only
// assignments are parameters, with a field of a local record variable
// (WHERE d.id = v_dev.id, v_dev declared in DECLARE), or with a hash function
// of a parameter (WHERE token_hash = encode(sha256(p_token), 'hex')), only
// picks the row the function acts on; it is not what a promise is about, and
// it is left out.
// Comparisons with a literal, another column, now() or another function, and <,
// >, >= and <>, still count.
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
	reSelectWord = regexp.MustCompile(`(?is)\bselect\b`)
	reCondWord   = regexp.MustCompile(`(?is)\b(if|elsif|elseif|when)\b`)
	reSimpleCol  = regexp.MustCompile(`(?is)^\s*((?:` + ident + `)(?:\s*\.\s*(?:` + ident + `))*)\s*(?:as\s+(?:` + ident + `))?\s*$`)
)

// colRef is a possibly qualified name in an expression.
type colRef struct {
	qual []string
	name string
	// at is the offset of the reference in the text refsIn read.
	at int
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
			start := i
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
			out = append(out, colRef{qual: parts[:len(parts)-1], name: parts[len(parts)-1], at: start})
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

// readConditionSet reads the condition reads of a function body that starts at
// file line base, and whether the body has a guard clause: an IF or ELSIF
// whose branch raises or returns before the end of the body. A read in the
// condition of such a branch carries Guard. When readOnly, the body writes
// nothing, and the WHERE of each of its SELECTs counts as that of its own
// statement. A read of NEW or OLD is on the table newTable, which Resolve
// turns into the table of each trigger that runs the function.
// @enforces adapters.postgres:INV-12, adapters.postgres:INV-14, adapters.postgres:INV-15, adapters.postgres:INV-16, adapters.postgres:INV-17
func readConditionSet(code string, base int, params map[string]bool, readOnly bool) ([]ir.CondRead, bool) {
	mask := maskCode(code)
	idVars, callVars := assignedVars(mask)
	isParam := paramVars(mask, params)
	records := declaredVars(mask)
	lines := newLineIndex(code)
	line := func(off int) int { return base + lines.before(off) }

	// An alias belongs to its statement: the same short alias may stand for
	// one table in one statement and another in the next. Statements are the
	// text between semicolons. A name that no statement of the reference's own
	// segment binds falls back to the body's other statements only when they
	// all agree on the table.
	segOf := func(off int) int { return strings.Count(mask[:off], ";") }
	segAliases := map[int]map[string]string{}
	bodyAliases := map[string]string{}
	bind := func(m map[string]string, key, table string, overwrite bool) {
		if _, ok := m[key]; overwrite || !ok {
			m[key] = table
		}
	}
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
		seg := segOf(loc[0])
		if segAliases[seg] == nil {
			segAliases[seg] = map[string]string{}
		}
		m := segAliases[seg]
		keys := []string{}
		if a := aliasAfter(mask, loc[1]); a != "" {
			bind(m, a, table, true)
			keys = append(keys, a)
		}
		bind(m, name, table, false)
		m[sch+"."+name] = table
		keys = append(keys, name, sch+"."+name)
		for _, k := range keys {
			if prev, ok := bodyAliases[k]; ok && prev != table {
				bodyAliases[k] = "" // two tables: ambiguous in the body
			} else if !ok {
				bodyAliases[k] = table
			}
		}
	}
	aliasCache := map[int]map[string]string{}
	aliasesAt := func(off int) map[string]string {
		seg := segOf(off)
		if m, ok := aliasCache[seg]; ok {
			return m
		}
		m := map[string]string{}
		for k, v := range bodyAliases {
			if v != "" {
				m[k] = v
			}
		}
		for k, v := range segAliases[seg] {
			m[k] = v
		}
		aliasCache[seg] = m
		return m
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
			t, col, _, good := resolveRef(refs[0], table, aliasesAt(loc[0]), nil)
			if good {
				vars[target] = condVar{t, col}
			}
		}
	}
	if readOnly {
		for _, loc := range reSelectWord.FindAllStringIndex(mask, -1) {
			end := stmtEnd(mask, loc[1])
			dup := false
			for _, st := range stmts {
				dup = dup || st.start == loc[0]
			}
			table := ""
			if m := reAliasTable.FindStringSubmatchIndex(mask[loc[1]:end]); m != nil {
				second := ""
				if m[4] >= 0 {
					second = mask[loc[1]+m[4] : loc[1]+m[5]]
				}
				table = tableOf(mask[loc[1]+m[2]:loc[1]+m[3]], second)
			}
			if !dup && table != "" {
				stmts = append(stmts, condStmt{loc[0], end, table})
			}
		}
	}
	sort.SliceStable(stmts, func(i, j int) bool { return stmts[i].start < stmts[j].start })
	// A record variable is a DECLAREd name that is not a table alias of the
	// body: a name that is both stays a possible column.
	isRecord := func(name string) bool {
		_, alias := bodyAliases[name]
		return records[name] && !alias
	}

	var out []ir.CondRead
	seen := map[ir.CondRead]bool{}
	add := func(cr ir.CondRead) {
		if !seen[cr] {
			seen[cr] = true
			out = append(out, cr)
		}
	}
	// emit records the reads of a predicate that starts at offset at of the
	// body; scope is the offset of its statement, which binds its aliases. Each
	// read is on the line where the reference itself is, so a predicate of a
	// sub-select on a later line is cited there.
	emit := func(text string, primary string, scope, at int, guard bool) {
		for _, ref := range refsIn(text) {
			t, col, qualified, ok := resolveRef(ref, primary, aliasesAt(scope), vars)
			if !ok {
				continue
			}
			add(ir.CondRead{Table: t, Column: col, Qualified: qualified, Line: line(at + ref.at), Guard: guard})
		}
	}
	// emitPred emits one predicate. A column compared with the caller's
	// identity is recorded as such; any other predicate as plain reads.
	emitPred := func(p predAt, primary string, scope int, guard bool) {
		pred := p.text
		if ref, via, call, callVar, ok := ownerCompare(pred, idVars, callVars); ok {
			if t, col, qualified, good := resolveRef(ref, primary, aliasesAt(scope), vars); good {
				first := p.off + len(pred) - len(strings.TrimLeft(pred, " \t\r\n"))
				add(ir.CondRead{Table: t, Column: col, Qualified: qualified, Line: line(first), Guard: guard,
					OwnerVia: via, OwnerCall: call, OwnerCallVar: callVar})
				return
			}
		}
		emit(pred, primary, scope, p.off, guard)
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
		for _, pred := range splitAndAt(mask[w+5:stop], w+5) {
			if !isSelector(pred.text, isParam, isRecord) {
				emitPred(pred, st.table, w, false)
			}
		}
	}

	// IF, ELSIF and CASE WHEN conditions.
	guarded := false
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
		guard := false
		if kw != "when" {
			guard = branchGuards(mask, then+len("then"))
			guarded = guarded || guard
		}
		for _, pred := range splitAndAt(cond, loc[1]) {
			emitPred(pred, primary, loc[0], guard)
		}
	}
	return out, guarded
}

var (
	reIdentityExpr = regexp.MustCompile(`(?i)^(?:auth\s*\.\s*(?:uid|jwt)\s*\(\s*\)|current_user|session_user)$`)
	reNoArgCall    = regexp.MustCompile(`(?is)^(?:(` + ident + `)\s*\.\s*)?(` + ident + `)\s*\(\s*\)$`)
	reDistinct     = regexp.MustCompile(`(?is)\s+is\s+(?:not\s+)?distinct\s+from\s+`)
	reNoFromInto   = regexp.MustCompile(`(?is)\bselect\s+([^;]*?)\s+into\s+(?:strict\s+)?(` + ident + `)\s*;`)
	reRaiseNoise   = map[string]bool{"notice": true, "warning": true, "info": true, "log": true, "debug": true}
	reOnlyEnd      = regexp.MustCompile(`(?is)^(?:end\b[\s\w"]*;?)?\s*$`)
)

// stripWrappers removes the casts, outer parentheses and a leading SELECT around
// an expression: (select auth.uid())::uuid is auth.uid().
func stripWrappers(e string) string {
	for {
		before := e
		e = strings.TrimSpace(reCondCastTail.ReplaceAllString(strings.TrimSpace(e), ""))
		if strings.HasPrefix(e, "(") && matchParen(e, 0) == len(e)-1 {
			e = strings.TrimSpace(e[1 : len(e)-1])
		}
		if w, n := firstWord(e); w == "select" {
			e = strings.TrimSpace(e[n:])
		}
		if e == before {
			return e
		}
	}
}

// identityExpr says whether e, as a whole, is a read of the caller's identity:
// auth.uid(), auth.jwt(), current_user or session_user, with optional casts,
// parentheses and a leading SELECT. It returns the read as the adapter records
// one elsewhere, lower case with no spaces.
func identityExpr(e string) (string, bool) {
	e = stripWrappers(e)
	if !reIdentityExpr.MatchString(e) {
		return "", false
	}
	return strings.Join(strings.Fields(strings.ToLower(e)), ""), true
}

// identityVars lists the variables whose every assignment is a read of the
// caller's identity, with that read: v_uid := auth.uid(), a DECLARE default, or
// SELECT auth.uid() INTO v_uid. A variable any other statement loads, or that is
// ever assigned something else, is not one.
func identityVars(mask string) map[string]string {
	via, _ := assignedVars(mask)
	return via
}

// assignedVars is identityVars with the variables whose every assignment is a
// call of one and the same function with no arguments, with that function's
// FQN: v_family := public.current_family(). Whether such a call reads the
// caller's identity is settled in Resolve. A variable assigned both an identity
// read and a call, two different calls, or anything else, is in neither map.
func assignedVars(mask string) (map[string]string, map[string]string) {
	bad := map[string]bool{}
	for _, m := range reIntoVars.FindAllStringSubmatch(mask, -1) {
		for _, t := range splitTop(m[1]) {
			if n, ok := firstIdent(strings.TrimSpace(t)); ok {
				bad[n] = true
			}
		}
	}
	via, calls := map[string]string{}, map[string]string{}
	assign := func(name, rhs string) {
		name = unquote(name)
		if v, ok := identityExpr(rhs); ok {
			if _, seen := via[name]; !seen {
				via[name] = v
			}
			return
		}
		if fqn, ok := noArgCall(stripWrappers(rhs)); ok {
			if prev, seen := calls[name]; seen && prev != fqn {
				bad[name] = true
			}
			calls[name] = fqn
			return
		}
		bad[name] = true
	}
	for _, m := range reAssign.FindAllStringSubmatch(mask, -1) {
		assign(m[1], m[2])
	}
	for _, m := range reDeclare.FindAllStringSubmatch(mask, -1) {
		assign(m[1], m[2])
	}
	for _, m := range reNoFromInto.FindAllStringSubmatch(mask, -1) {
		if !strings.Contains(strings.ToLower(m[1]), " from ") {
			assign(m[2], m[1])
		}
	}
	for n := range via {
		if _, both := calls[n]; both {
			bad[n] = true
		}
	}
	for n := range bad {
		delete(via, n)
		delete(calls, n)
	}
	return via, calls
}

// noArgCall reads e, as a whole, as a call with no arguments and returns the
// FQN of the function it calls.
func noArgCall(e string) (string, bool) {
	m := reNoArgCall.FindStringSubmatch(e)
	if m == nil {
		return "", false
	}
	first, second := m[1], m[2]
	s, name := qualify(second, "")
	if first != "" {
		s, name = qualify(first, second)
	}
	return functionFQN(s, name), true
}

// splitCompare splits a predicate at its first comparison outside parentheses:
// =, <>, !=, IS DISTINCT FROM or IS NOT DISTINCT FROM.
func splitCompare(pred string) (lhs, rhs string, ok bool) {
	depth := 0
	for i := 0; i < len(pred); i++ {
		switch pred[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '=':
			if depth == 0 && (i == 0 || !strings.ContainsRune("<>!:=", rune(pred[i-1]))) && (i+1 >= len(pred) || pred[i+1] != '>' && pred[i+1] != '=') {
				return pred[:i], pred[i+1:], true
			}
		case '<', '!':
			if depth == 0 && i+1 < len(pred) && (pred[i] == '<' && pred[i+1] == '>' || pred[i] == '!' && pred[i+1] == '=') {
				return pred[:i], pred[i+2:], true
			}
		case ' ', '\t', '\n', '\r':
			if depth == 0 {
				if loc := reDistinct.FindStringIndex(pred[i:]); loc != nil && loc[0] == 0 {
					return pred[:i], pred[i+loc[1]:], true
				}
			}
		}
	}
	return "", "", false
}

// ownerCompare reads a predicate that compares one column with the caller's
// identity, either side first: the column, and what it is compared with. That
// is a direct identity read (auth.uid(), also in (select auth.uid())), a
// variable assigned only from one, or a function called with no arguments, for
// Resolve to settle (call is its FQN). Anything else is not one.
func ownerCompare(pred string, idVars, callVars map[string]string) (ref colRef, via, call, callVar string, ok bool) {
	l, r, found := splitCompare(pred)
	if !found {
		return colRef{}, "", "", "", false
	}
	for _, sides := range [][2]string{{l, r}, {r, l}} {
		col, other := strings.TrimSpace(sides[0]), strings.TrimSpace(sides[1])
		col = strings.TrimSpace(reCondCastTail.ReplaceAllString(col, ""))
		if !(reIdentAt.FindString(col) == col || reFieldChain.MatchString(col)) {
			continue
		}
		refs := refsIn(col)
		if len(refs) != 1 {
			continue
		}
		if _, isVar := idVars[refs[0].name]; isVar && len(refs[0].qual) == 0 {
			continue
		}
		if _, isVar := callVars[refs[0].name]; isVar && len(refs[0].qual) == 0 {
			continue
		}
		if v, yes := identityExpr(other); yes {
			return refs[0], v, "", "", true
		}
		o := stripWrappers(other)
		if reIdentAt.FindString(o) == o {
			if v, yes := idVars[unquote(o)]; yes {
				return refs[0], unquote(o) + ", assigned only from " + v, "", "", true
			}
			if fqn, yes := callVars[unquote(o)]; yes {
				return refs[0], "", fqn, unquote(o), true
			}
		}
		if fqn, yes := noArgCall(o); yes {
			return refs[0], "", fqn, "", true
		}
	}
	return colRef{}, "", "", "", false
}

// branchGuards reads the branch of the IF or ELSIF whose THEN ends at from, up
// to the next ELSIF, ELSE or END IF at its own level, and says whether it is a
// guard: a statement of the branch itself raises (RAISE EXCEPTION, or RAISE with
// no level) or returns (RETURN, RETURN NULL, RETURN QUERY; not RETURN NEXT),
// and, for a return, more of the body follows the whole IF. A raise or a return
// inside a nested IF is conditional and does not make the branch a guard.
func branchGuards(mask string, from int) bool {
	depth := 0
	raises, returns := false, false
	for i := from; i < len(mask); {
		c := mask[i]
		if c == '"' {
			ln := 0
			i = skipToken(mask, i, &ln)
			continue
		}
		if !isWordByte(c) || i > 0 && isWordByte(mask[i-1]) {
			i++
			continue
		}
		j := i
		for j < len(mask) && isWordByte(mask[j]) {
			j++
		}
		w := strings.ToLower(mask[i:j])
		next, _ := firstWord(mask[skipWS(mask, j):])
		switch w {
		case "if":
			if statementStart[prevWord(mask, i)] {
				depth++
			}
		case "end":
			if next == "if" {
				if depth == 0 {
					if raises {
						return true
					}
					if !returns {
						return false
					}
					rest := mask[skipWS(mask, j):]
					if semi := strings.IndexByte(rest, ';'); semi >= 0 {
						return !reOnlyEnd.MatchString(strings.TrimSpace(rest[semi+1:]))
					}
					return false
				}
				depth--
			}
		case "elsif", "elseif", "else":
			if depth == 0 {
				if raises {
					return true
				}
				// The branch ends here; whether more of the body follows
				// the whole IF is read at its END IF.
				if !returns {
					return false
				}
				return returnsBeforeEnd(mask, j)
			}
		case "raise":
			if depth == 0 && !reRaiseNoise[next] {
				raises = true
			}
		case "return":
			if depth == 0 && next != "next" {
				returns = true
			}
		}
		i = j
	}
	return raises
}

// returnsBeforeEnd says whether more of the body follows the END IF that closes
// the IF the scan is inside, which starts at from.
func returnsBeforeEnd(mask string, from int) bool {
	depth := 0
	for i := from; i < len(mask); {
		c := mask[i]
		if !isWordByte(c) || i > 0 && isWordByte(mask[i-1]) {
			i++
			continue
		}
		j := i
		for j < len(mask) && isWordByte(mask[j]) {
			j++
		}
		w := strings.ToLower(mask[i:j])
		next, _ := firstWord(mask[skipWS(mask, j):])
		if w == "if" && statementStart[prevWord(mask, i)] {
			depth++
		}
		if w == "end" && next == "if" {
			if depth == 0 {
				rest := mask[skipWS(mask, j):]
				if semi := strings.IndexByte(rest, ';'); semi >= 0 {
					return !reOnlyEnd.MatchString(strings.TrimSpace(rest[semi+1:]))
				}
				return false
			}
			depth--
		}
		i = j
	}
	return false
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

var (
	reAssign       = regexp.MustCompile(`(?is)(?:^|[;\s(])(` + ident + `)\s*:=\s*([^;]*);`)
	reDeclare      = regexp.MustCompile(`(?is)(?:\bdeclare|;)\s*(` + ident + `)\s+[A-Za-z_][A-Za-z0-9_.\[\]]*(?:\s*\(\s*\d+(?:\s*,\s*\d+)?\s*\))?\s*(?::=|=|\bdefault\b)\s*([^;]*);`)
	reIntoVars     = regexp.MustCompile(`(?is)\binto\s+(?:strict\s+)?([^;]*?)\s+from\b`)
	reSelector     = regexp.MustCompile(`(?is)^\s*((?:` + ident + `)(?:\s*\.\s*(?:` + ident + `))*)\s*(=|\bin\b)\s*(.+?)\s*$`)
	reParamEq      = regexp.MustCompile(`(?is)^\s*(.+?)\s*=\s*((?:` + ident + `)(?:\s*\.\s*(?:` + ident + `))*)\s*$`)
	reCondCastTail = regexp.MustCompile(`(?is)\s*::\s*[A-Za-z_][A-Za-z0-9_ .]*(?:\[\])?\s*$`)
)

// condParamNames reads the parameter names of the CREATE FUNCTION statement in
// text: the first word of each entry of the list after the name, past an IN,
// OUT, INOUT or VARIADIC mode.
func condParamNames(text string) map[string]bool {
	m := reFunction.FindStringIndex(text)
	if m == nil {
		return nil
	}
	open := m[1] - 1
	closeAt := matchParen(maskCode(text), open)
	if closeAt < 0 {
		return nil
	}
	out := map[string]bool{}
	for _, item := range splitTop(text[open+1 : closeAt]) {
		words := strings.Fields(item)
		for len(words) > 0 {
			switch strings.ToLower(words[0]) {
			case "in", "out", "inout", "variadic":
				words = words[1:]
				continue
			}
			break
		}
		if len(words) >= 2 {
			if n, ok := firstIdent(words[0]); ok {
				out[n] = true
			}
		}
	}
	return out
}

// paramVars returns a test for the names that stand for a parameter: the
// parameters, $n, and a variable whose every assignment (:=) is a parameter and
// that no SELECT ... INTO loads.
func paramVars(mask string, params map[string]bool) func(string) bool {
	vars := map[string]bool{}
	bad := map[string]bool{}
	for _, m := range reIntoVars.FindAllStringSubmatch(mask, -1) {
		for _, t := range splitTop(m[1]) {
			if n, ok := firstIdent(strings.TrimSpace(t)); ok {
				bad[n] = true
			}
		}
	}
	type assign struct{ name, rhs string }
	var as []assign
	for _, m := range reAssign.FindAllStringSubmatch(mask, -1) {
		as = append(as, assign{unquote(m[1]), strings.TrimSpace(reCondCastTail.ReplaceAllString(strings.TrimSpace(m[2]), ""))})
	}
	for _, m := range reDeclare.FindAllStringSubmatch(mask, -1) {
		as = append(as, assign{unquote(m[1]), strings.TrimSpace(reCondCastTail.ReplaceAllString(strings.TrimSpace(m[2]), ""))})
	}
	for _, a := range as {
		if _, seen := vars[a.name]; !seen {
			vars[a.name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, a := range as {
			if !vars[a.name] {
				continue
			}
			rhs := unquote(a.rhs)
			if bad[a.name] || !(params[rhs] || vars[rhs] && rhs != a.name && !bad[rhs]) || !reIdentAt.MatchString(a.rhs) || reIdentAt.FindString(a.rhs) != a.rhs {
				vars[a.name] = false
				changed = true
			}
		}
	}
	return func(n string) bool {
		if strings.HasPrefix(n, "$") {
			return true
		}
		return params[n] || vars[n] && !bad[n]
	}
}

// predAt is a predicate and its offset in the body.
type predAt struct {
	text string
	off  int
}

// splitAndAt splits a condition at its AND outside parentheses, with each
// predicate's offset, for s starting at offset base of the body.
func splitAndAt(s string, base int) []predAt {
	var out []predAt
	from := 0
	for {
		i := findWord(s, from, len(s), "and")
		if i < 0 {
			break
		}
		out = append(out, predAt{s[from:i], base + from})
		from = i + 3
	}
	return append(out, predAt{s[from:], base + from})
}

// isParamExpr says whether e is one value that picks a row without being a
// column of it: a parameter (or a parameter variable), with an optional cast; a
// field of a local record variable (v_dev.id); a hash function of a parameter
// (the recogniser hashAuth uses); or a parenthesised list or ANY(...) of them.
func isParamExpr(e string, isParam, isRecord func(string) bool) bool {
	e = strings.TrimSpace(reCondCastTail.ReplaceAllString(strings.TrimSpace(e), ""))
	if w, n := firstWord(e); w == "any" || w == "some" || w == "all" {
		e = strings.TrimSpace(e[n:])
	}
	if strings.HasPrefix(e, "(") && matchParen(e, 0) == len(e)-1 {
		items := splitTop(e[1 : len(e)-1])
		for _, it := range items {
			if !isParamExpr(it, isParam, isRecord) {
				return false
			}
		}
		return len(items) > 0
	}
	if strings.HasPrefix(e, "$") {
		return true
	}
	if m := reIdentAt.FindString(e); m != "" && m == e {
		return isParam(unquote(e))
	}
	if reFieldChain.MatchString(e) {
		q, _ := firstIdent(e)
		return isRecord(q)
	}
	return isHashOfParam(e, isParam)
}

// reFieldChain is a dotted chain of at least two identifiers.
var reFieldChain = regexp.MustCompile(`(?is)^(?:` + ident + `)(?:\s*\.\s*(?:` + ident + `))+$`)

// isSelector says whether a predicate only picks a row: <column> = <value>,
// <column> IN (<values>), <column> = ANY(<values>), or the same with the sides
// swapped, where a value is a parameter, a field of a local record variable or
// a hash of a parameter.
// @enforces adapters.postgres:INV-13
func isSelector(pred string, isParam, isRecord func(string) bool) bool {
	if m := reSelector.FindStringSubmatch(pred); m != nil && !isParam(strings.TrimSpace(m[1])) {
		if isParamExpr(m[3], isParam, isRecord) {
			return true
		}
	}
	if m := reParamEq.FindStringSubmatch(pred); m != nil && isParamExpr(m[1], isParam, isRecord) && !isParam(strings.TrimSpace(m[2])) {
		return true
	}
	return false
}

var reDeclareBlock = regexp.MustCompile(`(?is)\bdeclare\b(.*?)\bbegin\b`)

// declaredVars lists the names a body declares in its DECLARE sections, of any
// type. A name the loop variable of a FOR introduces is not declared there.
func declaredVars(mask string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reDeclareBlock.FindAllStringSubmatch(mask, -1) {
		for _, entry := range strings.Split(m[1], ";") {
			if n, ok := firstIdent(entry); ok {
				out[n] = true
			}
		}
	}
	return out
}
