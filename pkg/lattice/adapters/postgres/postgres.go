// Package postgres is the Lattice adapter for PostgreSQL: it reads the rules a
// database enforces (functions, row-level security policies, triggers) from
// migration files and the pgTAP tests that verify them, so an invariant
// enforced in SQL is traced and demonstrated like one in application code.
//
// Symbols, one per effective definition:
//
//	create [or replace] function|procedure <schema>.<name>   sql.<schema>.<name>
//	create policy <name> on <schema>.<table>                 sql.policy.<schema>.<table>.<name>
//	create [or replace] trigger <name> ... on <schema>.<table> sql.trigger.<schema>.<table>.<name>
//	constraint <name> check (...) of a create table or an
//	  alter table ... add constraint                          sql.check.<schema>.<table>.<name>
//
// Tags are SQL comments directly above the statement:
//
//	-- @enforces billing.refunds:INV-2
//	create or replace function public.refund(...) ...
//
// pgTAP assertions are tests, <file>.test.<slug of the description>, tagged
// "-- @verifies <feature>:INV-N" (or brd.<id>:SC-N) above the assertion, or
// above select plan(...) for every assertion in the file.
package postgres

// @module-feature adapters.postgres

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Name is the adapter's language name.
const Name = "postgres"

// CodeTagDropped mirrors schema.CodeSQLTagDropped for adapter diagnostics.
const CodeTagDropped = schema.CodeSQLTagDropped

// Adapter parses PostgreSQL migrations and pgTAP tests.
type Adapter struct {
	migrations []string
	tests      []string
	// opts are the declared facts references are read with (SetHTTPOrigins).
	opts refOpts
}

// New builds the adapter. migrations and tests are slash-path globs relative
// to the code root. With none given, a file under a "migrations" directory is
// a migration and a file under a test directory (tests/, test/, ...) is a
// pgTAP test; any other .sql file is read as schema definitions.
func New(migrations, tests []string) *Adapter {
	return &Adapter{migrations: migrations, tests: tests}
}

func (a *Adapter) Name() string             { return Name }
func (a *Adapter) FileExtensions() []string { return []string{".sql"} }
func (a *Adapter) CanParse(p string) bool   { return strings.EqualFold(path.Ext(p), ".sql") }

func (a *Adapter) SCIPIndexerCommand(string, string) ([]string, error) {
	return nil, adapters.ErrIndexerNotConfigured
}

func (a *Adapter) MutationRunnerCommand(string, []string) ([]string, error) {
	return nil, adapters.ErrMutationRunnerNotConfigured
}

// RenderAnnotationSuggestion renders suggested tags as SQL comments.
func (a *Adapter) RenderAnnotationSuggestion(_ ir.Symbol, suggested []adapters.AnnotationSuggestion) (string, error) {
	var b strings.Builder
	for _, s := range suggested {
		tag, ok := annotationTags[s.Annotation]
		if !ok {
			tag = strings.ReplaceAll(s.Annotation, "_", "-")
		}
		args := make([]string, 0, len(s.Args))
		for _, x := range s.Args {
			args = append(args, fmt.Sprint(x))
		}
		fmt.Fprintf(&b, "-- @%s %s\n", tag, strings.Join(args, ", "))
	}
	return b.String(), nil
}

// tagKinds maps SQL comment tags to Lattice annotation kinds (the same tag
// names as the TypeScript adapter).
var tagKinds = map[string]string{
	"feature":                   "feature",
	"capability":                "capability",
	"enforces":                  "enforces_invariant",
	"verifies":                  "verifies",
	"verifies-capability":       "verifies_capability",
	"depends-on-feature":        "depends_on_feature",
	"role":                      "role",
	"suppresses":                "suppresses_invariant",
	"module-feature":            "module_feature",
	"module-enforces":           "module_enforces_invariant",
	"module-depends-on-feature": "module_depends_on_feature",
}

var annotationTags = func() map[string]string {
	m := map[string]string{}
	for tag, kind := range tagKinds {
		m[kind] = tag
	}
	return m
}()

// fileKind is what a .sql file is to the adapter.
type fileKind int

const (
	kindDefinitions fileKind = iota // schema definitions (e.g. a declarative schema)
	kindMigration                   // change history, applied in filename order
	kindTest                        // pgTAP
)

