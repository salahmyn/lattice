package postgres

// @module-feature adapters.postgres

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Table writes. A promise about a column is only as strong as the set of
// code that writes the column, so the adapter records, for each SQL function,
// procedure and DO block, the INSERT, UPDATE, DELETE and MERGE statements in
// its body (ir.Symbol.Writes), and for a migration the top-level DML it runs
// itself (ir.Module.Writes). Test files are never writers.
//
// It records only what it parsed. A statement it could not read, an EXECUTE
// of a string, a write through a view, a foreign-key action and a rule leave
// the table's writer set unknown (ir.WriterGap), never empty.

// gapKind marks a write the adapter could not read in ModuleAnnotations;
// Resolve consumes it.
const gapKind = "sql_writer_gap"

// newTable marks a write to NEW.<column> in a function body; Resolve turns it
// into a write on the table of each trigger that runs the function.
const newTable = "NEW"

// tableOf is the "<schema>.<table>" a possibly qualified name stands for.
func tableOf(first, second string) string {
	s, n := qualify(first, second)
	return s + "." + n
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// maskCode blanks comments, single-quoted strings and dollar-quoted bodies,
// keeping every newline, the length and double-quoted identifiers, so a regex
// over the result sees only code, at its true offsets.
func maskCode(s string) string {
	b := []byte(s)
	blank := func(from, to int) {
		for k := from; k < to && k < len(b); k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		switch {
		case c == '-' && i+1 < n && s[i+1] == '-':
			j := i
			for j < n && s[j] != '\n' {
				j++
			}
			blank(i, j)
			i = j
		case c == '/' && i+1 < n && s[i+1] == '*':
			j, depth := i+2, 1
			for j < n && depth > 0 {
				switch {
				case s[j] == '/' && j+1 < n && s[j+1] == '*':
					depth++
					j += 2
				case s[j] == '*' && j+1 < n && s[j+1] == '/':
					depth--
					j += 2
				default:
					j++
				}
			}
			blank(i, j)
			i = j
		case c == '\'' || (c == 'E' || c == 'e') && i+1 < n && s[i+1] == '\'' && (i == 0 || !isWordByte(s[i-1])):
			ln := 0
			j := skipToken(s, i, &ln)
			blank(i, j)
			i = j
		case c == '"':
			ln := 0
			i = skipToken(s, i, &ln)
		case c == '$' && (i == 0 || !isWordByte(s[i-1])):
			ln := 0
			j := skipToken(s, i, &ln)
			if j > i+1 {
				blank(i, j)
			}
			i = j
		default:
			i++
		}
	}
	return string(b)
}

// lineIndex maps a byte offset to the number of newlines before it.
type lineIndex []int

func newLineIndex(s string) lineIndex {
	var l lineIndex
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			l = append(l, i)
		}
	}
	return l
}

func (l lineIndex) before(off int) int { return sort.SearchInts(l, off) }

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// prevWord is the word, or the single punctuation byte, before off.
func prevWord(s string, off int) string {
	i := off
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t' || s[i-1] == '\n' || s[i-1] == '\r') {
		i--
	}
	if i == 0 {
		return ""
	}
	if !isWordByte(s[i-1]) {
		return string(s[i-1])
	}
	j := i
	for j > 0 && isWordByte(s[j-1]) {
		j--
	}
	return strings.ToLower(s[j:i])
}

// statementStart are the words and punctuation after which a statement
// begins inside a function body.
var statementStart = map[string]bool{"": true, ";": true, "(": true, ")": true, "then": true, "else": true,
	"begin": true, "loop": true, "atomic": true}

// matchParen returns the offset of the ")" that closes the "(" at open.
func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// stmtEnd returns where the statement that has started at from ends: a ";" at
// depth 0, or the ")" that closes the group the statement sits in.
func stmtEnd(mask string, from int) int {
	depth := 0
	for i := from; i < len(mask); i++ {
		switch mask[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		case ';':
			if depth == 0 {
				return i
			}
		}
	}
	return len(mask)
}

