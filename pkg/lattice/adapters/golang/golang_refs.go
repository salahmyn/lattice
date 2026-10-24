package golang

// @module-feature reachability

import (
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// goBuiltins are the predeclared identifiers: never a reference to code in
// the workspace.
var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true,
	"delete": true, "imag": true, "len": true, "make": true, "max": true, "min": true, "new": true,
	"panic": true, "print": true, "println": true, "real": true, "recover": true,
	"nil": true, "true": true, "false": true, "iota": true, "_": true,
	"bool": true, "byte": true, "complex64": true, "complex128": true, "error": true,
	"float32": true, "float64": true, "int": true, "int8": true, "int16": true, "int32": true,
	"int64": true, "rune": true, "string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true, "any": true, "comparable": true,
}

// collectRefs records the file's imports and the raw references of every
// symbol (and of the package's top-level code: var initializers, init),
// for the core's call graph (docs/rnd/0009). Nothing is resolved here
// beyond the file's own facts: a receiver becomes "this", a local variable
// "~" (a value of unknown type), an import binding stays its name.
// @enforces reachability:INV-1
func (p *goParser) collectRefs(root *sitter.Node, pkgName string) {
	m := p.mod
	m.RefsKnown, m.Namespace, m.Package = true, p.pkgPath, pkgName
	imports := map[string]bool{}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		if c := root.NamedChild(i); c.Type() == "import_declaration" {
			p.collectImports(c, imports)
		}
	}
	if !p.testFile && pkgName == "main" {
		for i := range m.Symbols {
			if s := &m.Symbols[i]; s.Name == "main" && s.Kind == ir.KindFunction && s.EnclosingFQN == "" {
				s.Root = "main"
			}
		}
	}
	w := &goRefWalker{p: p, imports: imports}
	var top []ir.Ref
	w.emit = func(n *sitter.Node, r ir.Ref) {
		r.Line = line(n)
		if idx := p.owners.At(n.StartByte()); idx >= 0 {
			m.Symbols[idx].Refs = append(m.Symbols[idx].Refs, r)
			return
		}
		top = append(top, r)
	}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		c := root.NamedChild(i)
		switch c.Type() {
		case "package_clause", "import_declaration", "comment":
			continue
		}
		w.enter(c)
		w.visit(c)
	}
	// Every file has a top node, even with no top-level references: it is
	// where the file's imports load other packages from.
	m.Nodes = append(m.Nodes, ir.Node{FQN: ir.TopFQN(p.pkgPath, p.testFile), Kind: ir.NodeTop,
		IsTest: p.testFile, Line: 1, Refs: top})
}

// collectImports records each import spec. The binding of an unaliased
// import is the last path element (without a /vN suffix); Resolve replaces
// it with the target package's own name when the package is in the
// workspace.
func (p *goParser) collectImports(decl *sitter.Node, names map[string]bool) {
	var specs []*sitter.Node
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case "import_spec":
				specs = append(specs, c)
			case "import_spec_list":
				walk(c)
			}
		}
	}
	walk(decl)
	for _, s := range specs {
		pathNode := s.ChildByFieldName("path")
		if pathNode == nil {
			continue
		}
		from, err := strconv.Unquote(pathNode.Content(p.src))
		if err != nil {
			continue
		}
		name := defaultBinding(from)
		if n := s.ChildByFieldName("name"); n != nil {
			name = n.Content(p.src)
			if name == "_" {
				name = ""
			}
		}
		if name != "" && name != "." {
			names[name] = true
		}
		p.mod.Imports = append(p.mod.Imports, ir.Import{Name: name, From: from, Line: line(s)})
	}
}

