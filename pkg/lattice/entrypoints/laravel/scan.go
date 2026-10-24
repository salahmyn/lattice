package laravel

// @module-feature entrypoints

import (
	"regexp"
	"strings"
)

// Route is one route a routes file registers, for checking declared routes
// against code.
type Route struct {
	Method  string // GET, POST, ..., ANY
	Path    string // "/"-joined: mount prefix, group prefixes, the route's own path
	Line    int
	Handler string // Class::method; "" for a closure
}

// ScanRoutes returns every route a routes file registers when it is mounted
// under mount. It follows nested groups and their prefix() calls, closures
// kept in a variable and mounted later (->group($fn), Route::<macro>($fn)),
// and only/except on resources. It does not run PHP: routes built from
// variables or loops are not seen.
// @enforces entrypoints:INV-4
func ScanRoutes(body, mount string) []Route {
	var out []Route
	for _, r := range scanFile(body, parseUseStatements(body), mount) {
		out = append(out, Route{Method: r.method, Path: r.path, Line: r.line, Handler: r.handler.fqn})
	}
	return out
}

type scanned struct {
	httpRoute
	line int
}

type body struct {
	start, end int    // the braces
	group      bool   // a closure passed to group(): prefix applies, site is the call
	prefix     string // group bodies
	site       int
	name       string // variable closures
	assign     int    // where the variable is assigned; its mounts are the ones before its next assignment
	dead       bool   // a Route::macro definition: registers nothing until the macro is called
}

type mountSite struct {
	prefix string
	site   int
}

type fileScan struct {
	src    string // comments blanked, same offsets as the file
	stmt   []int  // statement start at each offset, in the innermost brace scope
	bodies []body
	mounts map[string][]mountSite
}

var (
	groupCall   = regexp.MustCompile(`(?:->|Route::)group\s*\(`)
	prefixCall  = regexp.MustCompile(`(?:->|Route::)prefix\s*\(\s*(['"])(.*?)['"]\s*\)`)
	prefixKey   = regexp.MustCompile(`['"]prefix['"]\s*=>\s*['"]([^'"]*)['"]`)
	closureHead = regexp.MustCompile(`^\s*(?:static\s+)?function\b`)
	varClosure  = regexp.MustCompile(`\$(\w+)\s*=\s*(?:static\s+)?function\b`)
	varArg      = regexp.MustCompile(`^\s*\$(\w+)\s*\)`)
	macroMount  = regexp.MustCompile(`Route::(\w+)\s*\(\s*\$(\w+)\s*\)`)
	verbCall    = regexp.MustCompile(`(Route::|->)(get|post|put|patch|delete|options|any|match|resource|apiResource)\s*\(`)
	firstString = regexp.MustCompile(`^\s*(['"])(.*?)['"]\s*(?:,|$)`)
	verbList    = regexp.MustCompile(`^\s*\[([^\]]*)\]\s*,`)
	quoted      = regexp.MustCompile(`['"]([^'"]+)['"]`)
	invokable   = regexp.MustCompile(`^\s*['"]([A-Za-z_\\][A-Za-z0-9_\\]*)['"]\s*$`)
	classInvoke = regexp.MustCompile(`^\s*([A-Za-z_\\][A-Za-z0-9_\\]*)::class\s*$`)
	onlyCall    = regexp.MustCompile(`->(only|except)\s*\(([^)]*)\)`)
	onlyOption  = regexp.MustCompile(`['"](only|except)['"]\s*=>\s*\[([^\]]*)\]`)
	macroDef    = regexp.MustCompile(`Route::macro\s*\(`)
)