var (
	reInsertInto = regexp.MustCompile(`(?is)\binsert\s+into\s+(?:only\s+)?` + qname)
	reUpdateSet  = regexp.MustCompile(`(?is)\bupdate\s+(?:only\s+)?` + qname + `\s*\*?(?:\s+(?:as\s+)?(?:` + ident + `))?\s+set\b`)
	reDeleteFrom = regexp.MustCompile(`(?is)\bdelete\s+from\s+(?:only\s+)?` + qname)
	reMergeInto  = regexp.MustCompile(`(?is)\bmerge\s+into\s+(?:only\s+)?` + qname)
	reTruncate   = regexp.MustCompile(`(?is)\btruncate\s+(?:table\s+)?(?:only\s+)?` + qname)
	reTruncMore  = regexp.MustCompile(`(?is)^\s*,\s*(?:only\s+)?` + qname)
	reCopyFrom   = regexp.MustCompile(`(?is)^\s*copy\s+` + qname + `\s*(?:\(([^)]*)\))?\s+from\b`)
	reDMLStart   = regexp.MustCompile(`(?is)\b(?:insert\s+into|delete\s+from|merge\s+into)\b`)
	reUpdateWord = regexp.MustCompile(`(?is)\bupdate\b`)
	reOnConflict = regexp.MustCompile(`(?is)\bon\s+conflict\b`)
	reDoUpdate   = regexp.MustCompile(`(?is)\bdo\s+update\s+set\b`)
	reMergeThen  = regexp.MustCompile(`(?is)\bthen\s+(update\s+set|insert|delete)\b`)
	reExecuteKw  = regexp.MustCompile(`(?is)\bexecute\b`)
	reFormatOpen = regexp.MustCompile(`(?is)^format\s*\(\s*`)
	// reWriteForm finds the forms of a write statement in a string, so that
	// "for update" in a CREATE POLICY string is not one.
	reWriteForm = regexp.MustCompile(`(?is)\b(?:insert\s+into|delete\s+from|merge\s+into|truncate\b|copy\s+\S+\s+from|update\b[^;]*?\bset\b)`)
	reIdentAt   = regexp.MustCompile(`^(?:` + ident + `)`)
	reNewAssign = regexp.MustCompile(`(?is)\bnew\s*\.\s*(` + ident + `)((?:\s*\.\s*(?:` + ident + `)|\s*\[[^\]]*\])*)\s*(:=|=)`)
	reNewRow    = regexp.MustCompile(`(?is)\bnew\s*(:=|=)`)
	reIntoNew   = regexp.MustCompile(`(?is)\binto\s+(?:strict\s+)?new\s*\.\s*(` + ident + `)`)
)

// reader reads the table writes of one piece of code: a function body, a DO
// block, or one top-level statement.
type reader struct {
	file   string
	code   string
	mask   string
	base   int // the file line of the code's first byte
	lines  lineIndex
	seen   map[int]bool // offsets of the write keywords already read
	writes []ir.TableWrite
	gaps   []ir.WriterGap
}

// readCode reads the writes in code, which starts at file line base.
// trigger says NEW.<column> assignments count (a function body). A statement
// that starts like a write and cannot be read, and an EXECUTE, are gaps.
// @enforces adapters.postgres:INV-7
func readCode(file, code string, base int, trigger bool) ([]ir.TableWrite, []ir.WriterGap) {
	r := &reader{file: file, code: code, mask: maskCode(code), base: base, lines: newLineIndex(code), seen: map[int]bool{}}
	r.inserts()
	r.updates()
	r.deletes()
	r.merges()
	r.truncates()
	r.copies()
	r.unreadable()
	r.executes()
	if trigger {
		r.newColumns()
	}
	sort.SliceStable(r.writes, func(i, j int) bool { return r.writes[i].Line < r.writes[j].Line })
	return r.writes, r.gaps
}

func (r *reader) line(off int) int { return r.base + r.lines.before(off) }

func (r *reader) gap(off int, table string, cols []string, reason string) {
	r.gaps = append(r.gaps, ir.WriterGap{Table: table, Columns: cols, Reason: reason, File: r.file, Line: r.line(off)})
}