// defaultBinding is the name an unaliased import binds by convention: the
// last path element, skipping a major-version element (/v2).
func defaultBinding(importPath string) string {
	parts := strings.Split(importPath, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && isDigits(last[1:]) {
		last = parts[len(parts)-2]
	}
	if i := strings.IndexByte(last, '.'); i > 0 {
		last = last[:i] // gopkg.in/yaml.v3 -> yaml
	}
	return strings.ReplaceAll(last, "-", "_")
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// goRefWalker walks one top-level declaration for references, in source
// order. Go declares a local before its use, so the locals seen so far are
// exactly the names in scope (shadowing across sibling blocks aside).
type goRefWalker struct {
	p       *goParser
	imports map[string]bool // the file's import bindings
	inFunc  bool            // inside a function or method declaration
	recv    string          // the receiver of the method being walked
	locals  map[string]bool // names declared so far in the function
	types   map[string]string
	untyped map[string]bool
	emit    func(n *sitter.Node, r ir.Ref)
}

// enter starts a top-level declaration.
func (w *goRefWalker) enter(decl *sitter.Node) {
	w.inFunc = decl.Type() == "function_declaration" || decl.Type() == "method_declaration"
	w.recv = ""
	w.locals, w.types, w.untyped = map[string]bool{}, map[string]string{}, map[string]bool{}
	if decl.Type() == "method_declaration" {
		w.recv = receiverName(decl.ChildByFieldName("receiver"), w.p.src)
	}
}

func (w *goRefWalker) text(n *sitter.Node) string { return n.Content(w.p.src) }

// declare records a local and, when its declaration states one, its type.
// A name declared again with another type (or none) loses its type.
func (w *goRefWalker) declare(id *sitter.Node, typ string) {
	if !w.inFunc || id == nil || id.Type() != "identifier" {
		return
	}
	name := w.text(id)
	w.locals[name] = true
	if typ == "" || w.untyped[name] || (w.types[name] != "" && w.types[name] != typ) {
		w.untyped[name] = true
		delete(w.types, name)
		return
	}
	w.types[name] = typ
}

// declareList declares each identifier of an expression or identifier list.
func (w *goRefWalker) declareList(list *sitter.Node, typeOf func(i int) string) {
	if list == nil {
		return
	}
	if list.Type() == "identifier" {
		w.declare(list, typeOf(0))
		return
	}
	for i := 0; i < int(list.NamedChildCount()); i++ {
		w.declare(list.NamedChild(i), typeOf(i))
	}
}

func noType(int) string { return "" }

// resultOf returns "result:pkg.F" for a call of a function through an
// import binding (time.Now(), os.Open(p)), "" for anything else. The core
// makes a method called on such a value external when the import is
// outside the workspace, so time.Now().Add(d) never resolves to a
// workspace method that happens to be named Add.
func (w *goRefWalker) resultOf(e *sitter.Node) string {
	if e == nil || e.Type() != "call_expression" {
		return ""
	}
	fn := e.ChildByFieldName("function")
	if fn == nil || fn.Type() != "selector_expression" {
		return ""
	}
	operand, field := fn.ChildByFieldName("operand"), fn.ChildByFieldName("field")
	if operand == nil || field == nil {
		return ""
	}
	// A method of a value that already comes from an import (now.UTC() for
	// now time.Time, time.Now().UTC()) keeps that import as its origin.
	q, _ := w.qualifier(operand)
	switch {
	case strings.HasPrefix(q, "result:"):
		return q
	case strings.HasPrefix(q, "type:"):
		if pkg, _, dotted := strings.Cut(strings.TrimPrefix(q, "type:"), "."); dotted {
			return "result:" + pkg + "." + w.text(field)
		}
	case q != "~" && q != "this" && operand.Type() == "identifier":
		return "result:" + q + "." + w.text(field)
	}
	return ""
}

// qualifier classifies the operand of a selector: an import binding keeps
// its name, the method's receiver is "this", a local of known type is
// "type:T" (or "type:pkg.T"), anything else is a value of unknown type
// ("~"). plain is false when the operand is not a plain name.
func (w *goRefWalker) qualifier(operand *sitter.Node) (q string, plain bool) {
	if operand.Type() != "identifier" {
		if r := w.resultOf(operand); r != "" {
			return r, false
		}
		return "~", false
	}
	name := w.text(operand)
	if w.recv != "" && name == w.recv {
		return "this", true
	}
	if t := w.types[name]; strings.HasPrefix(t, "result:") {
		return t, true
	} else if t != "" {
		return "type:" + t, true
	}
	if w.imports[name] && !w.locals[name] {
		return name, true
	}
	return "~", true
}

// visitExcept visits n's named children, except those in the given field.
func (w *goRefWalker) visitExcept(n *sitter.Node, field string) {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c.IsNamed() && n.FieldNameForChild(i) != field {
			w.visit(c)
		}
	}
}

func (w *goRefWalker) visit(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type() {
	case "comment", "interpreted_string_literal", "raw_string_literal", "rune_literal",
		"int_literal", "float_literal", "imaginary_literal", "field_identifier",
		"package_identifier", "label_name", "nil", "true", "false", "iota", "dot", "blank_identifier":
		return
	case "call_expression":
		fn := n.ChildByFieldName("function")
		switch {
		case fn == nil:
		case fn.Type() == "identifier":
			if name := w.text(fn); !goBuiltins[name] && !w.locals[name] {
				w.emit(fn, ir.Ref{Name: name, Kind: ir.RefCall})
			}
		case fn.Type() == "selector_expression":
			operand, field := fn.ChildByFieldName("operand"), fn.ChildByFieldName("field")
			if operand != nil && field != nil {
				q, plain := w.qualifier(operand)
				w.emit(field, ir.Ref{Name: w.text(field), Qualifier: q, Kind: ir.RefCall})
				if !plain {
					w.visit(operand)
				}
			}
		default:
			w.visit(fn)
		}
		w.visit(n.ChildByFieldName("type_arguments"))
		w.visit(n.ChildByFieldName("arguments"))
		return
	case "composite_literal":
		w.typeRef(n.ChildByFieldName("type"), ir.RefNew)
		w.visit(n.ChildByFieldName("body"))
		return
	case "type_identifier":
		if name := w.text(n); !goBuiltins[name] && !w.locals[name] {
			w.emit(n, ir.Ref{Name: name, Kind: ir.RefValue})
		}
		return
	case "qualified_type":
		w.typeRef(n, ir.RefValue)
		return
	case "selector_expression":
		operand, field := n.ChildByFieldName("operand"), n.ChildByFieldName("field")
		if operand == nil || field == nil {
			return
		}
		// A value through an import, the receiver or a typed local: a
		// package-level name, a method value (p.handle), or a field, which
		// names no symbol and so resolves to nothing.
		if q, plain := w.qualifier(operand); plain && q != "~" {
			w.emit(field, ir.Ref{Name: w.text(field), Qualifier: q, Kind: ir.RefValue})
			return
		}
		w.visit(operand)
		return
	case "identifier":
		if name := w.text(n); !goBuiltins[name] && !w.locals[name] {
			w.emit(n, ir.Ref{Name: name, Kind: ir.RefValue})
		}
		return
	case "keyed_element":
		// A struct field key (Name: x) names no code; a map key may, but
		// only a constant, which is no symbol.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if i == 0 && n.NamedChildCount() > 1 && c.NamedChildCount() == 1 && c.NamedChild(0).Type() == "identifier" {
				continue
			}
			w.visit(c)
		}
		return
	case "function_declaration", "method_declaration", "type_spec", "type_alias":
		w.visitExcept(n, "name") // the declared name is no reference
		return
	case "parameter_declaration", "variadic_parameter_declaration":
		t := n.ChildByFieldName("type")
		w.visit(t)
		typ := ""
		if n.Type() == "parameter_declaration" {
			typ = namedType(t, w.p.src)
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			if n.FieldNameForChild(i) == "name" {
				w.declare(n.Child(i), typ)
			}
		}
		return
	case "type_parameter_declaration":
		for i := 0; i < int(n.ChildCount()); i++ {
			if n.FieldNameForChild(i) == "name" {
				w.declare(n.Child(i), "")
			}
		}
		w.visit(n.ChildByFieldName("type"))
		return
	case "var_spec", "const_spec":
		t, val := n.ChildByFieldName("type"), n.ChildByFieldName("value")
		w.visit(t)
		w.visit(val)
		typ := namedType(t, w.p.src)
		var names []*sitter.Node
		for i := 0; i < int(n.ChildCount()); i++ {
			if n.FieldNameForChild(i) == "name" {
				names = append(names, n.Child(i))
			}
		}
		for i, id := range names {
			nt := typ
			if nt == "" && n.Type() == "var_spec" && val != nil && int(val.NamedChildCount()) == len(names) {
				nt = literalType(val.NamedChild(i), w.p.src)
			}
			w.declare(id, nt)
		}
		return
	case "short_var_declaration":
		left, right := n.ChildByFieldName("left"), n.ChildByFieldName("right")
		w.visit(right)
		w.declareList(left, func(i int) string {
			if right == nil || left == nil {
				return ""
			}
			if left.NamedChildCount() == right.NamedChildCount() {
				if t := literalType(right.NamedChild(i), w.p.src); t != "" {
					return t
				}
				return w.resultOf(right.NamedChild(i))
			}
			if i == 0 && right.NamedChildCount() == 1 {
				return w.resultOf(right.NamedChild(0)) // x, err := pkg.F()
			}
			return ""
		})
		return
	case "range_clause", "receive_statement":
		w.visit(n.ChildByFieldName("right"))
		w.declareList(n.ChildByFieldName("left"), noType)
		return
	case "type_switch_statement":
		w.declareList(n.ChildByFieldName("alias"), noType)
		w.visitExcept(n, "alias")
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		w.visit(n.NamedChild(i))
	}
}

