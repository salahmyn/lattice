package typescript

// @module-feature adapters.typescript

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// httpMethods are the axios shorthand methods that send a request.
var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true}

// globalObjects are the objects fetch is also reached through.
var globalObjects = map[string]bool{"window": true, "globalThis": true, "self": true}

// functionsPrefix is where the platform serves an edge function by its name.
const functionsPrefix = "/functions/v1/"

// @enforces reachability:INV-11
// SetHTTPOrigins declares the origins of workspace.yaml http.origins. The
// env:<NAME> entries name an environment variable that holds an own origin: a
// request URL that is exactly that variable and a literal path is path-only
// (see originPath). Call it before parsing.
func (a *Adapter) SetHTTPOrigins(origins []string) {
	a.envOrigins = nil
	for _, n := range ir.OriginNames(origins, "env") {
		if a.envOrigins == nil {
			a.envOrigins = map[string]bool{}
		}
		a.envOrigins[n] = true
	}
}

// callee is the function a call_expression calls. The grammar reads
// await f<T>(x) as a call whose function is the await_expression around f, so
// the awaited operand is the callee; without this a call with type arguments
// after an await matched no call shape and was dropped.
func callee(call *sitter.Node) *sitter.Node {
	fn := call.ChildByFieldName("function")
	for fn != nil && fn.Kind() == "await_expression" && fn.NamedChildCount() > 0 {
		fn = fn.NamedChild(0)
	}
	return fn
}

// httpRef records a request the call sends, as a ref of kind http keyed by
// method and path (ir.HTTPKey): fetch(url, {method}), axios.get(url),
// axios({method, url}), supabase.functions.invoke('name'). The key is empty
// when the URL or the method is not a literal, which the core counts as
// dynamic. A call of any other shape records nothing.
func (w *tsRefWalker) httpRef(fn, args *sitter.Node) {
	if fn == nil || args == nil || args.NamedChildCount() == 0 {
		return
	}
	first := args.NamedChild(0)
	var second *sitter.Node
	if args.NamedChildCount() > 1 {
		second = args.NamedChild(1)
	}
	switch fn.Kind() {
	case "identifier":
		switch w.text(fn) {
		case "fetch":
			w.emitHTTP(fn, w.requestKey(first, second, "GET"))
		case "axios":
			if first.Kind() == "object" {
				w.emitHTTP(fn, w.configKey(first))
			}
		}
	case "member_expression":
		obj, prop := fn.ChildByFieldName("object"), fn.ChildByFieldName("property")
		if obj == nil || prop == nil {
			return
		}
		name := w.text(prop)
		switch {
		case name == "fetch" && obj.Kind() == "identifier" && globalObjects[w.text(obj)]:
			w.emitHTTP(prop, w.requestKey(first, second, "GET"))
		case httpMethods[name] && obj.Kind() == "identifier" && w.text(obj) == "axios":
			w.emitHTTP(prop, w.requestKey(first, nil, strings.ToUpper(name)))
		case name == "invoke" && obj.Kind() == "member_expression" && w.text(obj.ChildByFieldName("property")) == "functions":
			w.emitHTTP(prop, w.invokeKey(first, second))
		}
	}
}

func (w *tsRefWalker) emitHTTP(at *sitter.Node, r ir.Ref) { w.emit(at, r) }

// requestKey is the key of fetch(url, options) or axios.get(url): the URL
// must be a literal, and so must the method when options set one.
func (w *tsRefWalker) requestKey(url, options *sitter.Node, def string) ir.Ref {
	u, lit, ok := w.requestURL(url)
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	method, ok := w.methodOf(options, def)
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	r := ir.Ref{Name: ir.HTTPKey(method, u), Qualifier: ir.HTTPOrigin(u), Kind: ir.RefHTTP}
	if lit {
		r = withLit(r, url)
	}
	return r
}

