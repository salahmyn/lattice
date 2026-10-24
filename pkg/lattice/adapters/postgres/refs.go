package postgres

// @module-feature reachability

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// sqlKeywords are words followed by "(" that are SQL syntax, not calls.
var sqlKeywords = map[string]bool{
	"values": true, "in": true, "exists": true, "any": true, "all": true, "some": true, "array": true,
	"row": true, "filter": true, "over": true, "using": true, "check": true, "when": true, "and": true,
	"or": true, "not": true, "select": true, "if": true, "elsif": true, "while": true, "returns": true,
	"return": true, "as": true, "on": true, "with": true, "from": true, "join": true, "where": true,
	"then": true, "else": true, "case": true, "cast": true, "is": true, "between": true, "like": true,
	"ilike": true, "lateral": true, "perform": true, "execute": true,
	"varchar": true, "char": true, "character": true, "numeric": true, "decimal": true, "timestamp": true,
	"timestamptz": true, "time": true, "interval": true, "bit": true, "varbit": true, "float": true,
}

// skipAfter are keywords whose following name( is a table or column list,
// not a call (insert into t(a, b), references t(id)).
var skipAfter = map[string]bool{
	"into": true, "references": true, "table": true, "index": true, "type": true, "view": true,
	"sequence": true, "extension": true, "constraint": true, "key": true, "unique": true,
}

// sqlCall is one name( in SQL text.
type sqlCall struct {
	schema, name string
	line         int // 0-based line offset in the text
}

// sqlCalls returns the calls name( and schema.name( in SQL text. Quoted
// identifiers are read; single-quoted strings are skipped unless
// withStrings (a pgTAP assertion runs the SQL it quotes); dollar-quoted
// bodies are code and are read.
func sqlCalls(text string, withStrings bool) []sqlCall {
	var out []sqlCall
	prevWord := ""
	line := 0
	n := len(text)
	isIdent := func(c byte) bool {
		return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	readIdent := func(i int) (string, int) {
		if i < n && text[i] == '"' {
			j := i + 1
			for j < n && !(text[j] == '"' && (j+1 >= n || text[j+1] != '"')) {
				if text[j] == '"' {
					j++
				}
				j++
			}
			if j >= n {
				return "", n
			}
			return text[i : j+1], j + 1
		}
		j := i
		for j < n && isIdent(text[j]) {
			j++
		}
		return text[i:j], j
	}
	for i := 0; i < n; {
		c := text[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == '-' && i+1 < n && text[i+1] == '-':
			// A comment in a body: its words name nothing, and an apostrophe
			// in it ("each other's") must not open a string.
			for i < n && text[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				end = n - i - 2
			}
			line += strings.Count(text[i:i+2+end], "\n")
			i += end + 4
		case c == '\'' && !withStrings:
			j := i + 1
			for j < n {
				if text[j] == '\'' {
					if j+1 < n && text[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				if text[j] == '\n' {
					line++
				}
				j++
			}
			i = j + 1
		case c == '$' && i+1 < n && (text[i+1] == '$' || text[i+1] >= 'a' && text[i+1] <= 'z' || text[i+1] >= 'A' && text[i+1] <= 'Z'):
			if tag, ok := dollarTag(text[i:]); ok {
				i += len(tag) // a body: read on as code
				continue
			}
			i++
		case c == '"' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			first, j := readIdent(i)
			schema, name := "", first
			k := j
			for k < n && (text[k] == ' ' || text[k] == '\t') {
				k++
			}
			if k < n && text[k] == '.' {
				k++
				for k < n && (text[k] == ' ' || text[k] == '\t') {
					k++
				}
				if k < n && (text[k] == '"' || isIdent(text[k])) {
					second, k2 := readIdent(k)
					schema, name, j = first, second, k2
				}
			}
			k = j
			for k < n && (text[k] == ' ' || text[k] == '\t' || text[k] == '\n' || text[k] == '\r') {
				k++
			}
			lower := strings.ToLower(name)
			if k < n && text[k] == '(' && !skipAfter[prevWord] && (schema != "" || !sqlKeywords[lower]) {
				out = append(out, sqlCall{schema: schema, name: name, line: line})
			}
			prevWord = strings.ToLower(first)
			if schema != "" {
				prevWord = lower
			}
			i = j
		case c >= '0' && c <= '9':
			for i < n && isIdent(text[i]) {
				i++
			}
		default:
			if c != ' ' && c != '\t' && c != '\r' {
				prevWord = ""
			}
			i++
		}
	}
	return out
}

// callRefs turns the calls in a statement into refs with their targets:
// sql.<schema>.<name>, an unqualified name in public (Postgres's default
// search_path). A name that is no function of the workspace (a builtin, a
// type modifier) resolves to nothing in the core and counts as external.
func callRefs(st statement, withStrings bool) []ir.Ref {
	var out []ir.Ref
	for _, c := range sqlCalls(st.text, withStrings) {
		s, name := qualify(c.name, "")
		if c.schema != "" {
			s, name = qualify(c.schema, c.name)
		}
		out = append(out, ir.Ref{Name: name, Target: functionFQN(s, name), Kind: ir.RefCall, Line: st.line + c.line})
	}
	return out
}

var (
	reCronSchedule   = regexp.MustCompile(`(?is)\bcron\s*\.\s*schedule\s*\(`)
	reCronUnschedule = regexp.MustCompile(`(?is)\bcron\s*\.\s*unschedule\s*\(`)
)

// namesOnly are statements that name functions without running them.
var namesOnly = regexp.MustCompile(`(?is)^(grant|revoke|comment|drop|security\s+label|alter\s+(function|procedure|routine|extension)|create\s+(extension|schema|type|domain|aggregate|cast|operator)|reassign|set|reset|notify|listen)\b`)

// appliedRefs returns the calls of a top-level statement that the database
// runs when it applies the file: a seed select f(...), a do $$ ... $$
// block, a check constraint, a column default or a view that calls a
// function. Statements that only name functions (grant, revoke, comment,
// drop, alter function) give none.
func appliedRefs(st statement) []ir.Ref {
	if namesOnly.MatchString(st.text) {
		return nil
	}
	return callRefs(st, false)
}

// appliedNode is the root holding a migration's (or schema file's)
// top-level calls: the database runs them when the file is applied.
func appliedNode(file string, line int, refs []ir.Ref) ir.Node {
	return ir.Node{FQN: "sql.applied." + seg(strings.TrimSuffix(file, path.Ext(file))), Kind: ir.NodeTop,
		Root: "migration", Line: line, Refs: refs}
}

// cronUnscheduleKind marks a cron.unschedule in ModuleAnnotations; Resolve
// consumes it.
const cronUnscheduleKind = "sql_cron_unschedule"

// cronNode reads cron.schedule('job', '<cron>', $$ ... $$) (or the
// two-argument form without a name) into a root node holding the calls of
// its command. ok is false for any other statement.
func cronNode(mod *ir.Module, st statement) (ir.Node, bool) {
	if loc := reCronUnschedule.FindStringIndex(st.text); loc != nil {
		if args := callArgs(st.text[loc[0]:]); len(args) > 0 {
			if name, ok := stringLiteral(args[0]); ok {
				mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: cronUnscheduleKind,
					Args: []interface{}{cronFQN(name, mod.File, st.line)}, Line: st.line})
			}
		}
		return ir.Node{}, false
	}
	loc := reCronSchedule.FindStringIndex(st.text)
	if loc == nil {
		return ir.Node{}, false
	}
	args := callArgs(st.text[loc[0]:])
	var name, command string
	switch len(args) {
	case 2:
		command = args[1]
	case 3:
		name, _ = stringLiteral(args[0])
		command = args[2]
	default:
		return ir.Node{}, false
	}
	body := command
	if lit, ok := stringLiteral(command); ok {
		body = lit
	} else if tag, ok := dollarTag(strings.TrimSpace(command)); ok {
		body = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(command), tag), tag)
	}
	n := ir.Node{FQN: cronFQN(name, mod.File, st.line), Kind: ir.NodeCron, Root: "cron", Line: st.line}
	n.Refs = callRefs(statement{text: body, line: st.line}, true)
	return n, true
}

