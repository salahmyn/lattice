package postgres

// @module-feature adapters.postgres

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// EXECUTE privileges. A SQL function can be called by any role that holds
// EXECUTE on it, so the adapter reads who does from the statements that decide
// it: GRANT and REVOKE on a function, on all functions of a schema, ALTER
// DEFAULT PRIVILEGES for functions created later, and SECURITY DEFINER or
// INVOKER on the function itself. It records each role a statement names, and
// PUBLIC, with the file and line of the deciding statement (ir.Execute); a
// role no statement names, and the platform's own defaults, are resolved by
// the core from configuration.
//
// A statement the adapter cannot resolve to a function leaves it unknown,
// never closed: a GRANT or REVOKE inside a DO block, a function body or an
// EXECUTE string, an overloaded name, an ALTER DEFAULT PRIVILEGES FOR ROLE,
// a GRANT or REVOKE on functions the parser could not read.

// Annotation kinds for the privilege events Resolve consumes.
const (
	privKind        = "sql_priv"         // a GRANT, REVOKE or ALTER DEFAULT PRIVILEGES it understood
	privUnknownKind = "sql_priv_unknown" // one it could not resolve to the functions it changes
	memberKind      = "sql_member"       // GRANT role TO role, REVOKE role FROM role, CREATE ROLE ... IN ROLE
	memberUnknown   = "sql_member_unknown"
	inheritKind     = "sql_role_inherit" // a role created or altered INHERIT or NOINHERIT
)

// Targets of a privilege event.
const (
	targetNames   = "names"   // ON FUNCTION a, b
	targetAll     = "all"     // ON ALL FUNCTIONS IN SCHEMA s
	targetDefault = "default" // ALTER DEFAULT PRIVILEGES
)

var (
	reCreateRole = regexp.MustCompile(`(?is)^create\s+(?:role|user|group)\s+(` + ident + `)\b`)
	reAlterRole  = regexp.MustCompile(`(?is)^alter\s+(?:role|user|group)\s+(` + ident + `)\s`)
	// reDynamicMember finds a role GRANT or REVOKE (one with no ON) inside a
	// statement that is not one, and reDynamicRole a CREATE ROLE ... IN ROLE.
	reDynamicMember = regexp.MustCompile(`(?is)\b(?:grant|revoke)\b([^;]{0,300})`)
	reOn            = regexp.MustCompile(`(?i)\bon\b`)
	reToFrom        = regexp.MustCompile(`(?is)\b(?:to|from)\s+(` + ident + `(?:\s*,\s*(?:` + ident + `))*)`)
	reDynamicRole   = regexp.MustCompile(`(?is)\bcreate\s+(?:role|user|group)\s+(` + ident + `)[^;]{0,300}?\bin\s+(?:role|group)\b`)
	// reDynamicGrantee finds a CREATE ROLE x ... ROLE|USER|ADMIN m, which
	// makes m a member of x: the member is only known by reading the clause,
	// so inside dynamic text it makes every role unknown.
	reDynamicGrantee = regexp.MustCompile(`(?is)\bcreate\s+(?:role|user|group)\s+(?:` + ident + `)\b[^;]{0,300}?(?:^|[^\w])(?:role|user|admin)\s+(?:` + ident + `)`)
	reSecurity       = regexp.MustCompile(`(?i)\bsecurity\s+(definer|invoker)\b`)
	reSetSchema      = regexp.MustCompile(`(?i)\bset\s+schema\b`)
	// reDynamicPriv finds a GRANT or REVOKE on functions inside a statement
	// that is not one itself (a DO block, a function body, an EXECUTE string).
	reDynamicPriv = regexp.MustCompile(`(?is)\b(?:grant|revoke)\b[^;]{0,400}?\bon\s+(all\s+)?(function|procedure|routine)s?\b`)
	// reInRoleOnly matches a clause whose ROLE keyword is only the one of IN ROLE.
	reInRoleOnly = regexp.MustCompile(`(?is)\bin\s+(?:role|group)\s+(?:` + ident + `)\s*$`)
	reDynSchema  = regexp.MustCompile(`(?is)^\s+in\s+schema\s+(` + ident + `)\b`)
)