func (r *reader) add(table, op string, cols []string, off int) {
	cols = uniqueSorted(cols)
	r.writes = append(r.writes, ir.TableWrite{Table: table, Op: op, Columns: cols, Line: r.line(off)})
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// tableAt reads the table name a qname match names. ok is false for a word
// that is syntax, not a table.
func (r *reader) tableAt(loc []int, g int) (string, bool) {
	first := r.mask[loc[g*2]:loc[g*2+1]]
	second := ""
	if loc[g*2+2] >= 0 {
		second = r.mask[loc[g*2+2]:loc[g*2+3]]
	}
	if second == "" {
		switch strings.ToLower(first) {
		case "set", "select", "values", "only", "table":
			return "", false
		}
	}
	return tableOf(first, second), true
}

// firstIdent reads the identifier a text starts with.
func firstIdent(s string) (string, bool) {
	m := reIdentAt.FindString(strings.TrimSpace(s))
	if m == "" {
		return "", false
	}
	return unquote(m), true
}

// splitTop splits s at commas outside parentheses.
func splitTop(s string) []string {
	var out []string
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
				out = append(out, s[last:i])
				last = i + 1
			}
		}
	}
	return append(out, s[last:])
}

// columnList reads the identifiers of "a, b, c".
func columnList(s string) ([]string, bool) {
	var out []string
	for _, item := range splitTop(s) {
		c, ok := firstIdent(item)
		if !ok {
			return nil, false
		}
		out = append(out, c)
	}
	return out, len(out) > 0
}

// readSet reads the columns an UPDATE ... SET (or ON CONFLICT DO UPDATE SET,
// or MERGE ... UPDATE SET) assigns, from from to the end of its clause. A
// clause that ends at WHERE, FROM, RETURNING, or, in a MERGE, WHEN.
func readSet(mask string, from, end int, merge bool) ([]string, bool) {
	depth, caseDepth, start, stop := 0, 0, from, end
	var items []string
loop:
	for i := from; i < end; {
		c := mask[i]
		switch {
		case c == '(':
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
		case c == '"':
			ln := 0
			i = skipToken(mask, i, &ln)
		case c == ',' && depth == 0 && caseDepth == 0:
			items = append(items, mask[start:i])
			start = i + 1
			i++
		case isWordByte(c) && (i == 0 || !isWordByte(mask[i-1])):
			j := i
			for j < end && isWordByte(mask[j]) {
				j++
			}
			if depth == 0 {
				switch strings.ToLower(mask[i:j]) {
				case "case":
					caseDepth++
				case "end":
					if caseDepth > 0 {
						caseDepth--
					}
				case "where", "returning", "from":
					if caseDepth == 0 {
						stop = i
						break loop
					}
				case "when":
					if merge && caseDepth == 0 {
						stop = i
						break loop
					}
				}
			}
			i = j
		default:
			i++
		}
	}
	items = append(items, mask[start:stop])
	var cols []string
	for _, it := range items {
		t := strings.TrimSpace(it)
		if t == "" {
			return nil, false
		}
		if t[0] == '(' {
			close := matchParen(t, 0)
			if close < 0 || !strings.HasPrefix(strings.TrimSpace(t[close+1:]), "=") {
				return nil, false
			}
			list, ok := columnList(t[1:close])
			if !ok {
				return nil, false
			}
			cols = append(cols, list...)
			continue
		}
		c, ok := firstIdent(t)
		if !ok || !strings.Contains(t, "=") {
			return nil, false
		}
		cols = append(cols, c)
	}
	return cols, len(cols) > 0
}

// insertColumns reads what follows INSERT INTO <table>: an optional alias and
// an optional column list. cols is [WriteAll] when none is given.
func insertColumns(mask string, from int) (cols []string, listEnd int, ok bool) {
	p := skipWS(mask, from)
	if m := reIdentAt.FindString(mask[p:]); m != "" {
		switch w := strings.ToLower(m); w {
		case "as":
			q := skipWS(mask, p+2)
			if a := reIdentAt.FindString(mask[q:]); a != "" {
				p = skipWS(mask, q+len(a))
			}
		case "values", "select", "default", "overriding", "on", "with", "table", "returning":
		default:
			p = skipWS(mask, p+len(m))
		}
	}
	if p < len(mask) && mask[p] == '(' {
		close := matchParen(mask, p)
		if close < 0 {
			return nil, p, false
		}
		inner := strings.TrimSpace(mask[p+1 : close])
		if w, _ := firstWord(inner); w != "select" && w != "values" && w != "with" && w != "table" {
			list, good := columnList(inner)
			if !good {
				return nil, close, false
			}
			return list, close + 1, true
		}
	}
	return []string{ir.WriteAll}, p, true
}

