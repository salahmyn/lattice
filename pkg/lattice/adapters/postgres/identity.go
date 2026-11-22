package postgres

// @module-feature adapters.postgres

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// param is one input parameter of a function, as its CREATE declares it.
type param struct {
	name    string // lower case; "" when the parameter is unnamed
	pos     int    // 1-based among the input parameters
	boolean bool   // declared boolean or bool
}

var (
	reParamMode    = regexp.MustCompile(`(?is)^\s*(in|out|inout|variadic)\s+`)
	reParamDefault = regexp.MustCompile(`(?is)\s(?:default\b|=)`)
	reParamNamed   = regexp.MustCompile(`(?is)^("(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$]*)\s+(\S.*)$`)
	reBoolType     = regexp.MustCompile(`(?i)^(?:pg_catalog\s*\.\s*)?(?:boolean|bool)$`)
)

// functionParams reads the input parameters of a CREATE FUNCTION's argument
// list. A parameter is boolean only when its declared type is boolean or
// bool: no name is read for a meaning.
// @enforces adapters.postgres:INV-11
func functionParams(args []string) []param {
	var out []param
	for _, a := range args {
		a = strings.TrimSpace(a)
		if loc := reParamDefault.FindStringIndex(a); loc != nil {
			a = strings.TrimSpace(a[:loc[0]])
		}
		if m := reParamMode.FindStringSubmatch(a); m != nil {
			if strings.EqualFold(m[1], "out") {
				continue // an output the caller does not supply
			}
			a = strings.TrimSpace(a[len(m[0]):])
		}
		if a == "" {
			continue
		}
		p := param{pos: len(out) + 1}
		typ := a
		if m := reParamNamed.FindStringSubmatch(a); m != nil && (!reBoolType.MatchString(a)) {
			p.name = strings.ToLower(strings.Trim(m[1], `"`))
			typ = strings.TrimSpace(m[2])
		}
		p.boolean = reBoolType.MatchString(typ)
		out = append(out, p)
	}
	return out
}

// paramRef is how a body names a parameter: its name, or $n.
func (p param) ref() string {
	if p.name != "" {
		return p.name
	}
	return "$" + strconv.Itoa(p.pos)
}

// boolParams names the boolean parameters, in order.
func boolParams(ps []param) []string {
	var out []string
	for _, p := range ps {
		if p.boolean {
			out = append(out, p.ref())
		}
	}
	return out
}

var (
	reHashCall = regexp.MustCompile(`(?i)\b(?:[a-z_][a-z0-9_]*\s*\.\s*)?(?:digest|crypt|sha224|sha256|sha384|sha512|md5|hmac)\s*\(`)
	reWrapper  = regexp.MustCompile(`(?i)\b(?:encode|decode|lower|upper|trim|btrim|convert_from|convert_to)\s*$`)
	reCastTail = regexp.MustCompile(`^\s*::\s*[A-Za-z_][A-Za-z0-9_.]*(?:\[\])?`)
	reOperandR = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*(?:\s*\.\s*[A-Za-z_][A-Za-z0-9_]*)?)\s*(\(?)`)
	reOperandL = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*(?:\s*\.\s*[A-Za-z_][A-Za-z0-9_]*)?)\s*(?:::\s*[A-Za-z_][A-Za-z0-9_.]*(?:\[\])?)?\s*$`)
	reWordsRef = regexp.MustCompile(`\$\d+|[A-Za-z_][A-Za-z0-9_]*`)
	reClause   = regexp.MustCompile(`(?i)\b(?:where|if|elsif|elseif|when)\b`)
	notColumn  = map[string]bool{"null": true, "true": true, "false": true, "not": true, "any": true, "all": true,
		"some": true, "select": true, "case": true, "exists": true, "then": true, "and": true, "or": true, "where": true}
)

