package breaktest

// @module-feature breaktest

import (
	"bytes"
	"errors"
	"strings"
)

// The PostgreSQL adapter's scanner is not reused here: it rebuilds each
// statement's text (comments become spaces, leading comments are dropped), so
// its output has no byte offsets to insert a break at. sqlLex below
// tokenises the source in place with the same quoting rules.

// sqlKind is what one SQL token is.
type sqlKind int

const (
	sqlWord   sqlKind = iota // a keyword, an unquoted identifier or a number
	sqlQuoted                // a "quoted identifier"
	sqlString                // a 'string', E'string' or B'...'/X'...' literal
	sqlDollar                // a $tag$ ... $tag$ dollar-quoted string
	sqlPunct                 // one byte of punctuation or an operator
)

// sqlTok is one token, by byte offsets into the source. Comments and
// whitespace are not tokens.
type sqlTok struct {
	kind       sqlKind
	start, end int
	line       int // 1-based line of the token's first byte
}

func sqlWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// sqlDollarTag returns the length of the "$tag$" (or "$$") opening at
// src[i], or 0 when there is none ($1 is a parameter, not a tag).
func sqlDollarTag(src []byte, i int) int {
	if i >= len(src) || src[i] != '$' {
		return 0
	}
	for j := i + 1; j < len(src); j++ {
		c := src[j]
		if c == '$' {
			return j - i + 1
		}
		if !sqlWordByte(c) || j == i+1 && c >= '0' && c <= '9' {
			return 0
		}
	}
	return 0
}

// sqlLex tokenises src[from:to], whose first byte is on line. It
// understands line and nested block comments, strings with ” (and, for
// E'...', backslash) escapes, quoted identifiers and dollar quoting. Anything
// left open is an error, because offsets past it cannot be trusted.
func sqlLex(src []byte, from, to, line int) ([]sqlTok, error) {
	var out []sqlTok
	i := from
	// closeQuote returns the end of a quoted run opened at i by q, where qq
	// escapes q, and a backslash escapes the next byte when backslash is set.
	closeQuote := func(i int, q byte, backslash bool) int {
		for j := i + 1; j < to; j++ {
			switch {
			case backslash && src[j] == '\\':
				j++
			case src[j] == q && j+1 < to && src[j+1] == q:
				j++
			case src[j] == q:
				return j + 1
			}
		}
		return -1
	}
	for i < to {
		c := src[i]
		start, startLine := i, line
		kind := sqlPunct
		switch {
		case c == '\n':
			line++
			i++
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
			continue
		case c == '-' && i+1 < to && src[i+1] == '-':
			j := bytes.IndexByte(src[i:to], '\n')
			if j < 0 {
				j = to - i
			}
			i += j
			continue
		case c == '/' && i+1 < to && src[i+1] == '*':
			j, depth := i+2, 1
			for j < to && depth > 0 {
				switch {
				case src[j] == '/' && j+1 < to && src[j+1] == '*':
					depth, j = depth+1, j+2
				case src[j] == '*' && j+1 < to && src[j+1] == '/':
					depth, j = depth-1, j+2
				default:
					j++
				}
			}
			if depth > 0 {
				return nil, errors.New("unterminated block comment")
			}
			line += bytes.Count(src[i:j], []byte("\n"))
			i = j
			continue
		case (c == 'E' || c == 'e') && i+1 < to && src[i+1] == '\'':
			j := closeQuote(i+1, '\'', true)
			if j < 0 {
				return nil, errors.New("unterminated string")
			}
			kind, i = sqlString, j
		case c == '\'':
			j := closeQuote(i, '\'', false)
			if j < 0 {
				return nil, errors.New("unterminated string")
			}
			kind, i = sqlString, j
		case c == '"':
			j := closeQuote(i, '"', false)
			if j < 0 {
				return nil, errors.New("unterminated quoted identifier")
			}
			kind, i = sqlQuoted, j
		case c == '$' && sqlDollarTag(src[:to], i) > 0:
			n := sqlDollarTag(src[:to], i)
			end := bytes.Index(src[i+n:to], src[i:i+n])
			if end < 0 {
				return nil, errors.New("unterminated dollar-quoted string " + string(src[i:i+n]))
			}
			kind, i = sqlDollar, i+n+end+n
		case sqlWordByte(c):
			j := i + 1
			for j < to && (sqlWordByte(src[j]) || src[j] == '$') {
				j++
			}
			kind, i = sqlWord, j
		default:
			i++
		}
		line += bytes.Count(src[start:i], []byte("\n"))
		out = append(out, sqlTok{kind: kind, start: start, end: i, line: startLine})
	}
	return out, nil
}