func (a *Adapter) classify(p string) fileKind {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if matchAny(a.tests, p) {
		return kindTest
	}
	if matchAny(a.migrations, p) {
		return kindMigration
	}
	if len(a.tests) == 0 && astutil.IsTestPath(p) {
		return kindTest
	}
	if len(a.migrations) == 0 {
		for _, seg := range strings.Split(p, "/") {
			if seg == "migrations" {
				return kindMigration
			}
		}
	}
	return kindDefinitions
}

func matchAny(globs []string, p string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, p); ok {
			return true
		}
	}
	return false
}

// Parse reads one .sql file into IR.
func (a *Adapter) Parse(_ context.Context, p string, source []byte) (ir.Module, error) {
	mod := ir.Module{File: p, Language: Name, RefsKnown: true}
	stmts := scan(string(source))
	switch a.classify(p) {
	case kindTest:
		parseTests(&mod, stmts)
	case kindMigration:
		mod.ChangeHistory = true
		parseDefinitions(&mod, stmts, a.opts)
	default:
		parseDefinitions(&mod, stmts, a.opts)
	}
	return mod, nil
}

// tags parses the @tags in a statement's attached comments. Module tags go
// to the module; the rest are returned for the statement.
func tags(mod *ir.Module, cs []comment) []ir.Annotation {
	var out []ir.Annotation
	for _, c := range cs {
		for i, raw := range strings.Split(c.text, "\n") {
			ln := strings.TrimSpace(raw)
			ln = strings.TrimPrefix(ln, "--")
			ln = strings.TrimPrefix(ln, "/*")
			ln = strings.TrimPrefix(ln, "*")
			ln = strings.TrimSuffix(ln, "*/")
			ln = strings.TrimSpace(ln)
			if !strings.HasPrefix(ln, "@") {
				continue
			}
			for _, seg := range strings.Split(ln, "@") {
				seg = strings.TrimSpace(seg)
				if seg == "" {
					continue
				}
				fields := strings.SplitN(seg, " ", 2)
				kind, ok := tagKinds[fields[0]]
				if !ok {
					continue
				}
				ann := ir.Annotation{Kind: kind, Line: c.line + i}
				if len(fields) == 2 {
					applyArgs(&ann, strings.TrimSpace(fields[1]))
				}
				if strings.HasPrefix(kind, "module_") {
					mod.ModuleAnnotations = append(mod.ModuleAnnotations, ann)
				} else {
					out = append(out, ann)
				}
			}
		}
	}
	return out
}

// applyArgs splits a tag's argument text, as the TypeScript adapter does.
func applyArgs(ann *ir.Annotation, text string) {
	if idx := strings.Index(text, "reason:"); idx >= 0 {
		ann.Kwargs = map[string]interface{}{"reason": strings.TrimSpace(text[idx+len("reason:"):])}
		text = strings.TrimSpace(text[:idx])
	}
	for _, part := range strings.Split(text, ",") {
		if v := strings.TrimSpace(part); v != "" {
			ann.Args = append(ann.Args, v)
		}
	}
}

const ident = `"(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$]*`
const qname = `(` + ident + `)(?:\s*\.\s*(` + ident + `))?`

var (
	reFunction    = regexp.MustCompile(`(?is)^create\s+(?:or\s+replace\s+)?(function|procedure)\s+` + qname + `\s*\(`)
	rePolicy      = regexp.MustCompile(`(?is)^create\s+policy\s+(` + ident + `)\s+on\s+` + qname)
	reTrigger     = regexp.MustCompile(`(?is)^create\s+(?:or\s+replace\s+)?(?:constraint\s+)?trigger\s+(` + ident + `)\s.*?\bon\s+` + qname)
	reAlterPolicy = regexp.MustCompile(`(?is)^alter\s+policy\s+(` + ident + `)\s+on\s+` + qname)
	reAlterFunc   = regexp.MustCompile(`(?is)^alter\s+(?:function|procedure|routine)\s+` + qname)
	reAlterTrig   = regexp.MustCompile(`(?is)^alter\s+trigger\s+(` + ident + `)\s+on\s+` + qname)
	reDropFunc    = regexp.MustCompile(`(?is)^drop\s+(?:function|procedure)\s+(?:if\s+exists\s+)?` + qname)
	reDropPolicy  = regexp.MustCompile(`(?is)^drop\s+policy\s+(?:if\s+exists\s+)?(` + ident + `)\s+on\s+` + qname)
	reDropTrigger = regexp.MustCompile(`(?is)^drop\s+trigger\s+(?:if\s+exists\s+)?(` + ident + `)\s+on\s+` + qname)
)