// blankNested blanks what lies inside parentheses, strings, quoted identifiers
// and dollar-quoted bodies, keeping every newline and the length, so a regex
// over the result sees only top-level words at their true offsets.
func blankNested(s string) string {
	b := []byte(s)
	depth := 0
	blank := func(from, to int) {
		for k := from; k < to && k < len(b); k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '(':
			depth++
			blank(i, i+1)
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			blank(i, i+1)
			i++
		case c == '\'' || c == '"' || c == '$':
			ln := 0
			j := skipToken(s, i, &ln)
			if j > i+1 || c != '$' {
				blank(i, j)
				i = j
				continue
			}
			if depth > 0 {
				blank(i, i+1)
			}
			i++
		default:
			if depth > 0 {
				blank(i, i+1)
			}
			i++
		}
	}
	return string(b)
}

// privTokens splits a statement into words, quoted identifiers, "," and "."
// and one "(" for each parenthesised group, whose content is skipped, as are
// strings and dollar-quoted bodies.
func privTokens(s string) []string {
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
		case c == '\'' || c == '"' || c == '$':
			ln := 0
			j := skipToken(s, i, &ln)
			if j == i+1 { // a lone "$": not a body
				i++
				continue
			}
			if c == '"' && depth == 0 {
				out = append(out, s[i:j])
			}
			i = j
		case c == ',' || c == '.':
			if depth == 0 {
				out = append(out, string(c))
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

// tokens is a cursor over privTokens.
type tokens struct {
	t []string
	i int
}

func (k *tokens) done() bool { return k.i >= len(k.t) }
func (k *tokens) peek() string {
	if k.i < len(k.t) {
		return k.t[k.i]
	}
	return ""
}
func (k *tokens) is(words ...string) bool {
	for n, w := range words {
		if k.i+n >= len(k.t) || !strings.EqualFold(k.t[k.i+n], w) {
			return false
		}
	}
	return true
}
func (k *tokens) take(words ...string) bool {
	if k.is(words...) {
		k.i += len(words)
		return true
	}
	return false
}

// qualified reads name or schema.name, and skips an argument list.
func (k *tokens) qualified() (schema, name string, ok bool) {
	first := k.peek()
	if first == "" || first == "," || first == "." || first == "(" {
		return "", "", false
	}
	k.i++
	schema, name = qualify(first, "")
	if k.peek() == "." {
		k.i++
		second := k.peek()
		if second == "" || second == "," || second == "." || second == "(" {
			return "", "", false
		}
		k.i++
		schema, name = qualify(first, second)
	}
	if k.peek() == "(" {
		k.i++
	}
	return schema, name, true
}

// privStmt is a parsed GRANT, REVOKE or ALTER DEFAULT PRIVILEGES on functions.
type privStmt struct {
	verb       string // grant or revoke
	target     string // targetNames, targetAll or targetDefault
	names      []string
	schemas    []string
	routine    string // function, procedure or routine
	roles      []string
	forRole    bool // ALTER DEFAULT PRIVILEGES FOR ROLE|USER
	optionOnly bool // REVOKE GRANT OPTION FOR: takes no privilege away
	// For privMember: roles, members (the grantees), the option a REVOKE
	// ... OPTION FOR names, and whether WITH INHERIT FALSE was given.
	members   []string
	optionFor string
	noInherit bool
}

type privResult int

const (
	privNone   privResult = iota // not a GRANT, REVOKE or ALTER DEFAULT PRIVILEGES
	privOther                    // one on something other than functions
	privOK                       // one on functions, read
	privFail                     // one on functions the parser could not read
	privMember                   // GRANT or REVOKE of role membership
)

// readRoles reads a role list up to the end or a trailing clause.
func readRoles(k *tokens) ([]string, bool) {
	var roles []string
	for {
		k.take("group")
		t := k.peek()
		if t == "" || t == "," || t == "." || t == "(" {
			return nil, false
		}
		k.i++
		switch low := strings.ToLower(t); {
		case t[0] != '"' && low == "public":
			roles = append(roles, ir.ExecPublic)
		case t[0] != '"' && (low == "current_user" || low == "session_user" || low == "current_role"):
			// The role the migration runs as: not a client role.
		default:
			roles = append(roles, unquote(t))
		}
		if k.take(",") {
			continue
		}
		return roles, true
	}
}

// readPrivs reads the privilege list up to ON and says whether it can
// include EXECUTE (EXECUTE or ALL [PRIVILEGES]).
func readPrivs(k *tokens) (execute, ok bool) {
	for !k.done() && !k.is("on") {
		switch strings.ToLower(k.peek()) {
		case "execute", "all":
			execute = true
		}
		k.i++
	}
	return execute, k.take("on")
}

// parsePriv reads one statement.
func parsePriv(text string) (privStmt, privResult) {
	k := &tokens{t: privTokens(text)}
	switch {
	case k.is("alter", "default", "privileges"):
		k.i += 3
		return parseDefaultPriv(k)
	case k.is("grant") || k.is("revoke"):
	default:
		return privStmt{}, privNone
	}
	var p privStmt
	p.verb = strings.ToLower(k.peek())
	k.i++
	if k.take("grant", "option", "for") {
		p.optionOnly = p.verb == "revoke"
	}
	for _, o := range []string{"admin", "inherit", "set"} {
		if k.take(o, "option", "for") {
			p.optionFor = o
		}
	}
	if !hasOn(k) {
		return parseMembership(k, p)
	}
	execute, ok := readPrivs(k)
	if !ok {
		return p, privOther
	}
	switch {
	case k.is("function") || k.is("procedure") || k.is("routine"):
		p.target, p.routine = targetNames, strings.ToLower(k.peek())
		k.i++
		for {
			s, n, ok := k.qualified()
			if !ok {
				return p, privFail
			}
			p.names = append(p.names, functionFQN(s, n))
			if k.take(",") {
				continue
			}
			break
		}
	case k.is("all") && (k.is("all", "functions") || k.is("all", "procedures") || k.is("all", "routines")):
		p.target = targetAll
		p.routine = strings.TrimSuffix(strings.ToLower(k.t[k.i+1]), "s")
		k.i += 2
		if !k.take("in", "schema") {
			return p, privFail
		}
		for {
			t := k.peek()
			if t == "" || t == "," || t == "." || t == "(" {
				return p, privFail
			}
			k.i++
			p.schemas = append(p.schemas, unquote(t))
			if k.take(",") {
				continue
			}
			break
		}
	default:
		return p, privOther
	}
	if !execute {
		return p, privOther
	}
	if !k.take("to") && !k.take("from") {
		return p, privFail
	}
	roles, ok := readRoles(k)
	if !ok {
		return p, privFail
	}
	p.roles = roles
	return p, privOK
}

func hasOn(k *tokens) bool {
	for _, t := range k.t[k.i:] {
		if strings.EqualFold(t, "on") {
			return true
		}
	}
	return false
}

// parseMembership reads what follows GRANT or REVOKE when no ON is there:
// role [, ...] TO|FROM role [, ...] and its trailing options.
func parseMembership(k *tokens, p privStmt) (privStmt, privResult) {
	p.target = "member"
	roles, ok := readRoles(k)
	if !ok {
		return p, privFail
	}
	if !k.take("to") && !k.take("from") {
		return p, privFail
	}
	members, ok := readRoles(k)
	if !ok {
		return p, privFail
	}
	p.roles, p.members = roles, members
	for i := k.i; i+1 < len(k.t); i++ {
		if strings.EqualFold(k.t[i], "inherit") && strings.EqualFold(k.t[i+1], "false") {
			p.noInherit = true
		}
	}
	return p, privMember
}

// readCreateRole reads CREATE ROLE x ... IN ROLE a, b ROLE m, n: x is a
// member of a and b, and m and n are members of x.
func readCreateRole(mod *ir.Module, st statement, name string) {
	k := &tokens{t: privTokens(st.text)}
	k.i = 3
	var in, members []string
	list := func() []string {
		var out []string
		for {
			t := k.peek()
			if t == "" || t == "," || t == "." || t == "(" {
				return out
			}
			k.i++
			out = append(out, unquote(t))
			if !k.take(",") {
				return out
			}
		}
	}
	x := unquote(name)
	for !k.done() {
		switch {
		case k.take("in", "role") || k.take("in", "group"):
			in = append(in, list()...)
		case k.take("role") || k.take("user") || k.take("admin"):
			members = append(members, list()...)
		case k.take("noinherit"):
			roleInherit(mod, st, x, false)
		default:
			k.i++
		}
	}
	if len(in) > 0 {
		memberEvent(mod, st, privStmt{verb: "grant", roles: in, members: []string{x}})
	}
	if len(members) > 0 {
		memberEvent(mod, st, privStmt{verb: "grant", roles: []string{x}, members: members})
	}
}

func roleInherit(mod *ir.Module, st statement, role string, inherit bool) {
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: inheritKind, Line: st.line,
		Kwargs: map[string]interface{}{"role": role, "inherit": inherit}})
}