// sqlStmt is one statement's tokens, up to its terminating semicolon.
type sqlStmt struct {
	src  []byte
	toks []sqlTok
}

func sqlStatements(src []byte, toks []sqlTok) []sqlStmt {
	var out []sqlStmt
	begin := 0
	for i, t := range toks {
		if t.kind == sqlPunct && src[t.start] == ';' {
			if i > begin {
				out = append(out, sqlStmt{src, toks[begin:i]})
			}
			begin = i + 1
		}
	}
	if begin < len(toks) {
		out = append(out, sqlStmt{src, toks[begin:]})
	}
	return out
}

// is reports whether token i is the keyword w (case-insensitive).
func (s sqlStmt) is(i int, w string) bool {
	if i < 0 || i >= len(s.toks) || s.toks[i].kind != sqlWord {
		return false
	}
	return strings.EqualFold(string(s.src[s.toks[i].start:s.toks[i].end]), w)
}

// punct reports whether token i is the punctuation byte p.
func (s sqlStmt) punct(i int, p byte) bool {
	return i >= 0 && i < len(s.toks) && s.toks[i].kind == sqlPunct && s.src[s.toks[i].start] == p
}

// ident is token i as an identifier, folded the way PostgreSQL folds it:
// quoted ones keep their case, unquoted ones are lower-cased.
func (s sqlStmt) ident(i int) (string, bool) {
	if i < 0 || i >= len(s.toks) {
		return "", false
	}
	t := s.toks[i]
	raw := string(s.src[t.start:t.end])
	switch t.kind {
	case sqlWord:
		return strings.ToLower(raw), true
	case sqlQuoted:
		return strings.ReplaceAll(raw[1:len(raw)-1], `""`, `"`), true
	}
	return "", false
}

// qname reads a name that may be schema-qualified at token i, giving schema
// and name (schema public when unqualified) and the index after it.
func (s sqlStmt) qname(i int) (string, string, int, bool) {
	first, ok := s.ident(i)
	if !ok {
		return "", "", i, false
	}
	if s.punct(i+1, '.') {
		second, ok := s.ident(i + 2)
		if !ok {
			return "", "", i, false
		}
		return first, second, i + 3, true
	}
	return "public", first, i + 1, true
}