func firstWord(s string) (string, int) {
	j := 0
	for j < len(s) && isWordByte(s[j]) {
		j++
	}
	return strings.ToLower(s[:j]), j
}

func (r *reader) inserts() {
	for _, loc := range reInsertInto.FindAllStringSubmatchIndex(r.mask, -1) {
		table, ok := r.tableAt(loc, 1)
		if !ok {
			continue
		}
		r.seen[loc[0]] = true
		cols, from, good := insertColumns(r.mask, loc[1])
		if !good {
			r.gap(loc[0], table, nil, fmt.Sprintf("the INSERT into %s at %s:%d has a column list the adapter could not read", table, r.file, r.line(loc[0])))
			continue
		}
		r.add(table, ir.WriteInsert, cols, loc[0])
		end := stmtEnd(r.mask, loc[1])
		if from > end {
			continue
		}
		oc := reOnConflict.FindStringIndex(r.mask[from:end])
		if oc == nil {
			continue
		}
		du := reDoUpdate.FindStringIndex(r.mask[from+oc[0] : end])
		if du == nil {
			continue
		}
		setFrom := from + oc[0] + du[1]
		set, good := readSet(r.mask, setFrom, end, false)
		if !good {
			set = []string{ir.WriteAll}
		}
		r.add(table, ir.WriteUpdate, set, from+oc[0]+du[0])
	}
}

func (r *reader) updates() {
	for _, loc := range reUpdateSet.FindAllStringSubmatchIndex(r.mask, -1) {
		table, ok := r.tableAt(loc, 1)
		if !ok {
			continue
		}
		r.seen[loc[0]] = true
		set, good := readSet(r.mask, loc[1], stmtEnd(r.mask, loc[1]), false)
		if !good {
			set = []string{ir.WriteAll}
		}
		r.add(table, ir.WriteUpdate, set, loc[0])
	}
}

func (r *reader) deletes() {
	for _, loc := range reDeleteFrom.FindAllStringSubmatchIndex(r.mask, -1) {
		table, ok := r.tableAt(loc, 1)
		if !ok {
			continue
		}
		r.seen[loc[0]] = true
		r.add(table, ir.WriteDelete, nil, loc[0])
	}
}

func (r *reader) truncates() {
	for _, loc := range reTruncate.FindAllStringSubmatchIndex(r.mask, -1) {
		table, ok := r.tableAt(loc, 1)
		if !ok {
			continue
		}
		r.add(table, ir.WriteDelete, nil, loc[0])
		for pos := loc[1]; ; {
			m := reTruncMore.FindStringSubmatchIndex(r.mask[pos:])
			if m == nil {
				break
			}
			shift := make([]int, len(m))
			for i, v := range m {
				if v >= 0 {
					shift[i] = v + pos
				} else {
					shift[i] = v
				}
			}
			if t, ok := r.tableAt(shift, 1); ok {
				r.add(t, ir.WriteDelete, nil, loc[0])
			}
			pos = shift[1]
		}
	}
}

func (r *reader) copies() {
	loc := reCopyFrom.FindStringSubmatchIndex(r.mask)
	if loc == nil {
		return
	}
	table, ok := r.tableAt(loc, 1)
	if !ok {
		return
	}
	cols := []string{ir.WriteAll}
	if loc[6] >= 0 {
		if list, good := columnList(r.mask[loc[6]:loc[7]]); good {
			cols = list
		}
	}
	r.add(table, ir.WriteInsert, cols, loc[0])
}

func (r *reader) merges() {
	for _, loc := range reMergeInto.FindAllStringSubmatchIndex(r.mask, -1) {
		table, ok := r.tableAt(loc, 1)
		if !ok {
			continue
		}
		r.seen[loc[0]] = true
		end := stmtEnd(r.mask, loc[1])
		for _, th := range reMergeThen.FindAllStringSubmatchIndex(r.mask[loc[1]:end], -1) {
			off := loc[1] + th[0]
			after := loc[1] + th[1]
			switch kind := strings.Join(strings.Fields(strings.ToLower(r.mask[loc[1]+th[2]:loc[1]+th[3]])), " "); kind {
			case "update set":
				set, good := readSet(r.mask, after, end, true)
				if !good {
					set = []string{ir.WriteAll}
				}
				r.add(table, ir.WriteUpdate, set, off)
			case "insert":
				cols := []string{ir.WriteAll}
				p := skipWS(r.mask, after)
				if p < end && r.mask[p] == '(' {
					if close := matchParen(r.mask, p); close > 0 {
						if list, good := columnList(r.mask[p+1 : close]); good {
							cols = list
						}
					}
				}
				r.add(table, ir.WriteInsert, cols, off)
			case "delete":
				r.add(table, ir.WriteDelete, nil, off)
			}
		}
	}
}