func memberEvent(mod *ir.Module, st statement, p privStmt) {
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: memberKind, Line: st.line,
		Kwargs: map[string]interface{}{"verb": p.verb, "roles": p.roles, "members": p.members,
			"option_for": p.optionFor, "no_inherit": p.noInherit}})
}

// memberUnknownEvent records a membership statement the adapter could not
// resolve: members it named (unknown for any role), or all=true when it named
// none (every role is unknown).
func memberUnknownEvent(mod *ir.Module, st statement, members []string, all bool, reason string) {
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: memberUnknown, Line: st.line,
		Kwargs: map[string]interface{}{"members": members, "all": all, "reason": reason}})
}

// parseDefaultPriv reads what follows ALTER DEFAULT PRIVILEGES.
func parseDefaultPriv(k *tokens) (privStmt, privResult) {
	p := privStmt{target: targetDefault}
	for {
		switch {
		case k.take("for", "role") || k.take("for", "user"):
			p.forRole = true
			for !k.done() && !k.is("in") && !k.is("grant") && !k.is("revoke") {
				k.i++
			}
		case k.take("in", "schema"):
			for !k.done() && !k.is("for") && !k.is("grant") && !k.is("revoke") {
				if t := k.peek(); t != "," {
					p.schemas = append(p.schemas, unquote(t))
				}
				k.i++
			}
		default:
			if !k.is("grant") && !k.is("revoke") {
				return p, privFail
			}
			p.verb = strings.ToLower(k.peek())
			k.i++
			if k.take("grant", "option", "for") {
				p.optionOnly = p.verb == "revoke"
			}
			execute, ok := readPrivs(k)
			if !ok {
				return p, privFail
			}
			if !k.take("functions") && !k.take("routines") {
				return p, privOther // tables, sequences, types, schemas
			}
			if !execute {
				return p, privOther
			}
			if !k.take("to") && !k.take("from") {
				return p, privFail
			}
			roles, ok := readRoles(k)
			if !ok {
				return p, privFail
			}
			p.roles = roles
			return p, privOK
		}
	}
}