// matchParen returns the index of the ")" that closes the "(" at token i, or
// -1. Strings, quoted identifiers and comments are already single tokens or
// gone, so a ")" inside them is never counted.
func (s sqlStmt) matchParen(i int) int {
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

// topLevel calls visit for each token from i on that is outside any
// parentheses, stopping when visit returns false. It reports false when the
// parentheses do not balance.
func (s sqlStmt) topLevel(i int, visit func(j int) bool) bool {
	for j := i; j < len(s.toks); j++ {
		if s.punct(j, '(') {
			k := s.matchParen(j)
			if k < 0 {
				return false
			}
			j = k
			continue
		}
		if s.punct(j, ')') {
			return false
		}
		if !visit(j) {
			return true
		}
	}
	return true
}

// sqlEnforcer is the kind of a CREATE statement the adapter makes a symbol of.
type sqlEnforcer int

const (
	sqlFunction sqlEnforcer = iota
	sqlProcedure
	sqlPolicy
	sqlTrigger
)

// header reads CREATE [OR REPLACE] [CONSTRAINT] {FUNCTION|PROCEDURE|POLICY|
// TRIGGER} and the name, giving the kind, the symbol name the PostgreSQL
// adapter gives it, and the index of the token after the name (for a policy
// or trigger, after its table).
func (s sqlStmt) header() (sqlEnforcer, string, int, bool) {
	if !s.is(0, "create") {
		return 0, "", 0, false
	}
	i := 1
	if s.is(i, "or") && s.is(i+1, "replace") {
		i += 2
	}
	constraint := s.is(i, "constraint")
	if constraint {
		i++
	}
	switch {
	case !constraint && (s.is(i, "function") || s.is(i, "procedure")):
		kind := sqlFunction
		if s.is(i, "procedure") {
			kind = sqlProcedure
		}
		sch, name, next, ok := s.qname(i + 1)
		return kind, sch + "." + name, next, ok
	case !constraint && s.is(i, "policy"):
		name, ok := s.ident(i + 1)
		if !ok || !s.is(i+2, "on") {
			return 0, "", 0, false
		}
		sch, table, next, ok := s.qname(i + 3)
		return sqlPolicy, "policy " + name + " on " + sch + "." + table, next, ok
	case s.is(i, "trigger"):
		name, ok := s.ident(i + 1)
		if !ok {
			return 0, "", 0, false
		}
		for j := i + 2; j < len(s.toks); j++ {
			if s.is(j, "on") {
				sch, table, next, ok := s.qname(j + 1)
				return sqlTrigger, "trigger " + name + " on " + sch + "." + table, next, ok
			}
		}
	}
	return 0, "", 0, false
}

// generateSQL breaks a PostgreSQL enforcer, found by the line its CREATE
// statement starts on and its name: a policy's expressions always pass, a
// trigger never fires, and a PL/pgSQL function returns before its checks.
// Every break is an insertion, so the statement stays valid SQL; anything
// this cannot place exactly is a *NoBreak.
// @enforces breaktest:INV-1
func generateSQL(src []byte, sym Symbol) (Break, error) {
	if isCheckSymbol(sym) {
		c, err := locateCheck(src, sym)
		if err != nil {
			return Break{}, err
		}
		return c.checkBreak()
	}
	st, kind, next, err := locateSQL(src, sym)
	if err != nil {
		return Break{}, err
	}
	switch kind {
	case sqlPolicy:
		return st.policyBreak(next)
	case sqlTrigger:
		return st.triggerBreak(next)
	}
	return st.functionBreak(next, kind == sqlProcedure)
}

// locateSQL finds the CREATE statement of an enforcer symbol: the one that
// starts on its line and has its name. It returns the statement, its kind and
// the index of the token after its name (after the table, for a policy or
// trigger). A symbol renamed by a later ALTER is found by the name its CREATE
// gave it.
func locateSQL(src []byte, sym Symbol) (sqlStmt, sqlEnforcer, int, error) {
	toks, err := sqlLex(src, 0, len(src), 1)
	if err != nil {
		return sqlStmt{}, 0, 0, noBreak("SQL does not scan: %v; add a script", err)
	}
	want := sym.Name
	if sym.DeclaredAs != "" {
		want = sym.DeclaredAs
	}
	for _, st := range sqlStatements(src, toks) {
		if st.toks[0].line != sym.Line {
			continue
		}
		kind, name, next, ok := st.header()
		if !ok || name != want {
			continue
		}
		return st, kind, next, nil
	}
	return sqlStmt{}, 0, 0, noBreak("no SQL function, policy or trigger %s at line %d", want, sym.Line)
}

// locateAlterPolicy finds the ALTER POLICY statement that starts on line and
// returns it with the index of the token after its table. The policy's name
// is not compared: a rename may have changed it between the CREATE and here.
func locateAlterPolicy(src []byte, line int) (sqlStmt, int, error) {
	toks, err := sqlLex(src, 0, len(src), 1)
	if err != nil {
		return sqlStmt{}, 0, noBreak("SQL does not scan: %v; add a script", err)
	}
	for _, st := range sqlStatements(src, toks) {
		if st.toks[0].line != line || !st.is(0, "alter") || !st.is(1, "policy") || !st.is(3, "on") {
			continue
		}
		if _, _, next, ok := st.qname(4); ok {
			return st, next, nil
		}
	}
	return sqlStmt{}, 0, noBreak("no ALTER POLICY statement at line %d", line)
}

// noPolicyExpr is the reason a policy statement has nothing to break.
const noPolicyExpr = "policy has no USING or WITH CHECK expression; add a script"

// policyBreak turns USING (expr) and WITH CHECK (expr) into
// USING (true OR (expr)): a permissive policy then admits every row, and a
// restrictive one stops restricting, while expr stays in place and still
// has to resolve. It works the same on an ALTER POLICY statement, which
// sets the expressions it names.
func (s sqlStmt) policyBreak(from int) (Break, error) {
	return s.policyBreakOf(from, true, true)
}

// policyBreakOf is policyBreak for the clauses asked for: USING when using
// is set, WITH CHECK when check is.
func (s sqlStmt) policyBreakOf(from int, using, check bool) (Break, error) {
	var ins []Break
	var bad string
	balanced := s.topLevel(from, func(j int) bool {
		open := -1
		switch {
		case using && s.is(j, "using") && s.punct(j+1, '('):
			open = j + 1
		case check && s.is(j, "with") && s.is(j+1, "check") && s.punct(j+2, '('):
			open = j + 2
		default:
			return true
		}
		closeAt := s.matchParen(open)
		if closeAt < 0 {
			return true // topLevel reports the imbalance
		}
		if closeAt == open+1 {
			bad = "empty policy expression"
			return false
		}
		ins = append(ins,
			Break{Offset: s.toks[open].end, Text: "true OR ("},
			Break{Offset: s.toks[closeAt].start, Text: ")"})
		return true
	})
	switch {
	case !balanced:
		return Break{}, noBreak("policy parentheses do not balance; add a script")
	case bad != "":
		return Break{}, noBreak("%s; add a script", bad)
	case len(ins) == 0:
		return Break{}, noBreak(noPolicyExpr)
	}
	return Break{Offset: ins[0].Offset, Text: ins[0].Text, More: ins[1:]}, nil
}

// triggerBreak makes a trigger never fire: WHEN (cond) becomes
// WHEN (false AND (cond)), and a trigger with no WHEN gets " WHEN (false)"
// before EXECUTE. Row and statement triggers and constraint triggers all
// allow WHEN; a statement trigger's condition cannot use NEW or OLD, and
// false uses neither. INSTEAD OF triggers allow no WHEN, so they get none.
func (s sqlStmt) triggerBreak(from int) (Break, error) {
	when, exec, insteadOf := -1, -1, false
	balanced := s.topLevel(1, func(j int) bool {
		switch {
		case j < from && s.is(j, "instead") && s.is(j+1, "of"):
			insteadOf = true
		case j >= from && when < 0 && s.is(j, "when") && s.punct(j+1, '('):
			when = j
		case j >= from && s.is(j, "execute") && (s.is(j+1, "function") || s.is(j+1, "procedure")):
			exec = j
			return false
		}
		return true
	})
	switch {
	case !balanced:
		return Break{}, noBreak("trigger parentheses do not balance; add a script")
	case insteadOf:
		return Break{}, noBreak("INSTEAD OF trigger: WHEN is not allowed; add a script")
	case exec < 0:
		return Break{}, noBreak("trigger has no EXECUTE FUNCTION clause; add a script")
	case when >= 0:
		closeAt := s.matchParen(when + 1)
		return Break{Offset: s.toks[when+1].end, Text: "false AND (",
			More: []Break{{Offset: s.toks[closeAt].start, Text: ")"}}}, nil
	}
	return Break{Offset: s.toks[exec-1].end, Text: " WHEN (false)"}, nil
}

// functionBreak inserts an early return as the first statement of a
// PL/pgSQL function's top-level block, inside its dollar-quoted body. The
// return is chosen from the declared result:
//
//	RETURNS trigger                   RETURN COALESCE(NEW, OLD);  the row goes through unchecked
//	RETURNS void, SETOF ..., TABLE    RETURN;
//	OUT parameters, or a procedure    RETURN;
//	RETURNS boolean                   RETURN true;
//	any other type                    RETURN NULL;
//
// A boolean guard usually says whether something is allowed, and the case
// its verifiers must catch is a denial that no longer happens, so the break
// allows: true, not NULL or false. NEW is NULL in a DELETE trigger and OLD
// in an INSERT one, so COALESCE returns whichever row the trigger has.
// Functions in any other language (sql, c, plv8, ...) get no break.
func (s sqlStmt) functionBreak(from int, procedure bool) (Break, error) {
	if !s.punct(from, '(') {
		return Break{}, noBreak("function has no parameter list; add a script")
	}
	argsEnd := s.matchParen(from)
	if argsEnd < 0 {
		return Break{}, noBreak("function parentheses do not balance; add a script")
	}
	hasOut, argStart := false, true
	for j := from + 1; j < argsEnd; j++ {
		if argStart && (s.is(j, "out") || s.is(j, "inout")) {
			hasOut = true
		}
		argStart = false
		if s.punct(j, '(') {
			j = s.matchParen(j)
		} else if s.punct(j, ',') {
			argStart = true
		}
	}

	var ret, lang string
	retSet, retArray, sqlBody := false, false, false
	body := -1
	balanced := s.topLevel(argsEnd+1, func(j int) bool {
		switch {
		case s.is(j, "returns") && s.is(j+1, "null") && s.is(j+2, "on"):
			// RETURNS NULL ON NULL INPUT is STRICT, not a result type.
		case s.is(j, "returns") && ret == "" && !retSet:
			switch {
			case s.is(j+1, "setof"), s.is(j+1, "table") && s.punct(j+2, '('):
				retSet = true
			default:
				_, name, next, ok := s.qname(j + 1)
				if ok {
					ret, retArray = name, s.punct(next, '[')
				}
			}
		case s.is(j, "language") && lang == "":
			if j+1 < len(s.toks) {
				t := s.toks[j+1]
				lang = strings.ToLower(strings.Trim(string(s.src[t.start:t.end]), `'"`))
			}
		case s.is(j, "as") && body < 0:
			body = j + 1
		case s.is(j, "begin") && s.is(j+1, "atomic"), s.is(j, "return") && body < 0:
			sqlBody = true
		}
		return true
	})
	if !balanced {
		return Break{}, noBreak("function parentheses do not balance; add a script")
	}
	if lang == "" && (sqlBody || body < 0) {
		lang = "sql"
	}
	switch {
	case lang == "":
		return Break{}, noBreak("function declares no LANGUAGE; add a script")
	case lang != "plpgsql":
		return Break{}, noBreak("%s-language function: no generated break; add a script", lang)
	case body < 0 || body >= len(s.toks):
		return Break{}, noBreak("plpgsql function has no AS body; add a script")
	case s.toks[body].kind != sqlDollar:
		return Break{}, noBreak("plpgsql body is not dollar-quoted; add a script")
	}

	var stmt string
	switch {
	case procedure, hasOut, retSet:
		stmt = "RETURN;"
	case ret == "":
		return Break{}, noBreak("function declares no RETURNS and no OUT parameter; add a script")
	case retArray:
		stmt = "RETURN NULL;"
	case ret == "trigger":
		stmt = "RETURN COALESCE(NEW, OLD);"
	case ret == "event_trigger":
		return Break{}, noBreak("event trigger function: no generated break; add a script")
	case ret == "void":
		stmt = "RETURN;"
	case ret == "boolean" || ret == "bool":
		stmt = "RETURN true;"
	default:
		stmt = "RETURN NULL;"
	}

	at, err := plpgsqlBegin(s.src, s.toks[body])
	if err != nil {
		return Break{}, noBreak("plpgsql body: %v; add a script", err)
	}
	return Break{Offset: at, Text: " " + stmt}, nil
}

// plpgsqlBegin returns the offset just after the BEGIN that opens the
// top-level block of a dollar-quoted PL/pgSQL body: the first BEGIN, after
// an optional <<label>> and DECLARE section. Nested blocks come after it, so
// they are never chosen.
func plpgsqlBegin(src []byte, body sqlTok) (int, error) {
	n := sqlDollarTag(src, body.start)
	toks, err := sqlLex(src, body.start+n, body.end-n, body.line)
	if err != nil {
		return 0, err
	}
	b := sqlStmt{src, toks}
	i := 0
	if b.punct(0, '<') && b.punct(1, '<') && b.punct(3, '>') && b.punct(4, '>') {
		i = 5
	}
	if b.is(i, "declare") {
		for j := i + 1; j < len(toks); j++ {
			if b.is(j, "begin") {
				return toks[j].end, nil
			}
		}
		return 0, errors.New("no BEGIN after DECLARE")
	}
	if !b.is(i, "begin") {
		return 0, errors.New("does not start with DECLARE or BEGIN")
	}
	return toks[i].end, nil
}