// unreadable reports a statement that starts like a write and that no reader
// above could name a table for.
func (r *reader) unreadable() {
	flag := func(off int) {
		r.gap(off, ir.WriteAll, nil, fmt.Sprintf("a statement at %s:%d starts like a table write the adapter could not read, so any table may be written", r.file, r.line(off)))
	}
	for _, loc := range reDMLStart.FindAllStringIndex(r.mask, -1) {
		if !r.seen[loc[0]] {
			flag(loc[0])
		}
	}
	for _, loc := range reUpdateWord.FindAllStringIndex(r.mask, -1) {
		if r.seen[loc[0]] || !statementStart[prevWord(r.mask, loc[0])] {
			continue
		}
		flag(loc[0])
	}
}

// leadingLiteral reads a string literal at p of code: a single-quoted string
// or a dollar-quoted body.
func leadingLiteral(code string, p int) (string, int, bool) {
	if p >= len(code) {
		return "", p, false
	}
	switch c := code[p]; {
	case c == '\'' || (c == 'E' || c == 'e') && p+1 < len(code) && code[p+1] == '\'':
		ln := 0
		j := skipToken(code, p, &ln)
		lit, ok := stringLiteral(code[p:j])
		return lit, j, ok
	case c == '$':
		if tag, ok := dollarTag(code[p:]); ok {
			if end := strings.Index(code[p+len(tag):], tag); end >= 0 {
				return code[p+len(tag) : p+len(tag)+end], p + len(tag) + end + len(tag), true
			}
		}
	}
	return "", p, false
}

// tablesIn lists the tables a statement string writes, as the readers above
// read them.
func tablesIn(lit string) []string {
	mask := maskCode(lit)
	var out []string
	for _, re := range []*regexp.Regexp{reInsertInto, reUpdateSet, reDeleteFrom, reMergeInto, reTruncate} {
		for _, loc := range re.FindAllStringSubmatchIndex(mask, -1) {
			first := mask[loc[2]:loc[3]]
			second := ""
			if loc[4] >= 0 {
				second = mask[loc[4]:loc[5]]
			}
			if second == "" && strings.EqualFold(first, "set") {
				continue
			}
			out = append(out, tableOf(first, second))
		}
	}
	return uniqueSorted(out)
}

// executes marks what an EXECUTE may write. A statement string that holds no
// write keyword writes nothing; one that names its tables leaves those
// unknown; one built at run time, or one that names no table literally, leaves
// every table unknown.
func (r *reader) executes() {
	for _, loc := range reExecuteKw.FindAllStringIndex(r.mask, -1) {
		// EXECUTE FUNCTION of a trigger, and the EXECUTE privilege of a GRANT.
		if w, _ := firstWord(strings.TrimSpace(r.mask[loc[1]:])); w == "function" || w == "procedure" || w == "on" {
			continue
		}
		p := skipWS(r.code, loc[1])
		lit, _, ok := leadingLiteral(r.code, p)
		if !ok {
			if m := reFormatOpen.FindString(r.code[p:]); m != "" {
				lit, _, ok = leadingLiteral(r.code, p+len(m))
			}
		}
		where := fmt.Sprintf("%s:%d", r.file, r.line(loc[0]))
		if !ok {
			r.gap(loc[0], ir.WriteAll, nil, fmt.Sprintf("an EXECUTE at %s builds its statement at run time, so it may write any table", where))
			continue
		}
		if !reWriteForm.MatchString(lit) {
			continue
		}
		tables := tablesIn(lit)
		if len(tables) == 0 {
			r.gap(loc[0], ir.WriteAll, nil, fmt.Sprintf("an EXECUTE at %s runs a write statement that names no table literally, so it may write any table", where))
			continue
		}
		for _, t := range tables {
			r.gap(loc[0], t, nil, fmt.Sprintf("an EXECUTE at %s runs a statement string that writes %s, which the adapter does not read as a write", where, t))
		}
	}
}