// privEvent records a statement for Resolve.
func privEvent(mod *ir.Module, st statement, p privStmt) {
	kw := map[string]interface{}{
		"verb": p.verb, "target": p.target, "names": p.names, "schemas": p.schemas,
		"routine": p.routine, "roles": p.roles, "for_role": p.forRole,
	}
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: privKind, Kwargs: kw, Line: st.line})
}

// privUnknown records a statement that may change the privileges of
// functions the adapter cannot name: names and schemas it did read, and
// all=true when it could name none (then every function defined before it is
// unknown).
func privUnknown(mod *ir.Module, st statement, names, schemas []string, all bool, reason string) {
	mod.ModuleAnnotations = append(mod.ModuleAnnotations, ir.Annotation{Kind: privUnknownKind, Line: st.line,
		Kwargs: map[string]interface{}{"names": names, "schemas": schemas, "all": all, "reason": reason}})
}

// readPrivileges reads a statement for its effect on EXECUTE privileges and
// reports whether it is a GRANT, REVOKE or ALTER DEFAULT PRIVILEGES.
func readPrivileges(mod *ir.Module, st statement) bool {
	p, res := parsePriv(st.text)
	switch res {
	case privNone:
		if m := reCreateRole.FindStringSubmatch(st.text); m != nil {
			readCreateRole(mod, st, m[1])
			return false
		}
		if m := reAlterRole.FindStringSubmatch(st.text); m != nil {
			for _, t := range privTokens(st.text[len(m[0]):]) {
				switch strings.ToLower(t) {
				case "noinherit":
					roleInherit(mod, st, unquote(m[1]), false)
				case "inherit":
					roleInherit(mod, st, unquote(m[1]), true)
				}
			}
			return false
		}
		scanDynamicPriv(mod, st)
		return false
	case privMember:
		// REVOKE ADMIN|SET OPTION FOR leaves the membership as it is.
		if p.optionFor != "admin" && p.optionFor != "set" {
			memberEvent(mod, st, p)
		}
	case privOK:
		if !p.optionOnly {
			privEvent(mod, st, p)
		}
	case privFail:
		privUnknown(mod, st, nil, nil, true,
			fmt.Sprintf("the GRANT or REVOKE at %s:%d could not be read", mod.File, st.line))
		memberUnknownEvent(mod, st, nil, true,
			fmt.Sprintf("the GRANT or REVOKE at %s:%d could not be read", mod.File, st.line))
	}
	return true
}