// dropKind marks a drop statement in ModuleAnnotations; Resolve consumes it.
const dropKind = "sql_drop"

// alterKind marks an ALTER statement in ModuleAnnotations; Resolve consumes
// it. Args[0] is the FQN of the object it names. Kwargs: "sets" (the
// ir.Alteration.Sets), "end_line", and for a rename "new_fqn" and "new_name".
const alterKind = "sql_alter"

// Alteration.Sets values.
const (
	setUsing     = "using"
	setWithCheck = "with_check"
	setTo        = "to"
	setRename    = "rename"
	setOther     = "other"
)

// unquote folds an identifier the way PostgreSQL does: quoted ones keep their
// case, unquoted ones are lower-cased.
func unquote(id string) string {
	if strings.HasPrefix(id, `"`) && strings.HasSuffix(id, `"`) && len(id) >= 2 {
		return strings.ReplaceAll(id[1:len(id)-1], `""`, `"`)
	}
	return strings.ToLower(id)
}

// qualify returns schema and name of a possibly qualified name; the default
// schema is public.
func qualify(first, second string) (string, string) {
	if second == "" {
		return "public", unquote(first)
	}
	return unquote(first), unquote(second)
}

// seg makes a name usable as one FQN segment: anything but letters, digits
// and underscores becomes an underscore (policy names often carry spaces).
func seg(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func functionFQN(schemaName, name string) string { return "sql." + seg(schemaName) + "." + seg(name) }
func policyFQN(schemaName, table, name string) string {
	return "sql.policy." + seg(schemaName) + "." + seg(table) + "." + seg(name)
}
func triggerFQN(schemaName, table, name string) string {
	return "sql.trigger." + seg(schemaName) + "." + seg(table) + "." + seg(name)
}

// parseDefinitions records functions, policies and triggers, and marks drops.
func parseDefinitions(mod *ir.Module, stmts []statement, o refOpts) {
	var applied []ir.Ref
	appliedLine := 0
	defer func() {
		if len(applied) > 0 {
			mod.Nodes = append(mod.Nodes, appliedNode(mod.File, appliedLine, applied))
		}
	}()
	for _, st := range stmts {
		anns := tags(mod, attached(st))
		var sym *ir.Symbol
		kind, tSchema, table := "function", "", ""
		switch {
		case reFunction.MatchString(st.text):
			m := reFunction.FindStringSubmatch(st.text)
			s, n := qualify(m[2], m[3])
			sym = &ir.Symbol{Name: s + "." + n, FQN: functionFQN(s, n)}
			sym.Execute = headerExecute(mod.File, st, strings.ToLower(m[1]))
			scanDynamicPriv(mod, st)
			scanDynamicTableGrant(mod, st)
			scanDynamicRLS(mod, st)
		case rePolicy.MatchString(st.text):
			m := rePolicy.FindStringSubmatch(st.text)
			s, t := qualify(m[2], m[3])
			name := unquote(m[1])
			sym = &ir.Symbol{Name: "policy " + name + " on " + s + "." + t, FQN: policyFQN(s, t, name)}
			kind, tSchema, table = "policy", s, t
			sym.Guard = policyGuard(st.text, s, t)
		case reTrigger.MatchString(st.text):
			m := reTrigger.FindStringSubmatch(st.text)
			s, t := qualify(m[2], m[3])
			name := unquote(m[1])
			sym = &ir.Symbol{Name: "trigger " + name + " on " + s + "." + t, FQN: triggerFQN(s, t, name)}
			kind, tSchema, table = "trigger", s, t
			sym.Guard = triggerGuard(st.text, s, t)
		case reAlterPolicy.MatchString(st.text):
			m := reAlterPolicy.FindStringSubmatch(st.text)
			s, t := qualify(m[2], m[3])
			rest := st.text[len(m[0]):]
			markAlter(mod, st, policyFQN(s, t, unquote(m[1])), rest, func(n string) (string, string) {
				return policyFQN(s, t, n), "policy " + n + " on " + s + "." + t
			}, true)
		case reAlterFunc.MatchString(st.text):
			m := reAlterFunc.FindStringSubmatch(st.text)
			s, n := qualify(m[1], m[2])
			markAlter(mod, st, functionFQN(s, n), st.text[len(m[0]):], func(n string) (string, string) {
				return functionFQN(s, n), s + "." + n
			}, false)
			alterExec(mod, st, len(m[0]))
		case reAlterTrig.MatchString(st.text):
			m := reAlterTrig.FindStringSubmatch(st.text)
			s, t := qualify(m[2], m[3])
			markAlter(mod, st, triggerFQN(s, t, unquote(m[1])), st.text[len(m[0]):], func(n string) (string, string) {
				return triggerFQN(s, t, n), "trigger " + n + " on " + s + "." + t
			}, false)
		case reDropFunc.MatchString(st.text):
			m := reDropFunc.FindStringSubmatch(st.text)
			s, n := qualify(m[1], m[2])
			markDrop(mod, functionFQN(s, n), st.line)
		case reDropPolicy.MatchString(st.text):
			m := reDropPolicy.FindStringSubmatch(st.text)
			s, t := qualify(m[2], m[3])
			markDrop(mod, policyFQN(s, t, unquote(m[1])), st.line)
		case reDropTrigger.MatchString(st.text):
			m := reDropTrigger.FindStringSubmatch(st.text)
			s, t := qualify(m[2], m[3])
			markDrop(mod, triggerFQN(s, t, unquote(m[1])), st.line)
		}
		if sym == nil {
			readPrivileges(mod, st)
			readTables(mod, st)
			if !readTableGrants(mod, st) {
				scanDynamicTableGrant(mod, st)
			}
			scanDynamicRLS(mod, st)
			if mod.ChangeHistory {
				readMigrationWrites(mod, st)
			}
			used := parseConstraints(mod, st, anns)
			if n, ok := cronNode(mod, st, o); ok {
				mod.Nodes = append(mod.Nodes, n)
			} else if refs := appliedRefs(st, o); len(refs) > 0 {
				if appliedLine == 0 {
					appliedLine = st.line
				}
				applied = append(applied, refs...)
			}
			if !used {
				orphan(mod, anns, st)
			}
			continue
		}
		// Policies and triggers are guards like functions: each rejects or
		// rewrites the violating case, so they share the function kind.
		sym.Kind, sym.File, sym.Line, sym.EndLine = ir.KindFunction, mod.File, st.line, st.endLine
		sym.Exported, sym.Annotations = true, withImpliedFeature(anns)
		if sym.Execute != nil {
			readFunctionWrites(mod, sym, st)
		}
		linkRefs(mod, sym, st, kind, tSchema, table, o)
		mod.Symbols = append(mod.Symbols, *sym)
	}
}

// withImpliedFeature attributes a symbol to the feature its @enforces refs
// name ("refunds:INV-1" names refunds) when it has no @feature: a migration
// spans features, so its symbols carry their own attribution. Refs to BRD
// criteria (brd.<id>:SC-N) and refs naming several features imply nothing.
// @enforces adapters.postgres:INV-2
func withImpliedFeature(anns []ir.Annotation) []ir.Annotation {
	feature := ""
	for _, a := range anns {
		switch a.Kind {
		case "feature":
			return anns
		case "enforces_invariant":
			for _, x := range a.Args {
				ref := fmt.Sprint(x)
				i := strings.Index(ref, ":")
				if i <= 0 || strings.HasPrefix(ref, "brd.") {
					continue
				}
				if feature != "" && feature != ref[:i] {
					return anns
				}
				feature = ref[:i]
			}
		}
	}
	if feature == "" {
		return anns
	}
	return append(anns, ir.Annotation{Kind: "feature", Args: []interface{}{feature},
		Kwargs: map[string]interface{}{"implied": true}, Line: anns[0].Line})
}

func markDrop(mod *ir.Module, fqn string, line int) {
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: dropKind, Args: []interface{}{fqn}, Line: line})
}

