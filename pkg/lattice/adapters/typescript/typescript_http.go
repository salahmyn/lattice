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
	u, ok := w.literalURL(url)
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	method, ok := w.methodOf(options, def)
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	return withLit(ir.Ref{Name: ir.HTTPKey(method, u), Qualifier: ir.HTTPOrigin(u), Kind: ir.RefHTTP}, url)
}

// configKey is the key of axios({method, url}).
func (w *tsRefWalker) configKey(cfg *sitter.Node) ir.Ref {
	val, state := w.property(cfg, "url")
	if state != propFound {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	u, ok := w.literalURL(val)
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	method, ok := w.methodOf(cfg, "GET")
	if !ok {
		return ir.Ref{Kind: ir.RefHTTP}
	}
	return withLit(ir.Ref{Name: ir.HTTPKey(method, u), Qualifier: ir.HTTPOrigin(u), Kind: ir.RefHTTP}, val)
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