// typeRef records a reference to the named type of a composite literal or
// a qualified type, unwrapping generic instantiations. Element types of
// slices, maps and arrays are walked for their own type names.
func (w *goRefWalker) typeRef(t *sitter.Node, kind string) {
	if t == nil {
		return
	}
	switch t.Type() {
	case "type_identifier":
		if name := w.text(t); !goBuiltins[name] && !w.locals[name] {
			w.emit(t, ir.Ref{Name: name, Kind: kind})
		}
	case "qualified_type":
		pkg, name := t.ChildByFieldName("package"), t.ChildByFieldName("name")
		if pkg == nil || name == nil {
			return
		}
		q := w.text(pkg)
		if !w.imports[q] || w.locals[q] {
			q = "~"
		}
		w.emit(name, ir.Ref{Name: w.text(name), Qualifier: q, Kind: kind})
	case "generic_type":
		w.typeRef(t.ChildByFieldName("type"), kind)
		w.visit(t.ChildByFieldName("type_arguments"))
	default:
		w.visit(t)
	}
}

// namedType returns the named type a declaration states (T, *T, pkg.T,
// T[X]), or "" for any other type (a slice, a map, a func, an interface
// literal).
func namedType(t *sitter.Node, src []byte) string {
	for t != nil {
		switch t.Type() {
		case "type_identifier":
			if name := t.Content(src); !goBuiltins[name] {
				return name
			}
			return ""
		case "qualified_type":
			pkg, name := t.ChildByFieldName("package"), t.ChildByFieldName("name")
			if pkg == nil || name == nil {
				return ""
			}
			return pkg.Content(src) + "." + name.Content(src)
		case "pointer_type", "parenthesized_type":
			if t.NamedChildCount() == 0 {
				return ""
			}
			t = t.NamedChild(0)
		case "generic_type":
			t = t.ChildByFieldName("type")
		default:
			return ""
		}
	}
	return ""
}