// markAlter records an ALTER statement for Resolve. rest is the statement
// after the object's name; renamed gives the FQN and symbol name of the object
// under a new name; policy says whether USING, WITH CHECK and TO are read.
func markAlter(mod *ir.Module, st statement, fqn, rest string, renamed func(string) (string, string), policy bool) {
	sets, newName := alterSets(rest, policy)
	kw := map[string]interface{}{"sets": sets, "end_line": st.endLine}
	if newName != "" {
		kw["new_fqn"], kw["new_name"] = renamed(newName)
	}
	mod.ModuleAnnotations = append(mod.ModuleAnnotations,
		ir.Annotation{Kind: alterKind, Args: []interface{}{fqn}, Kwargs: kw, Line: st.line})
}

// alterSets reads what an ALTER statement changes from the text after the
// object's name, looking only outside parentheses so a "to" or "using" inside
// an expression is never taken for a clause. A rename returns the new name,
// folded as PostgreSQL folds it.
func alterSets(rest string, policy bool) (sets []string, newName string) {
	toks := topTokens(rest)
	for i := 0; i < len(toks); i++ {
		next := func(k int) string {
			if i+k < len(toks) {
				return toks[i+k]
			}
			return ""
		}
		switch t := strings.ToLower(toks[i]); {
		case t == "rename" && strings.EqualFold(next(1), "to") && next(2) != "" && next(2) != "(":
			newName = unquote(next(2))
			sets = append(sets, setRename)
			i += 2
		case policy && t == "to":
			sets = append(sets, setTo)
		case policy && t == "using" && next(1) == "(":
			sets = append(sets, setUsing)
		case policy && t == "with" && strings.EqualFold(next(1), "check") && next(2) == "(":
			sets = append(sets, setWithCheck)
		}
	}
	if len(sets) == 0 {
		sets = []string{setOther}
	}
	return sets, newName
}