// scanDynamicPriv finds a GRANT or REVOKE on functions inside a statement
// that is not one: a DO block, a function body, an EXECUTE string. One that
// names its function literally leaves that function unknown; one that names
// nothing literal leaves every function defined before it unknown.
func scanDynamicPriv(mod *ir.Module, st statement) {
	text := st.text
	var names, schemas []string
	all := false
	for _, loc := range reDynamicPriv.FindAllStringSubmatchIndex(text, -1) {
		after := text[loc[1]:]
		if loc[2] >= 0 { // ALL FUNCTIONS IN SCHEMA s
			if m := reDynSchema.FindStringSubmatch(after); m != nil {
				schemas = append(schemas, unquote(m[1]))
			} else {
				all = true
			}
			continue
		}
		if q := reFuncName.FindStringSubmatch(strings.TrimSpace(after)); q != nil {
			s, n := qualify(q[1], q[2])
			names = append(names, functionFQN(s, n))
			continue
		}
		all = true
	}
	scanDynamicMember(mod, st)
	if len(names) == 0 && len(schemas) == 0 && !all {
		return
	}
	privUnknown(mod, st, names, schemas, all,
		fmt.Sprintf("a GRANT or REVOKE inside the statement at %s:%d is not a plain statement the adapter reads", mod.File, st.line))
}

var reFuncName = regexp.MustCompile(`(?is)^` + qname + `\s*[(\s,;]`)

// headerExecute reads what a CREATE FUNCTION states about the function: its
// security, its argument list and whether it is a procedure.
func headerExecute(file string, st statement, routine string) *ir.Execute {
	ex := &ir.Execute{Procedure: routine == "procedure", Signature: normalizeSignature(callArgs(st.text))}
	mask := blankNested(st.text)
	if loc := reSecurity.FindStringSubmatchIndex(mask); loc != nil {
		ex.SecurityDefiner = strings.EqualFold(mask[loc[2]:loc[3]], "definer")
		ex.DefinerFile, ex.DefinerLine = file, st.line+strings.Count(mask[:loc[0]], "\n")
	}
	return ex
}