// newColumns reads the columns a trigger function assigns on NEW, one write
// for each assignment.
func (r *reader) newColumns() {
	for _, loc := range reNewAssign.FindAllStringSubmatchIndex(r.mask, -1) {
		if r.mask[loc[6]:loc[7]] == "=" && !statementStart[prevWord(r.mask, loc[0])] {
			continue // a comparison
		}
		r.add(newTable, ir.WriteUpdate, []string{unquote(r.mask[loc[2]:loc[3]])}, loc[0])
	}
	for _, loc := range reNewRow.FindAllStringSubmatchIndex(r.mask, -1) {
		if r.mask[loc[2]:loc[3]] == "=" && !statementStart[prevWord(r.mask, loc[0])] {
			continue
		}
		r.add(newTable, ir.WriteUpdate, []string{ir.WriteAll}, loc[0])
	}
	for _, loc := range reIntoNew.FindAllStringSubmatchIndex(r.mask, -1) {
		r.add(newTable, ir.WriteUpdate, []string{unquote(r.mask[loc[2]:loc[3]])}, loc[0])
	}
}

// codePart is a piece of a statement that is code: a dollar-quoted body.
type codePart struct {
	text string
	line int
}

// codeBodies returns the bodies of a CREATE FUNCTION, CREATE PROCEDURE or DO
// statement: its dollar-quoted bodies, or the quoted string after AS, or, for
// a BEGIN ATOMIC body, the statement itself.
func codeBodies(text string, line int) []codePart {
	var parts []codePart
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '$' && (i == 0 || !isWordByte(text[i-1])):
			if tag, ok := dollarTag(text[i:]); ok {
				if end := strings.Index(text[i+len(tag):], tag); end >= 0 {
					start := i + len(tag)
					parts = append(parts, codePart{text[start : start+end], line + strings.Count(text[:start], "\n")})
					i = start + end + len(tag)
					continue
				}
			}
			i++
		case c == '\'':
			ln := 0
			j := skipToken(text, i, &ln)
			if prevWord(text, i) == "as" {
				if lit, ok := stringLiteral(text[i:j]); ok {
					parts = append(parts, codePart{lit, line + strings.Count(text[:i], "\n")})
				}
			}
			i = j
		case c == '"':
			ln := 0
			i = skipToken(text, i, &ln)
		default:
			i++
		}
	}
	if len(parts) == 0 {
		parts = append(parts, codePart{text, line})
	}
	return parts
}

// gapEvent records an incomplete writer set for Resolve. owner and ownerLine
// name the function whose body it is read from, so that a function redefined
// later takes its gaps with it; owner is empty for a statement a migration
// runs itself.
func gapEvent(mod *ir.Module, g ir.WriterGap, owner string, ownerLine int) {
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: gapKind, Line: g.Line,
		Kwargs: map[string]interface{}{"table": g.Table, "columns": g.Columns, "reason": g.Reason,
			"file": g.File, "owner": owner, "owner_line": ownerLine}})
}

// readFunctionWrites records the writes and gaps of a function or procedure.
func readFunctionWrites(mod *ir.Module, sym *ir.Symbol, st statement) {
	for _, part := range codeBodies(st.text, st.line) {
		w, g := readCode(mod.File, part.text, part.line, true)
		sym.Writes = append(sym.Writes, w...)
		for _, gap := range g {
			gapEvent(mod, gap, sym.FQN, st.line)
		}
	}
}

var (
	reTopDML   = regexp.MustCompile(`(?is)^(?:with|insert|update|delete|merge|truncate|copy)\b`)
	reDoBlock  = regexp.MustCompile(`(?is)^do\b`)
	reCronCall = regexp.MustCompile(`(?is)\bcron\s*\.\s*schedule\s*\(`)
)