func scanFile(src string, uses map[string]string, mount string) []scanned {
	f := &fileScan{src: blankComments(src), mounts: map[string][]mountSite{}}
	f.statements()
	f.findBodies()
	var out []scanned
	seen := map[string]bool{}
	line, counted := 1, 0 // matches come in file order: count newlines once
	for _, m := range verbCall.FindAllStringSubmatchIndex(f.src, -1) {
		at, open := m[0], m[1]-1
		line += strings.Count(src[counted:at], "\n")
		counted = at
		chain := f.src[f.stmt[at]:at]
		if f.src[m[2]:m[3]] == "->" && (!strings.Contains(chain, "Route::") || openDepth(chain) > 0) {
			continue // ->get( on something that is not a route chain, or inside another call's arguments
		}
		fluent := ""
		if f.src[m[2]:m[3]] == "->" { // Route::middleware(..)->prefix('m')->get(..)
			for _, p := range prefixCall.FindAllStringSubmatch(chain, -1) {
				fluent = joinRel(fluent, p[2])
			}
		}
		end := matchClose(f.src, open)
		if end < 0 {
			continue
		}
		verb, args := strings.ToUpper(f.src[m[4]:m[5]]), f.src[open+1:end] // comments blanked, strings kept
		var verbs []string
		if verb == "MATCH" {
			vl := verbList.FindStringSubmatchIndex(args)
			if vl == nil {
				continue
			}
			for _, q := range quoted.FindAllStringSubmatch(args[vl[2]:vl[3]], -1) {
				verbs = append(verbs, strings.ToUpper(q[1]))
			}
			args = args[vl[1]:]
		}
		ps := firstString.FindStringSubmatchIndex(args)
		if ps == nil {
			continue // a path built at run time
		}
		path, rest := args[ps[4]:ps[5]], args[ps[1]:]
		var routes []httpRoute
		switch verb {
		case "RESOURCE", "APIRESOURCE":
			routes = resourceRoutes(verb, path, rest, f.src[end+1:f.stmtEnd(end+1)], uses)
		default:
			h := handlerOf(rest, uses)
			if verbs == nil {
				verbs = []string{verb}
			}
			for _, v := range verbs {
				routes = append(routes, httpRoute{method: v, path: path, handler: h})
			}
		}
		for _, ctx := range f.contexts(at, map[int]bool{}) {
			for _, r := range routes {
				r.path = joinPath(append(append([]string{mount}, ctx...), fluent, r.path)...)
				k := r.method + " " + r.path + " " + r.handler.fqn
				if !seen[k] {
					seen[k] = true
					out = append(out, scanned{r, line})
				}
			}
		}
	}
	return out
}

// statements records, for every offset, where its statement starts in the
// innermost brace scope; parentheses and brackets do not start statements.
func (f *fileScan) statements() {
	type frame struct {
		ch   byte
		stmt int
	}
	s := f.src
	f.stmt = make([]int, len(s)+1)
	stack := []frame{{'{', 0}}
	brace := func() *frame {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].ch == '{' {
				return &stack[i]
			}
		}
		return &stack[0]
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\'' || c == '"' {
			j := stringEnd(s, i)
			for k := i; k <= j && k < len(s); k++ {
				f.stmt[k] = brace().stmt
			}
			i = j
			continue
		}
		switch s[i] {
		case '{':
			stack = append(stack, frame{'{', i + 1})
		case '(', '[':
			stack = append(stack, frame{s[i], -1})
		case '}', ')', ']':
			closedBrace := s[i] == '}'
			if top := stack[len(stack)-1].ch; len(stack) > 1 && (top == '{') == closedBrace {
				stack = stack[:len(stack)-1] // a stray closer of the other kind is ignored
			}
			if closedBrace && stack[len(stack)-1].ch == '{' {
				stack[len(stack)-1].stmt = i + 1 // a block ended: the next statement starts here
			}
		case ';':
			// a statement ends here, so ( and [ still open in it were never closed (a cut or broken file)
			for len(stack) > 1 && stack[len(stack)-1].ch != '{' {
				stack = stack[:len(stack)-1]
			}
			if stack[len(stack)-1].ch == '{' {
				stack[len(stack)-1].stmt = i + 1
			}
		}
		f.stmt[i] = brace().stmt
	}
	f.stmt[len(s)] = brace().stmt
}

// stmtEnd is the offset of the ';' that ends the statement around i.
func (f *fileScan) stmtEnd(i int) int {
	depth := 0
	for j := i; j < len(f.src); j++ {
		switch c := f.src[j]; c {
		case '\'', '"':
			j = stringEnd(f.src, j)
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth--; depth < 0 {
				return j
			}
		case ';':
			if depth == 0 {
				return j
			}
		}
	}
	return len(f.src)
}