// topTokens splits SQL into the words, quoted identifiers and "(" that lie
// outside any parentheses; whatever is inside a parenthesis is skipped, and
// so are strings and dollar-quoted bodies.
func topTokens(s string) []string {
	var out []string
	depth := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '(':
			if depth == 0 {
				out = append(out, "(")
			}
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(s) {
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j < len(s) {
				j++
			}
			if c == '"' && depth == 0 {
				out = append(out, s[i:j])
			}
			i = j
		case c == '$':
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			if j < len(s) && s[j] == '$' {
				tag := s[i : j+1]
				if k := strings.Index(s[j+1:], tag); k >= 0 {
					i = j + 1 + k + len(tag)
					continue
				}
			}
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80:
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] == '$' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9' || s[j] >= 0x80) {
				j++
			}
			if depth == 0 {
				out = append(out, s[i:j])
			}
			i = j
		default:
			i++
		}
	}
	return out
}

// orphan reports tags above a statement that declares nothing Lattice reads.
func orphan(mod *ir.Module, anns []ir.Annotation, st statement) {
	if len(anns) == 0 {
		return
	}
	kinds := make([]string, 0, len(anns))
	for _, a := range anns {
		kinds = append(kinds, "@"+annotationTags[a.Kind])
	}
	head := strings.Fields(st.text)
	if len(head) > 3 {
		head = head[:3]
	}
	mod.Diagnostics = append(mod.Diagnostics, ir.Diagnostic{
		Line: anns[0].Line, Code: schema.CodeAnnotationOrphaned,
		Message: fmt.Sprintf("%s attaches to no symbol: it is followed by %q, which is not a function, policy, trigger or named CHECK constraint",
			strings.Join(kinds, ", "), strings.Join(head, " ")),
	})
}

// orphanAt is orphan for a tag inside a statement: want says what it could
// have bound to.
func orphanAt(mod *ir.Module, anns []ir.Annotation, want string) {
	kinds := make([]string, 0, len(anns))
	for _, a := range anns {
		kinds = append(kinds, "@"+annotationTags[a.Kind])
	}
	mod.Diagnostics = append(mod.Diagnostics, ir.Diagnostic{
		Line: anns[0].Line, Code: schema.CodeAnnotationOrphaned,
		Message: fmt.Sprintf("%s attaches to no symbol: it would have to sit directly above %s", strings.Join(kinds, ", "), want),
	})
}