// hashAuth finds the first place a function body compares a hash of one of its
// own parameters with something other than a parameter or a literal: a hash
// function (digest, crypt, sha256, md5, hmac, also wrapped in encode(...)) whose
// arguments name a parameter, tested with = in a WHERE, or with =, <>, !=,
// IS DISTINCT FROM or IS NOT DISTINCT FROM in an IF, ELSIF or CASE WHEN. The hashed
// expression must reference a parameter by name, or $n; a hash of a column or
// of a literal is not read, and neither is a hash that is only assigned or
// returned. Comments and strings are not code.
// @enforces adapters.postgres:INV-9
func hashAuth(file string, st statement, params []param) *ir.HashAuth {
	if len(params) == 0 {
		return nil
	}
	code := maskSQL(st.text, false)
	from := 0
	if loc := reBodyStart.FindStringIndex(maskSQL(st.text, true)); loc != nil {
		from = loc[0]
	}
	byName := map[string]param{}
	for _, p := range params {
		if p.name != "" {
			byName[p.name] = p
		}
	}
	resolve := func(w string) string {
		if w[0] == '$' {
			n, _ := strconv.Atoi(w[1:])
			if n >= 1 && n <= len(params) {
				return params[n-1].ref()
			}
			return ""
		}
		if p, ok := byName[w]; ok {
			return p.ref()
		}
		return ""
	}
	for _, loc := range reHashCall.FindAllStringIndex(code[from:], -1) {
		callAt, open := from+loc[0], from+loc[1]-1
		cl := matchParen(code, open)
		if cl < 0 {
			continue
		}
		pname := hashedParam(code[open+1:cl], resolve)
		if pname == "" {
			continue
		}
		start, end := hashSpan(code, callAt, cl)
		stmt := strings.LastIndexByte(code[:start], ';') + 1
		cls := reClause.FindAllString(code[max(stmt, from):start], -1)
		if len(cls) == 0 {
			continue
		}
		// A WHERE is read for = only; an IF, ELSIF or CASE WHEN condition
		// also for <>, !=, IS DISTINCT FROM and IS NOT DISTINCT FROM.
		inIf := !strings.EqualFold(cls[len(cls)-1], "where")
		if !comparedWithColumn(code, start, end, params, inIf) {
			continue
		}
		return &ir.HashAuth{Param: pname, File: file, Line: st.line + strings.Count(st.text[:callAt], "\n")}
	}
	return nil
}

// hashSpan widens a hash call code[callAt:cl+1] over the casts and wrappers
// around it (encode(digest(...), 'hex'), lower(...), ::text) and returns the
// span of the whole expression.
func hashSpan(code string, callAt, cl int) (start, end int) {
	start, end = callAt, cl+1
	for i := 0; i < 4; i++ {
		if m := reCastTail.FindString(code[end:]); m != "" {
			end += len(m)
			continue
		}
		before := strings.TrimRight(code[:start], " \t\r\n")
		if !strings.HasSuffix(before, "(") {
			break
		}
		w := reWrapper.FindStringIndex(strings.TrimRight(before[:len(before)-1], " \t\r\n"))
		if w == nil {
			break
		}
		wc := matchParen(code, len(before)-1)
		if wc < 0 {
			break
		}
		start, end = w[0], wc+1
	}
	return start, end
}

// isHashOfParam says whether e is, as a whole, a hash of a parameter: a hash
// function whose arguments name one (isParam says which names do), with
// optional casts and wrappers. It is the recogniser hashAuth uses, so a row
// selector and an authentication read the same shape. e must be masked.
func isHashOfParam(e string, isParam func(string) bool) bool {
	e = strings.TrimSpace(e)
	resolve := func(w string) string {
		if isParam(w) {
			return w
		}
		return ""
	}
	for _, loc := range reHashCall.FindAllStringIndex(e, -1) {
		cl := matchParen(e, loc[1]-1)
		if cl < 0 || hashedParam(e[loc[1]:cl], resolve) == "" {
			continue
		}
		if start, end := hashSpan(e, loc[0], cl); start == 0 && end == len(e) {
			return true
		}
	}
	return false
}

// hashedParam returns the first name in the arguments of a hash call that
// resolve says is a parameter, as it returns it, or "".
func hashedParam(args string, resolve func(string) string) string {
	for _, loc := range reWordsRef.FindAllStringIndex(args, -1) {
		w := strings.ToLower(args[loc[0]:loc[1]])
		if loc[0] > 0 && args[loc[0]-1] == '.' {
			continue // a qualified name is a column
		}
		if rest := strings.TrimLeft(args[loc[1]:], " \t\r\n"); strings.HasPrefix(rest, "(") {
			continue // a function name
		}
		if p := resolve(w); p != "" {
			return p
		}
	}
	return ""
}

var (
	reOpRight = regexp.MustCompile(`(?is)^\s*(=[^=>]|<>|!=|is\s+not\s+distinct\s+from\b|is\s+distinct\s+from\b)`)
	reOpLeft  = regexp.MustCompile(`(?is)(=|<>|!=|\bis\s+not\s+distinct\s+from|\bis\s+distinct\s+from)\s*$`)
)