func (f *fileScan) findBodies() {
	s := f.src
	for _, m := range groupCall.FindAllStringIndex(s, -1) {
		at, open := m[0], m[1]-1
		chain := s[f.stmt[at]:at]
		if !strings.Contains(chain+s[at:m[1]], "Route::") {
			continue
		}
		prefix := ""
		for _, p := range prefixCall.FindAllStringSubmatch(chain, -1) {
			prefix = joinRel(prefix, p[2])
		}
		arg := open + 1
		if rest := strings.TrimLeft(s[arg:], " \t\r\n"); strings.HasPrefix(rest, "[") { // Route::group([...], fn)
			ao := arg + strings.Index(s[arg:], "[")
			ac := matchClose(s, ao)
			if ac < 0 {
				continue
			}
			if pk := prefixKey.FindStringSubmatch(s[ao:ac]); pk != nil {
				prefix = joinRel(prefix, pk[1])
			}
			comma := strings.IndexByte(s[ac:], ',')
			if comma < 0 {
				continue
			}
			arg = ac + comma + 1
		}
		if v := varArg.FindStringSubmatch(s[arg:]); v != nil {
			f.mounts[v[1]] = append(f.mounts[v[1]], mountSite{prefix, at})
			continue
		}
		if !closureHead.MatchString(s[arg:]) {
			continue // a file path or something else run time decides
		}
		if b, ok := closureBody(s, arg); ok {
			f.bodies = append(f.bodies, body{start: b[0], end: b[1], group: true, prefix: prefix, site: at})
		}
	}
	for _, m := range varClosure.FindAllStringSubmatchIndex(s, -1) {
		if b, ok := closureBody(s, m[2]-1); ok {
			f.bodies = append(f.bodies, body{start: b[0], end: b[1], name: s[m[2]:m[3]], assign: m[0]})
		}
	}
	for _, m := range macroDef.FindAllStringIndex(s, -1) {
		if b, ok := closureBody(s, m[1]); ok {
			f.bodies = append(f.bodies, body{start: b[0], end: b[1], dead: true})
		}
	}
	for _, m := range macroMount.FindAllStringSubmatchIndex(s, -1) {
		if macro := s[m[2]:m[3]]; macro != "group" && macro != "prefix" {
			f.mounts[s[m[4]:m[5]]] = append(f.mounts[s[m[4]:m[5]]], mountSite{"", m[0]})
		}
	}
}

// contexts lists the group prefixes, outermost first, of every way the code
// at offset o is registered.
func (f *fileScan) contexts(o int, path map[int]bool) [][]string {
	var in *body
	for i := range f.bodies {
		b := &f.bodies[i]
		if b.start < o && o < b.end && (in == nil || b.start > in.start) {
			in = b
		}
	}
	if in == nil {
		return [][]string{nil}
	}
	if in.dead || path[in.start] || len(path) > 16 { // a macro body, or a closure that mounts itself
		return nil
	}
	path[in.start] = true
	defer delete(path, in.start)
	type via struct {
		prefix string
		site   int
	}
	var sites []via
	if in.group {
		sites = []via{{in.prefix, in.site}}
	} else {
		next := len(f.src)
		for _, b := range f.bodies {
			if b.name == in.name && b.assign > in.assign && b.assign < next {
				next = b.assign
			}
		}
		for _, m := range f.mounts[in.name] {
			if m.site > in.assign && m.site < next {
				sites = append(sites, via{m.prefix, m.site})
			}
		}
	}
	var out [][]string
	for _, v := range sites {
		for _, ctx := range f.contexts(v.site, path) {
			out = append(out, append(append([]string{}, ctx...), v.prefix))
		}
	}
	return out
}

// closureBody finds the braces of the function that starts at or after from.
func closureBody(s string, from int) ([2]int, bool) {
	i := strings.Index(s[from:], "function")
	if i < 0 {
		return [2]int{}, false
	}
	po := strings.IndexByte(s[from+i:], '(')
	if po < 0 {
		return [2]int{}, false
	}
	pc := matchClose(s, from+i+po)
	if pc < 0 {
		return [2]int{}, false
	}
	bo := strings.IndexByte(s[pc:], '{')
	if bo < 0 {
		return [2]int{}, false
	}
	bc := matchClose(s, pc+bo)
	if bc < 0 {
		return [2]int{}, false
	}
	return [2]int{pc + bo, bc}, true
}