// literalType returns the type of an initializer that states it: T{...},
// &T{...}, pkg.T{...}; "" for anything else.
func literalType(e *sitter.Node, src []byte) string {
	if e != nil && e.Type() == "unary_expression" {
		if op := e.ChildByFieldName("operand"); op != nil && op.Type() == "composite_literal" {
			e = op
		}
	}
	if e == nil || e.Type() != "composite_literal" {
		return ""
	}
	return namedType(e.ChildByFieldName("type"), src)
}

// receiverName returns the receiver variable of a method: s in (s *Set).
func receiverName(recv *sitter.Node, src []byte) string {
	if recv == nil {
		return ""
	}
	for i := 0; i < int(recv.NamedChildCount()); i++ {
		param := recv.NamedChild(i)
		if param.Type() != "parameter_declaration" {
			continue
		}
		if n := param.ChildByFieldName("name"); n != nil {
			return n.Content(src)
		}
	}
	return ""
}

// conventionMethods are methods the standard library and common encoders
// call through an interface whenever a value of the type reaches them
// (fmt calls String, errors call Error and Unwrap, encoders call
// MarshalJSON, sort calls Len/Less/Swap, ...). Resolve links each one from
// its receiver type, so the method is reached exactly when its type is.
var conventionMethods = map[string]bool{
	"String": true, "GoString": true, "Format": true, "Error": true, "Unwrap": true, "Is": true, "As": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "MarshalText": true, "UnmarshalText": true,
	"MarshalYAML": true, "UnmarshalYAML": true, "MarshalBinary": true, "UnmarshalBinary": true,
	"IsZero": true, "Len": true, "Less": true, "Swap": true, "ServeHTTP": true,
	"Read": true, "Write": true, "Close": true, "Scan": true, "Value": true,
}