// comparedWithColumn reports whether the expression code[start:end] is an
// operand of a comparison whose other operand is a name that is not a
// parameter. The comparison is a plain =; with anyOp it is also <>, !=,
// IS DISTINCT FROM or IS NOT DISTINCT FROM.
func comparedWithColumn(code string, start, end int, params []param, anyOp bool) bool {
	ok := func(operand string) bool {
		w := strings.ToLower(strings.Join(strings.Fields(operand), ""))
		if w == "" || notColumn[w] {
			return false
		}
		for _, p := range params {
			if p.name == w {
				return false
			}
		}
		return true
	}
	allowed := func(op string) bool {
		op = strings.ToLower(strings.TrimSpace(op))
		return strings.HasPrefix(op, "=") || anyOp
	}
	rest := code[end:]
	if m := reOpRight.FindStringSubmatch(rest); m != nil && allowed(m[1]) {
		after := rest[len(m[0]):]
		if strings.HasPrefix(m[1], "=") {
			after = rest[len(m[0])-1:] // [^=>] consumed the next character
		}
		if o := reOperandR.FindStringSubmatch(after); o != nil && o[2] == "" && ok(o[1]) {
			return true
		}
	}
	before := strings.TrimRight(code[:start], " \t\r\n")
	if loc := reOpLeft.FindStringSubmatchIndex(before); loc != nil && allowed(before[loc[2]:loc[3]]) {
		op := before[loc[2]:loc[3]]
		if op != "=" || loc[2] == 0 || !strings.ContainsRune("<>!:=|", rune(before[loc[2]-1])) {
			if o := reOperandL.FindStringSubmatch(before[:loc[2]]); o != nil && ok(o[1]) {
				return true
			}
		}
	}
	return false
}

// resolveHelperIdentity gives a function that reads no caller identity itself
// the read of the first SQL function it calls that does, one level only: the
// helper's own read, not what its helpers read. A call counts only when it
// resolves to a function of the workspace that is created once; a builtin, an
// external function and an overloaded name are not identity. It runs where
// every module is known.
// @enforces adapters.postgres:INV-10
func resolveHelperIdentity(mods []ir.Module, eff map[string][2]int, overloaded map[string]string) {
	direct := map[string]*ir.CallerIdentity{}
	for fqn, at := range eff {
		if ex := mods[at[0]].Symbols[at[1]].Execute; ex != nil && ex.Identity != nil {
			if _, many := overloaded[fqn]; !many {
				direct[fqn] = ex.Identity
			}
		}
	}
	for fqn, at := range eff {
		sym := &mods[at[0]].Symbols[at[1]]
		if sym.Execute == nil || sym.Execute.Identity != nil {
			continue
		}
		for _, r := range sym.Refs {
			h, ok := direct[r.Target]
			if r.Kind != ir.RefCall || !ok || r.Target == fqn {
				continue
			}
			sym.Execute.Identity = &ir.CallerIdentity{
				Via:  "via " + strings.TrimPrefix(r.Target, "sql.") + "() → " + h.Via,
				File: h.File, Line: h.Line}
			break
		}
	}
}

// resolveOwnerCalls settles the columns a body compares with a function called
// with no arguments. The column is a column of ownership when that function is a
// function of the workspace, created once, that reads the caller's identity
// itself; the identity read is then cited through the function. For any other
// call (a builtin, an unknown or overloaded name, a function that reads no
// identity) the comparison is an ordinary one, and it is recorded without an
// owner. It runs where every module is known, with the identity reads recorded
// on the effective functions.
// @enforces adapters.postgres:INV-15
func resolveOwnerCalls(mods []ir.Module, eff map[string][2]int, overloaded map[string]string) {
	direct := map[string]*ir.CallerIdentity{}
	for fqn, at := range eff {
		if ex := mods[at[0]].Symbols[at[1]].Execute; ex != nil && ex.Identity != nil && !strings.HasPrefix(ex.Identity.Via, "via ") {
			if _, many := overloaded[fqn]; !many {
				direct[fqn] = ex.Identity
			}
		}
	}
	for _, at := range eff {
		sym := &mods[at[0]].Symbols[at[1]]
		for i := range sym.CondReads {
			c := &sym.CondReads[i]
			if c.OwnerCall == "" {
				continue
			}
			if h, ok := direct[c.OwnerCall]; ok {
				c.OwnerVia = strings.TrimPrefix(c.OwnerCall, "sql.") + "() → " + h.Via
			}
			c.OwnerCall = ""
		}
	}
}