func handlerOf(arg string, uses map[string]string) handlerRef {
	if sm := stringHandler.FindStringSubmatch(arg); sm != nil {
		return handlerRef{fqn: strings.ReplaceAll(sm[1], "\\\\", "\\") + "::" + sm[2], method: sm[2]}
	}
	if am := arrayHandler.FindStringSubmatch(arg); am != nil {
		return handlerRef{shortClass: am[1], method: am[2], fqn: resolveClass(am[1], uses) + "::" + am[2]}
	}
	if im := invokable.FindStringSubmatch(arg); im != nil {
		return handlerRef{fqn: strings.ReplaceAll(im[1], "\\\\", "\\") + "::__invoke", method: "__invoke"}
	}
	if cm := classInvoke.FindStringSubmatch(arg); cm != nil {
		return handlerRef{shortClass: cm[1], method: "__invoke", fqn: resolveClass(cm[1], uses) + "::__invoke"}
	}
	return handlerRef{} // a closure
}

func resourceRoutes(verb, name, arg, tail string, uses map[string]string) []httpRoute {
	cm := classOnly.FindStringSubmatch(arg)
	if cm == nil {
		if cm = invokable.FindStringSubmatch(arg); cm == nil { // a controller named by a string
			return nil
		}
	}
	class := resolveClass(cm[1], uses)
	methods := resourceMethods
	if verb == "APIRESOURCE" {
		methods = []struct{ verb, action string }{
			{"GET", "index"}, {"POST", "store"}, {"GET", "show"}, {"PUT", "update"}, {"DELETE", "destroy"},
		}
	}
	keep := map[string]bool{}
	mode := ""
	for _, oc := range append(onlyCall.FindAllStringSubmatch(tail, -1), onlyOption.FindAllStringSubmatch(arg, -1)...) {
		mode = oc[1]
		for _, q := range quoted.FindAllStringSubmatch(oc[2], -1) {
			keep[q[1]] = true
		}
	}
	var out []httpRoute
	for _, rm := range methods {
		if (mode == "only" && !keep[rm.action]) || (mode == "except" && keep[rm.action]) {
			continue
		}
		out = append(out, httpRoute{method: rm.verb, path: resourcePath(name, rm.action),
			handler: handlerRef{shortClass: cm[1], method: rm.action, fqn: class + "::" + rm.action}})
	}
	return out
}

// joinPath joins path parts with single slashes, with a leading slash.
func joinPath(parts ...string) string {
	var segs []string
	for _, p := range parts {
		for _, s := range strings.Split(p, "/") {
			if s != "" {
				segs = append(segs, s)
			}
		}
	}
	return "/" + strings.Join(segs, "/")
}

func joinRel(a, b string) string { return strings.TrimPrefix(joinPath(a, b), "/") }

// stringEnd returns the offset of the quote closing the PHP string at i.
func stringEnd(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case q:
			return j
		}
	}
	return len(s) - 1
}

// matchClose returns the offset of the bracket closing the one at open,
// skipping strings; -1 when it is not closed.
func matchClose(s string, open int) int {
	depth, braces := 0, 0
	for j := open; j < len(s); j++ {
		switch s[j] {
		case '\'', '"':
			j = stringEnd(s, j)
		case '{':
			braces++
			depth++
		case '(', '[':
			depth++
		case '}':
			braces--
			if depth--; depth == 0 {
				return j
			}
		case ')', ']':
			if depth--; depth == 0 {
				return j
			}
		case ';':
			if braces == 0 { // a statement ended inside an unclosed ( or [: the file is cut or not PHP we read
				return -1
			}
		}
	}
	return -1
}

// openDepth counts the ( and [ left open in s, outside strings.
func openDepth(s string) int {
	d := 0
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '\'', '"':
			j = stringEnd(s, j)
		case '(', '[':
			d++
		case ')', ']':
			d--
		}
	}
	return d
}

// blankComments replaces PHP comments with spaces, keeping newlines and
// offsets, and leaves strings as they are.
func blankComments(src string) string {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '\'' || b[i] == '"':
			i = stringEnd(src, i)
		case (b[i] == '#' && !(i+1 < len(b) && b[i+1] == '[')) || (b[i] == '/' && i+1 < len(b) && b[i+1] == '/'):
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			for ; i < len(b) && !(b[i] == '*' && i+1 < len(b) && b[i+1] == '/'); i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			if i+1 < len(b) {
				b[i], b[i+1] = ' ', ' '
				i++
			}
		}
	}
	return string(b)
}