// Resolve applies migrations in filename order (definition files first, then
// migrations, and within a file in statement order): the last create wins, a
// drop removes the symbol, and tags count only on the effective definition. A
// redefinition that loses tags an earlier definition carried raises
// SQL_TAG_DROPPED on the effective one, because a reader of the older file
// would believe the rule still holds.
//
// An ALTER POLICY, ALTER FUNCTION or ALTER TRIGGER that follows the effective
// definition is recorded on that symbol (Symbol.Altered), so a consumer knows
// which statement sets the object's current expressions and whether anything
// touched it after its CREATE. A rename gives the symbol its new name
// (Symbol.DeclaredAs keeps the one its CREATE used); a later CREATE or a drop
// discards the alterations that came before it. An ALTER of an object no
// definition in the scanned files created is ignored.
// @enforces adapters.postgres:INV-1
// @enforces adapters.postgres:INV-3
func (a *Adapter) Resolve(mods []ir.Module) []ir.Module {
	order := make([]int, 0, len(mods))
	for i := range mods {
		order = append(order, i)
	}
	sort.SliceStable(order, func(x, y int) bool {
		mx, my := mods[order[x]], mods[order[y]]
		if mx.ChangeHistory != my.ChangeHistory {
			return !mx.ChangeHistory
		}
		return mx.File < my.File
	})

	type def struct{ mod, sym int }
	type event struct {
		line  int
		drop  string
		alter *ir.Annotation
		priv  *ir.Annotation
		tbl   *ir.Annotation
		sym   int
	}
	fold := newExecFold()
	tfold := newTableFold()
	var pending []pendingGap
	effective := map[string]def{}
	removed := map[def]bool{}
	for _, mi := range order {
		m := &mods[mi]
		var events []event
		for si, s := range m.Symbols {
			if !s.IsTest {
				events = append(events, event{line: s.Line, sym: si})
			}
		}
		kept := m.ModuleAnnotations[:0]
		for ai, an := range m.ModuleAnnotations {
			if an.Kind == dropKind {
				if len(an.Args) > 0 {
					events = append(events, event{line: an.Line, drop: fmt.Sprint(an.Args[0]), sym: -1})
				}
				continue
			}
			if an.Kind == privKind || an.Kind == privUnknownKind || an.Kind == memberKind || an.Kind == memberUnknown || an.Kind == inheritKind {
				a := m.ModuleAnnotations[ai]
				events = append(events, event{line: an.Line, priv: &a, sym: -1})
				continue
			}
			switch an.Kind {
			case tableKind, tgrantKind, tgrantUnknownKind, tdefaultKind:
				a := m.ModuleAnnotations[ai]
				events = append(events, event{line: an.Line, tbl: &a, sym: -1})
				continue
			case gapKind:
				pending = append(pending, pendingGap{mod: mi, an: an})
				continue
			}
			if an.Kind == alterKind {
				if len(an.Args) > 0 {
					a := m.ModuleAnnotations[ai]
					events = append(events, event{line: an.Line, alter: &a, sym: -1})
				}
				continue
			}
			kept = append(kept, an)
		}
		m.ModuleAnnotations = kept
		sort.SliceStable(events, func(x, y int) bool { return events[x].line < events[y].line })

		for _, e := range events {
			if e.priv != nil {
				fold.apply(*e.priv, m.File)
				continue
			}
			if e.tbl != nil {
				tfold.applyTableEvent(*e.tbl, m.File)
				continue
			}
			if e.alter != nil {
				fqn := fmt.Sprint(e.alter.Args[0])
				cur, ok := effective[fqn]
				if !ok {
					continue
				}
				fold.alter(fqn, *e.alter, m.File)
				sym := &mods[cur.mod].Symbols[cur.sym]
				endLine, _ := e.alter.Kwargs["end_line"].(int)
				sets, _ := e.alter.Kwargs["sets"].([]string)
				sym.Altered = append(sym.Altered, ir.Alteration{File: m.File, Line: e.line, EndLine: endLine, Sets: sets})
				newFQN, _ := e.alter.Kwargs["new_fqn"].(string)
				if _, taken := effective[newFQN]; newFQN != "" && newFQN != fqn && !taken {
					if sym.DeclaredAs == "" {
						sym.DeclaredAs = sym.Name
					}
					sym.Name, sym.FQN = fmt.Sprint(e.alter.Kwargs["new_name"]), newFQN
					delete(effective, fqn)
					effective[newFQN] = cur
					fold.rename(fqn, newFQN)
				}
				continue
			}
			if e.drop != "" {
				if old, ok := effective[e.drop]; ok {
					removed[old] = true
					delete(effective, e.drop)
				}
				fold.drop(e.drop)
				continue
			}
			s := m.Symbols[e.sym]
			if old, ok := effective[s.FQN]; ok {
				removed[old] = true
				prev := mods[old.mod].Symbols[old.sym]
				if lost := lostTags(prev.Annotations, s.Annotations); len(lost) > 0 {
					m.Diagnostics = append(m.Diagnostics, ir.Diagnostic{
						Line: s.Line, Code: CodeTagDropped,
						Message: fmt.Sprintf("%s is redefined here without %s, which its definition at %s:%d carried; tags count only on the effective definition",
							s.Name, strings.Join(lost, ", "), prev.File, prev.Line),
					})
				}
			}
			effective[s.FQN] = def{mi, e.sym}
			if s.Execute != nil {
				fold.create(s.FQN, strings.SplitN(s.Name, ".", 2)[0], s)
			}
		}
	}
	if ms := fold.memberships(); len(ms) > 0 && len(order) > 0 {
		mods[order[0]].Memberships = ms
	}
	for fqn, d := range effective {
		if sym := &mods[d.mod].Symbols[d.sym]; sym.Execute != nil {
			sym.Execute = fold.final(fqn)
		}
	}
	eff := make(map[string][2]int, len(effective))
	for fqn, d := range effective {
		eff[fqn] = [2]int{d.mod, d.sym}
	}
	resolveWrites(mods, order, eff, pending, tfold)

	for mi := range mods {
		syms := mods[mi].Symbols[:0]
		for si, s := range mods[mi].Symbols {
			if !removed[def{mi, si}] {
				syms = append(syms, s)
			}
		}
		mods[mi].Symbols = syms
	}
	resolveCron(mods, order)
	return mods
}