// normalizeSignature folds an argument list so the same list written twice
// compares equal.
func normalizeSignature(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, strings.Join(strings.Fields(strings.ToLower(a)), " "))
	}
	return strings.Join(parts, ",")
}

// alterExec adds what an ALTER FUNCTION states about security and schema to
// its ALTER event.
func alterExec(mod *ir.Module, st statement, restFrom int) {
	if len(mod.ModuleAnnotations) == 0 {
		return
	}
	last := &mod.ModuleAnnotations[len(mod.ModuleAnnotations)-1]
	if last.Kind != alterKind || last.Line != st.line {
		return
	}
	rest := blankNested(st.text[restFrom:])
	if loc := reSecurity.FindStringSubmatchIndex(rest); loc != nil {
		last.Kwargs["security"] = strings.ToLower(rest[loc[2]:loc[3]])
		last.Kwargs["security_line"] = st.line + strings.Count(st.text[:restFrom]+rest[:loc[0]], "\n")
	}
	if reSetSchema.MatchString(rest) {
		last.Kwargs["moved"] = true
	}
}

// defaultRule is one ALTER DEFAULT PRIVILEGES statement for functions.
type defaultRule struct {
	schemas []string
	verb    string
	roles   []string
	forRole bool
	file    string
	line    int
}

// execState is the privilege state of one effective function while the
// migrations fold.
type execState struct {
	schema    string
	procedure bool
	sig       string
	definer   bool
	defFile   string
	defLine   int
	roles     map[string]ir.ExecuteGrant
	unknown   string
}

// execFold applies privilege statements in the order migrations apply.
type execFold struct {
	state      map[string]*execState
	rules      []defaultRule
	overloaded map[string]string // FQN -> reason, kept when the function is dropped
	tainted    map[string]string // FQN -> reason, named by a statement the adapter could not run
	members    map[[2]string]ir.Membership
	noInherit  map[string]string // role -> where it was made NOINHERIT
}

func newExecFold() *execFold {
	return &execFold{state: map[string]*execState{}, overloaded: map[string]string{}, tainted: map[string]string{},
		members: map[[2]string]ir.Membership{}, noInherit: map[string]string{}}
}

func grantState(verb string) string {
	if verb == "grant" {
		return ir.ExecGranted
	}
	return ir.ExecRevoked
}