// configKey is the key of axios({method, url}).
func (w *tsRefWalker) configKey(cfg *sitter.Node) ir.Ref {
	val, state := w.property(cfg, "url")
	if state != propFound {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	u, lit, ok := w.requestURL(val)
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	method, ok := w.methodOf(cfg, "GET")
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	r := ir.Ref{Name: ir.HTTPKey(method, u), Qualifier: ir.HTTPOrigin(u), Kind: ir.RefHTTP}
	if lit {
		r = withLit(r, val)
	}
	return r
}

// invokeKey is the key of functions.invoke('name', {method}): the platform
// serves a function at /functions/v1/<name> and invokes it with POST unless
// the options say otherwise.
func (w *tsRefWalker) invokeKey(name, options *sitter.Node) ir.Ref {
	if name.Kind() != "string" {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	fnName := strings.Trim(w.p.literalText(name), "/")
	if fnName == "" {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	method, ok := w.methodOf(options, "POST")
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	return withLit(ir.Ref{Name: ir.HTTPKey(method, functionsPrefix+fnName), Kind: ir.RefHTTP}, name)
}

// withLit records where the string literal n, the one that carries the name
// the reference is keyed by, is in the source (ir.Ref.LitStart, LitEnd). A
// reference with no name, and a node that is not a plain string or template
// literal, record nothing.
func withLit(r ir.Ref, n *sitter.Node) ir.Ref {
	if n != nil && r.Name != "" && (n.Kind() == "string" || n.Kind() == "template_string") {
		r.LitStart, r.LitEnd = int(n.StartByte()), int(n.EndByte())
	}
	return r
}

// requestURL is the URL of a request: a literal (literal is true, and the
// source holds it as one string), or a declared env origin followed by a
// literal path, whose URL is that path.
func (w *tsRefWalker) requestURL(n *sitter.Node) (u string, literal, ok bool) {
	if n == nil {
		return "", false, false
	}
	if u, ok = w.literalURL(n); ok {
		return u, true, true
	}
	u, ok = w.originPath(n)
	return u, false, ok
}

// originPath reads a URL that is one declared env origin and then literal
// text only: `${import.meta.env.X}/a/b`, or import.meta.env.X + '/a' (+ '/b').
// A second substitution, a computed part, or an env variable nothing
// declares as an origin is not path-only.
// @enforces reachability:INV-11
func (w *tsRefWalker) originPath(n *sitter.Node) (string, bool) {
	if len(w.p.envOrigins) == 0 {
		return "", false
	}
	switch n.Kind() {
	case "template_string":
		if n.NamedChildCount() == 0 {
			return "", false
		}
		first := n.NamedChild(0)
		if first.Kind() != "template_substitution" || first.StartByte() != n.StartByte()+1 || first.NamedChildCount() != 1 ||
			!w.isEnvOrigin(first.NamedChild(0)) {
			return "", false
		}
		var b strings.Builder
		for i := 1; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(uint(i))
			if c.Kind() != "string_fragment" {
				return "", false
			}
			b.WriteString(w.text(c))
		}
		return b.String(), true
	case "binary_expression":
		var lits []string
		cur := n
		for cur.Kind() == "binary_expression" {
			op := cur.ChildByFieldName("operator")
			l, r := cur.ChildByFieldName("left"), cur.ChildByFieldName("right")
			if op == nil || w.text(op) != "+" || l == nil || r == nil {
				return "", false
			}
			s, ok := w.literalURL(r)
			if !ok {
				return "", false
			}
			lits = append([]string{s}, lits...)
			cur = l
		}
		if !w.isEnvOrigin(cur) {
			return "", false
		}
		return strings.Join(lits, ""), true
	}
	return "", false
}

// isEnvOrigin reports whether n reads a declared env origin:
// import.meta.env.NAME, process.env.NAME, either with ['NAME'], and
// Deno.env.get('NAME'), under parentheses, a non-null assertion or a cast.
func (w *tsRefWalker) isEnvOrigin(n *sitter.Node) bool {
	for n != nil {
		switch n.Kind() {
		case "parenthesized_expression", "non_null_expression", "as_expression":
			if n.NamedChildCount() == 0 {
				return false
			}
			n = n.NamedChild(0)
			continue
		}
		break
	}
	if n == nil {
		return false
	}
	txt := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, w.text(n))
	for name := range w.p.envOrigins {
		for _, form := range []string{
			"import.meta.env." + name, "process.env." + name,
			"import.meta.env['" + name + "']", "import.meta.env[\"" + name + "\"]",
			"process.env['" + name + "']", "process.env[\"" + name + "\"]",
			"Deno.env.get('" + name + "')", "Deno.env.get(\"" + name + "\")",
		} {
			if txt == form {
				return true
			}
		}
	}
	return false
}

// literalURL returns the text of a string literal, or of a template literal
// with no substitution. Anything else is computed.
func (w *tsRefWalker) literalURL(n *sitter.Node) (string, bool) {
	switch n.Kind() {
	case "string":
		return w.p.literalText(n), true
	case "template_string":
		if n.NamedChildCount() == 0 {
			return strings.Trim(w.text(n), "`"), true
		}
		var b strings.Builder
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(uint(i))
			if c.Kind() != "string_fragment" {
				return "", false // a ${} substitution, or an escape the key cannot stand for
			}
			b.WriteString(w.text(c))
		}
		return b.String(), true
	}
	return "", false
}

// methodOf reads the method of a request's options. With no options it is
// def; with an object that sets method to a string literal, that literal;
// with an object that does not mention method and has no spread, def.
// Options that are not an object literal, a computed method and a spread
// that may set one leave the method unknown.
func (w *tsRefWalker) methodOf(options *sitter.Node, def string) (string, bool) {
	if options == nil {
		return def, true
	}
	if options.Kind() != "object" {
		return "", false
	}
	val, state := w.property(options, "method")
	switch state {
	case propAbsent:
		return def, true
	case propFound:
		if val.Kind() == "string" {
			return w.p.literalText(val), true
		}
	}
	return "", false
}

type propState int

const (
	propAbsent  propState = iota // the object does not set the key
	propFound                    // a key: value pair
	propUnknown                  // set, or maybe set, to something this cannot read (shorthand, spread)
)

// property finds a key in an object literal.
func (w *tsRefWalker) property(obj *sitter.Node, key string) (*sitter.Node, propState) {
	state := propAbsent
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		c := obj.NamedChild(uint(i))
		switch c.Kind() {
		case "pair":
			k := c.ChildByFieldName("key")
			if k == nil {
				continue
			}
			name := w.text(k)
			if k.Kind() == "string" {
				name = w.p.literalText(k)
			}
			if name == key {
				return c.ChildByFieldName("value"), propFound
			}
		case "shorthand_property_identifier":
			if w.text(c) == key {
				state = propUnknown
			}
		case "spread_element", "method_definition":
			state = propUnknown
		}
	}
	return nil, state
}