// resolveCron keeps one cron job per name across files, in the order
// migrations apply: the last cron.schedule of a job wins, and
// cron.unschedule removes it.
func resolveCron(mods []ir.Module, order []int) {
	type at struct{ mod, node int }
	effective := map[string]at{}
	for _, mi := range order {
		m := &mods[mi]
		type event struct {
			line   int
			node   int
			unsche string
		}
		var events []event
		for ni, n := range m.Nodes {
			if n.Kind == ir.NodeCron {
				events = append(events, event{line: n.Line, node: ni})
			}
		}
		kept := m.ModuleAnnotations[:0]
		for _, an := range m.ModuleAnnotations {
			if an.Kind == cronUnscheduleKind {
				if len(an.Args) > 0 {
					events = append(events, event{line: an.Line, node: -1, unsche: fmt.Sprint(an.Args[0])})
				}
				continue
			}
			kept = append(kept, an)
		}
		m.ModuleAnnotations = kept
		sort.SliceStable(events, func(x, y int) bool { return events[x].line < events[y].line })
		for _, e := range events {
			if e.node < 0 {
				delete(effective, e.unsche)
				continue
			}
			effective[m.Nodes[e.node].FQN] = at{mi, e.node}
		}
	}
	for mi := range mods {
		nodes := mods[mi].Nodes[:0]
		for ni, n := range mods[mi].Nodes {
			if e, ok := effective[n.FQN]; n.Kind != ir.NodeCron || ok && e == (at{mi, ni}) {
				nodes = append(nodes, n)
			}
		}
		mods[mi].Nodes = nodes
	}
}

// lostTags lists the tags on an earlier definition that a redefinition lacks.
func lostTags(prev, next []ir.Annotation) []string {
	have := map[string]bool{}
	for _, a := range next {
		have[annKey(a)] = true
	}
	var lost []string
	for _, a := range prev {
		if a.Kwargs["implied"] == true {
			continue // follows from a tag listed on its own
		}
		if !have[annKey(a)] {
			lost = append(lost, "@"+annotationTags[a.Kind]+" "+argText(a))
		}
	}
	return lost
}

func annKey(a ir.Annotation) string { return a.Kind + " " + argText(a) }

func argText(a ir.Annotation) string {
	parts := make([]string, 0, len(a.Args))
	for _, x := range a.Args {
		parts = append(parts, fmt.Sprint(x))
	}
	return strings.Join(parts, ", ")
}