// cronFQN names a cron job: sql.cron.<job>, or for an unnamed job its file
// and line.
func cronFQN(name, file string, line int) string {
	if name != "" {
		return "sql.cron." + seg(name)
	}
	stem := strings.TrimSuffix(path.Base(file), path.Ext(file))
	return fmt.Sprintf("sql.cron.%s_%d", seg(stem), line)
}

// tableFQN is the call-graph node of a table: a .from('t') reaches it, and
// it reaches the table's policies and triggers.
func tableFQN(schemaName, table string) string {
	return "sql.table." + seg(schemaName) + "." + seg(table)
}

// linkRefs fills a definitions module's references (docs/rnd/0009):
// function -> function calls in the body, trigger -> its function, policy ->
// the functions its expressions call, table -> its policies and triggers.
// Triggers and policies are roots: the database runs them.
// @enforces reachability:INV-1
func linkRefs(mod *ir.Module, sym *ir.Symbol, st statement, kind string, schemaName, table string) {
	for _, r := range callRefs(st, false) {
		if r.Target != sym.FQN { // the definition's own header
			sym.Refs = append(sym.Refs, r)
		}
	}
	switch kind {
	case "trigger", "policy":
		sym.Root = kind
		mod.Nodes = append(mod.Nodes, ir.Node{FQN: tableFQN(schemaName, table), Kind: ir.NodeTable, Line: st.line,
			Refs: []ir.Ref{{Name: sym.Name, Target: sym.FQN, Kind: ir.RefValue, Line: st.line}}})
	}
}