// readMigrationWrites records the writes a migration makes itself: its
// top-level DML, its DO blocks, and the commands it schedules with pg_cron.
func readMigrationWrites(mod *ir.Module, st statement) {
	read := func(code string, base int) {
		w, g := readCode(mod.File, code, base, false)
		mod.Writes = append(mod.Writes, w...)
		for _, gap := range g {
			gapEvent(mod, gap, "", 0)
		}
	}
	switch {
	case reDoBlock.MatchString(st.text):
		for _, part := range codeBodies(st.text, st.line) {
			read(part.text, part.line)
		}
	case reTopDML.MatchString(st.text):
		read(st.text, st.line)
	case reCronCall.MatchString(st.text):
		// The command a job runs is a string or a dollar-quoted body; each
		// that holds a write keyword is read as one.
		for _, part := range codeBodies(st.text, st.line) {
			read(part.text, part.line)
		}
		for i := 0; i < len(st.text); {
			if st.text[i] != '\'' {
				i++
				continue
			}
			ln := 0
			j := skipToken(st.text, i, &ln)
			if lit, ok := stringLiteral(st.text[i:j]); ok && reWriteForm.MatchString(lit) {
				read(lit, st.line+strings.Count(st.text[:i], "\n"))
			}
			i = j
		}
	}
}

var (
	rePolicyRestrictive = regexp.MustCompile(`(?is)\bas\s+restrictive\b`)
	rePolicyTo          = regexp.MustCompile(`(?is)\bto\s+(.+?)(?:\s+using\b|\s+with\s+check\b|\s*$)`)
	rePolicyFor         = regexp.MustCompile(`(?is)\bfor\s+(all|select|insert|update|delete)\b`)
	reTrigEvents        = regexp.MustCompile(`(?is)^create\s+(?:or\s+replace\s+)?(?:constraint\s+)?trigger\s+(?:` + ident + `)\s+(?:before|after|instead\s+of)\s+(.*?)\s+on\s+` + qname)
	reTrigExec          = regexp.MustCompile(`(?is)\bexecute\s+(?:function|procedure)\s+` + qname)
	reRule              = regexp.MustCompile(`(?is)^create\s+(?:or\s+replace\s+)?rule\s+(?:` + ident + `)\s+as\s+on\s+(?:insert|update|delete|select)\s+to\s+` + qname)
)

// policyGuard reads the commands a policy applies to.
func policyGuard(text, schemaName, table string) *ir.Guard {
	g := &ir.Guard{Kind: ir.GuardPolicy, Table: schemaName + "." + table}
	cmd := "all"
	if m := rePolicyFor.FindStringSubmatch(blankNested(text)); m != nil {
		cmd = strings.ToLower(m[1])
	}
	if cmd == "all" {
		g.Commands = []string{ir.WriteInsert, ir.WriteUpdate, ir.WriteDelete}
	} else {
		g.Commands = []string{cmd}
	}
	blanked := blankNested(text)
	g.Restrictive = rePolicyRestrictive.MatchString(blanked)
	if m := rePolicyTo.FindStringSubmatch(blanked); m != nil {
		g.Roles, _ = readRoles(&tokens{t: privTokens(m[1])})
	}
	return g
}

// triggerGuard reads the events a trigger fires on, and the function it runs.
func triggerGuard(text, schemaName, table string) *ir.Guard {
	g := &ir.Guard{Kind: ir.GuardTrigger, Table: schemaName + "." + table}
	if m := reTrigEvents.FindStringSubmatch(text); m != nil {
		for _, ev := range regexp.MustCompile(`(?is)\s+or\s+`).Split(m[1], -1) {
			words := strings.Fields(strings.ToLower(strings.NewReplacer(",", " , ").Replace(ev)))
			if len(words) == 0 {
				continue
			}
			switch words[0] {
			case "insert", "delete", "truncate":
				g.Commands = append(g.Commands, words[0])
			case "update":
				g.Commands = append(g.Commands, ir.WriteUpdate)
				if len(words) > 2 && words[1] == "of" {
					for _, w := range words[2:] {
						if w != "," {
							g.Columns = append(g.Columns, unquote(strings.Trim(w, ",")))
						}
					}
				}
			}
		}
	}
	if m := reTrigExec.FindStringSubmatch(text); m != nil {
		s, n := qualify(m[1], m[2])
		g.Function = functionFQN(s, n)
	}
	g.Commands = uniqueSorted(g.Commands)
	g.Columns = uniqueSorted(g.Columns)
	return g
}