// resolveImports sets each Go import's Module to the workspace package it
// names, and the binding of an unaliased one to that package's own name.
// The module path is inferred, not read from go.mod: an import path is
// local when it is <prefix>/<package directory> for a prefix that at least
// two of the workspace's packages are imported under, or for the prefix
// most packages are imported under. A prefix that fits fewer packages than
// another (an outside path that happens to end in a local directory name)
// is not local. Anything else, the standard library included, stays
// outside the workspace.
// @enforces reachability:INV-2
func resolveImports(modules []ir.Module) {
	pkgName := map[string]string{} // namespace -> package clause name
	// A non-test file names the package; a test file only when the
	// directory has nothing else.
	for _, testPass := range []bool{false, true} {
		for _, m := range modules {
			if m.Namespace == "" || strings.HasSuffix(m.File, "_test.go") != testPass {
				continue
			}
			if _, ok := pkgName[m.Namespace]; !ok {
				pkgName[m.Namespace] = m.Package
			}
		}
	}
	type match struct{ prefix, ns string }
	split := func(from string) (match, bool) {
		dotted := strings.ReplaceAll(from, "/", ".")
		best := ""
		for ns := range pkgName {
			if (dotted == ns || strings.HasSuffix(dotted, "."+ns)) && len(ns) > len(best) {
				best = ns
			}
		}
		if best == "" {
			return match{}, false
		}
		prefix := strings.TrimSuffix(strings.TrimSuffix(dotted, best), ".")
		return match{prefix, best}, true
	}
	seen := map[string]map[string]bool{} // prefix -> namespaces imported under it
	for _, m := range modules {
		for _, im := range m.Imports {
			if mt, ok := split(im.From); ok {
				if seen[mt.prefix] == nil {
					seen[mt.prefix] = map[string]bool{}
				}
				seen[mt.prefix][mt.ns] = true
			}
		}
	}
	best := 0
	for _, nss := range seen {
		if len(nss) > best {
			best = len(nss)
		}
	}
	for i := range modules {
		for j := range modules[i].Imports {
			im := &modules[i].Imports[j]
			mt, ok := split(im.From)
			if n := len(seen[mt.prefix]); !ok || n < 2 && n < best {
				continue
			}
			im.Module = mt.ns
			if im.Name == defaultBinding(im.From) && pkgName[mt.ns] != "" {
				im.Name = strings.TrimSuffix(pkgName[mt.ns], "_test")
			}
		}
	}
}

// linkConventionMethods adds, to each type symbol, a reference to its
// methods in conventionMethods (see there).
func linkConventionMethods(modules []ir.Module) {
	type loc struct{ mod, sym int }
	types := map[string]loc{}
	for mi := range modules {
		for si, s := range modules[mi].Symbols {
			if s.Kind == ir.KindClass {
				types[s.FQN] = loc{mi, si}
			}
		}
	}
	for mi := range modules {
		for _, s := range modules[mi].Symbols {
			if s.Kind != ir.KindMethod || !conventionMethods[s.Name] {
				continue
			}
			t, ok := types[s.EnclosingFQN]
			if !ok {
				continue
			}
			ts := &modules[t.mod].Symbols[t.sym]
			ts.Refs = append(ts.Refs, ir.Ref{Name: s.Name, Target: s.FQN, Kind: ir.RefValue, Line: ts.Line})
		}
	}
}