// create folds an effective CREATE of a function. A function already
// effective keeps its privileges (CREATE OR REPLACE); one created fresh
// starts from PostgreSQL's default and the default privileges in force.
func (f *execFold) create(fqn, schemaName string, sym ir.Symbol) {
	raw := sym.Execute
	st := &execState{schema: schemaName, procedure: raw.Procedure, sig: raw.Signature,
		definer: raw.SecurityDefiner, defFile: raw.DefinerFile, defLine: raw.DefinerLine}
	if prev := f.state[fqn]; prev != nil {
		st.roles, st.unknown = prev.roles, prev.unknown
		if prev.sig != st.sig {
			reason := fmt.Sprintf("%s is created with more than one argument list (%s:%d), and exposure is read per name, not per signature",
				fqn, sym.File, sym.Line)
			f.overloaded[fqn] = reason
			st.unknown = reason
		}
		f.state[fqn] = st
		return
	}
	st.roles = map[string]ir.ExecuteGrant{
		ir.ExecPublic: {Role: ir.ExecPublic, State: ir.ExecGranted, File: sym.File, Line: sym.Line, Default: true},
	}
	for _, r := range f.rules {
		if len(r.schemas) > 0 && !contains(r.schemas, schemaName) {
			continue
		}
		for _, role := range r.roles {
			g := ir.ExecuteGrant{Role: role, State: grantState(r.verb), File: r.file, Line: r.line}
			if r.forRole {
				g.State = ir.ExecUnknown
				g.Reason = fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE at %s:%d: the role that creates the function is not known", r.file, r.line)
			}
			st.roles[role] = g
		}
	}
	st.unknown = f.overloaded[fqn]
	f.state[fqn] = st
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// apply folds a privilege event of the file at the line.
func (f *execFold) apply(an ir.Annotation, file string) {
	kw := an.Kwargs
	switch an.Kind {
	case memberKind, memberUnknown, inheritKind:
		f.applyMember(an, file)
		return
	}
	if an.Kind == privUnknownKind {
		reason, _ := kw["reason"].(string)
		for _, n := range stringsOf(kw["names"]) {
			f.tainted[n] = reason
		}
		schemas := stringsOf(kw["schemas"])
		all, _ := kw["all"].(bool)
		for _, st := range f.state {
			if all || contains(schemas, st.schema) {
				st.unknown = reason
			}
		}
		return
	}
	verb, _ := kw["verb"].(string)
	target, _ := kw["target"].(string)
	roles := stringsOf(kw["roles"])
	if target == targetDefault {
		forRole, _ := kw["for_role"].(bool)
		f.rules = append(f.rules, defaultRule{schemas: stringsOf(kw["schemas"]), verb: verb, roles: roles,
			forRole: forRole, file: file, line: an.Line})
		return
	}
	routine, _ := kw["routine"].(string)
	set := func(st *execState) {
		for _, role := range roles {
			st.roles[role] = ir.ExecuteGrant{Role: role, State: grantState(verb), File: file, Line: an.Line}
		}
	}
	switch target {
	case targetNames:
		for _, n := range stringsOf(kw["names"]) {
			if st := f.state[n]; st != nil {
				set(st)
			}
		}
	case targetAll:
		schemas := stringsOf(kw["schemas"])
		for _, st := range f.state {
			if !contains(schemas, st.schema) {
				continue
			}
			if routine == "function" && st.procedure || routine == "procedure" && !st.procedure {
				continue
			}
			set(st)
		}
	}
}

// alter folds the security an ALTER FUNCTION sets, and a move to another
// schema, which the FQN no longer describes.
func (f *execFold) alter(fqn string, an ir.Annotation, file string) {
	st := f.state[fqn]
	if st == nil {
		return
	}
	if sec, _ := an.Kwargs["security"].(string); sec != "" {
		line, _ := an.Kwargs["security_line"].(int)
		st.definer, st.defFile, st.defLine = sec == "definer", file, line
	}
	if moved, _ := an.Kwargs["moved"].(bool); moved {
		st.unknown = fmt.Sprintf("ALTER FUNCTION ... SET SCHEMA at %s:%d moves %s to a schema the adapter does not follow", file, an.Line, fqn)
	}
}

func (f *execFold) drop(fqn string) { delete(f.state, fqn) }

func (f *execFold) rename(from, to string) {
	if st := f.state[from]; st != nil {
		delete(f.state, from)
		f.state[to] = st
	}
}

// final is the Execute fact of an effective function.
func (f *execFold) final(fqn string) *ir.Execute {
	st := f.state[fqn]
	if st == nil {
		return nil
	}
	ex := &ir.Execute{SecurityDefiner: st.definer, Signature: st.sig, Procedure: st.procedure, Unknown: st.unknown}
	if st.definer {
		ex.DefinerFile, ex.DefinerLine = st.defFile, st.defLine
	}
	if ex.Unknown == "" {
		ex.Unknown = f.tainted[fqn]
	}
	for _, g := range st.roles {
		ex.Roles = append(ex.Roles, g)
	}
	sort.Slice(ex.Roles, func(i, j int) bool { return ex.Roles[i].Role < ex.Roles[j].Role })
	return ex
}

func stringsOf(v interface{}) []string {
	s, _ := v.([]string)
	return s
}

// scanDynamicMember finds a role GRANT or REVOKE, or a CREATE ROLE ... IN
// ROLE, inside a statement that is not one: a DO block, a function body, an
// EXECUTE string. The members it names literally are unknown; with none
// literal, every role is.
func scanDynamicMember(mod *ir.Module, st statement) {
	reason := fmt.Sprintf("a role membership statement inside the statement at %s:%d is not a plain statement the adapter reads", mod.File, st.line)
	var members []string
	all := false
	for _, m := range reDynamicMember.FindAllStringSubmatch(st.text, -1) {
		if reOn.MatchString(m[1]) {
			continue // an object grant: scanDynamicPriv reads it
		}
		tf := reToFrom.FindStringSubmatch(m[1])
		if tf == nil {
			if strings.Contains(strings.ToLower(m[1]), "to") || strings.Contains(strings.ToLower(m[1]), "from") {
				all = true
			}
			continue
		}
		for _, id := range strings.Split(tf[1], ",") {
			members = append(members, unquote(strings.TrimSpace(id)))
		}
	}
	for _, m := range reDynamicRole.FindAllStringSubmatch(st.text, -1) {
		members = append(members, unquote(m[1]))
	}
	for _, loc := range reDynamicGrantee.FindAllStringIndex(st.text, -1) {
		clause := strings.ToLower(st.text[loc[0]:loc[1]])
		// IN ROLE alone is read above; a ROLE, USER or ADMIN clause names members.
		if reInRoleOnly.MatchString(clause) {
			continue
		}
		all = true
	}
	if len(members) > 0 || all {
		memberUnknownEvent(mod, st, members, all, reason)
	}
}

// applyMember folds a role membership event.
func (f *execFold) applyMember(an ir.Annotation, file string) {
	kw := an.Kwargs
	set := func(member, role, state, reason string) {
		f.members[[2]string{member, role}] = ir.Membership{Member: member, Role: role, State: state,
			File: file, Line: an.Line, Reason: reason}
	}
	switch an.Kind {
	case inheritKind:
		role, _ := kw["role"].(string)
		if inherit, _ := kw["inherit"].(bool); inherit {
			delete(f.noInherit, role)
		} else {
			f.noInherit[role] = fmt.Sprintf("%s:%d", file, an.Line)
		}
	case memberUnknown:
		reason, _ := kw["reason"].(string)
		if all, _ := kw["all"].(bool); all {
			set(ir.MemberAny, ir.MemberAny, ir.ExecUnknown, reason)
		}
		for _, m := range stringsOf(kw["members"]) {
			set(m, ir.MemberAny, ir.ExecUnknown, reason)
		}
	default:
		verb, _ := kw["verb"].(string)
		optionFor, _ := kw["option_for"].(string)
		noInherit, _ := kw["no_inherit"].(bool)
		for _, m := range stringsOf(kw["members"]) {
			for _, r := range stringsOf(kw["roles"]) {
				switch {
				case verb == "grant" && noInherit:
					set(m, r, ir.ExecUnknown, fmt.Sprintf("WITH INHERIT FALSE at %s:%d: the member does not inherit privileges automatically, but SET ROLE reaches them", file, an.Line))
				case verb == "grant":
					set(m, r, ir.ExecGranted, "")
				case optionFor == "inherit":
					set(m, r, ir.ExecUnknown, fmt.Sprintf("REVOKE INHERIT OPTION FOR at %s:%d changes whether the member inherits", file, an.Line))
				default:
					set(m, r, ir.ExecRevoked, "")
				}
			}
		}
	}
}

// memberships is the membership set left in force, sorted. A member role
// made NOINHERIT does not take its roles' privileges automatically, so its
// memberships read unknown, never revoked.
func (f *execFold) memberships() []ir.Membership {
	var out []ir.Membership
	for _, m := range f.members {
		if where, ok := f.noInherit[m.Member]; ok && m.State == ir.ExecGranted {
			m.State = ir.ExecUnknown
			m.Reason = fmt.Sprintf("%s is NOINHERIT (%s): it does not take the privileges of %s automatically, but SET ROLE reaches them", m.Member, where, m.Role)
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Member != out[j].Member {
			return out[i].Member < out[j].Member
		}
		return out[i].Role < out[j].Role
	})
	return out
}
