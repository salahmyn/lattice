package typescript

// @module-feature reachability

import (
	"path"
	"strings"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// notTables are receivers whose .from(...) builds a value, not a query on a
// database table (Array.from("abc"), Buffer.from("x")).
var notTables = map[string]bool{
	"Array": true, "Buffer": true, "Object": true, "Set": true, "Map": true, "Promise": true,
	"Uint8Array": true, "Int8Array": true, "Uint16Array": true, "Int16Array": true, "Uint32Array": true,
	"Int32Array": true, "Float32Array": true, "Float64Array": true, "BigInt64Array": true,
	"BigUint64Array": true, "Observable": true, "Rx": true, "rxjs": true, "Iterator": true,
}

// collectRefs records the module's imports, re-exports and default
// export, and the raw references of every symbol and of the top-level
// code, for the core's call graph (docs/rnd/0009):
//
//   - calls (f(), obj.m(), this.m()), new C(), JSX components (<C />);
//   - .rpc('fn') as an rpc ref (a computed name gives an rpc ref with no
//     name), .from('t') as a table ref, .schema('s') as their qualifier;
//   - a value named without a call (a callback, a default argument) when it
//     names an import or a top-level declaration of the module;
//   - import('./x') as a use of its default export.
//
// The callback of a top-level serve() or Deno.serve() call is a root of
// its own (<module>.<serve>), so the request handler is reached without an
// entry point being declared.
// @enforces reachability:INV-1
func (p *tsParser) collectRefs(root *sitter.Node) {
	m := p.mod
	m.RefsKnown, m.Namespace = true, p.modulePath
	names := map[string]bool{} // import bindings and top-level declarations
	for i := 0; i < int(root.NamedChildCount()); i++ {
		p.topLevel(root.NamedChild(i), names)
	}
	for i := range m.Symbols {
		s := &m.Symbols[i]
		if s.Kind != ir.KindClass {
			continue
		}
		// new C() runs C's constructor.
		for _, o := range m.Symbols {
			if o.EnclosingFQN == s.FQN && o.Name == "constructor" {
				s.Refs = append(s.Refs, ir.Ref{Name: o.Name, Target: o.FQN, Kind: ir.RefValue, Line: o.Line})
			}
		}
	}
	w := &tsRefWalker{p: p, names: names}
	var top, served []ir.Ref
	w.emit = func(n *sitter.Node, r ir.Ref) {
		r.Line = line(n)
		off := n.StartByte()
		if idx := p.owners.At(off); idx >= 0 {
			m.Symbols[idx].Refs = append(m.Symbols[idx].Refs, r)
			return
		}
		for _, s := range w.serve {
			if off >= s[0] && off < s[1] {
				served = append(served, r)
				return
			}
		}
		top = append(top, r)
	}
	w.visit(root)
	m.Nodes = append(m.Nodes, ir.Node{FQN: ir.TopFQN(p.modulePath, p.testFile), Kind: ir.NodeTop,
		IsTest: p.testFile, Line: 1, Refs: top})
	if len(served) > 0 || len(w.serve) > 0 {
		m.Nodes = append(m.Nodes, ir.Node{FQN: p.modulePath + ".<serve>", Kind: ir.NodeServe,
			Root: "serve", IsTest: p.testFile, Line: w.serveLine, Refs: served})
	}
}

// topLevel records one top-level statement's imports, exports and declared
// names.
func (p *tsParser) topLevel(n *sitter.Node, names map[string]bool) {
	m := p.mod
	switch n.Type() {
	case "import_statement":
		from := p.stringText(n.ChildByFieldName("source"))
		if from == "" {
			return
		}
		clause := firstNamed(n, "import_clause")
		if clause == nil {
			m.Imports = append(m.Imports, ir.Import{From: from, Line: line(n)}) // import './side-effect'
			return
		}
		for i := 0; i < int(clause.NamedChildCount()); i++ {
			c := clause.NamedChild(i)
			switch c.Type() {
			case "identifier":
				names[c.Content(p.src)] = true
				m.Imports = append(m.Imports, ir.Import{Name: c.Content(p.src), From: from, Member: "default", Line: line(n)})
			case "namespace_import":
				if id := firstNamed(c, "identifier"); id != nil {
					names[id.Content(p.src)] = true
					m.Imports = append(m.Imports, ir.Import{Name: id.Content(p.src), From: from, Line: line(n)})
				}
			case "named_imports":
				for j := 0; j < int(c.NamedChildCount()); j++ {
					spec := c.NamedChild(j)
					if spec.Type() != "import_specifier" {
						continue
					}
					member := p.fieldText(spec, "name")
					local := member
					if a := p.fieldText(spec, "alias"); a != "" {
						local = a
					}
					names[local] = true
					m.Imports = append(m.Imports, ir.Import{Name: local, From: from, Member: member, Line: line(spec)})
				}
			}
		}
	case "export_statement":
		from := p.stringText(n.ChildByFieldName("source"))
		clause := firstNamed(n, "export_clause")
		switch {
		case from != "" && clause == nil:
			// export * from './x', export * as ns from './x'
			if ns := firstNamed(n, "namespace_export"); ns != nil {
				m.Imports = append(m.Imports, ir.Import{From: from, Export: lastIdent(ns, p.src), Line: line(n)})
			} else {
				m.Imports = append(m.Imports, ir.Import{From: from, Member: "*", Export: "*", Line: line(n)})
			}
		case clause != nil:
			for j := 0; j < int(clause.NamedChildCount()); j++ {
				spec := clause.NamedChild(j)
				if spec.Type() != "export_specifier" {
					continue
				}
				member := p.fieldText(spec, "name")
				exported := member
				if a := p.fieldText(spec, "alias"); a != "" {
					exported = a
				}
				im := ir.Import{From: from, Member: member, Export: exported, Line: line(spec)}
				if from == "" {
					im.Module = p.modulePath // export { local as name }
					if exported == "default" {
						m.DefaultExport = member
						continue
					}
				}
				m.Imports = append(m.Imports, im)
			}
		default:
			if isDefaultExport(n) {
				if v := n.ChildByFieldName("value"); v != nil && v.Type() == "identifier" {
					m.DefaultExport = v.Content(p.src)
				} else if d := n.ChildByFieldName("declaration"); d != nil {
					m.DefaultExport = p.fieldText(d, "name")
				}
			}
			if d := n.ChildByFieldName("declaration"); d != nil {
				p.declaredNames(d, names)
			}
		}
	default:
		p.declaredNames(n, names)
		p.requireImports(n, names)
	}
}

// requireImports records const x = require('./x') and const { a } =
// require('./x') at the top level.
func (p *tsParser) requireImports(n *sitter.Node, names map[string]bool) {
	if n.Type() != "lexical_declaration" && n.Type() != "variable_declaration" {
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		d := n.NamedChild(i)
		val := d.ChildByFieldName("value")
		if d.Type() != "variable_declarator" || val == nil || val.Type() != "call_expression" {
			continue
		}
		fn := val.ChildByFieldName("function")
		if fn == nil || fn.Type() != "identifier" || fn.Content(p.src) != "require" {
			continue
		}
		from := p.firstStringArg(val.ChildByFieldName("arguments"))
		if from == "" {
			continue
		}
		p.boundRequires[val.StartByte()] = true
		name := d.ChildByFieldName("name")
		switch {
		case name == nil:
		case name.Type() == "identifier":
			p.mod.Imports = append(p.mod.Imports, ir.Import{Name: name.Content(p.src), From: from, Line: line(d)})
		case name.Type() == "object_pattern":
			for j := 0; j < int(name.NamedChildCount()); j++ {
				c := name.NamedChild(j)
				switch c.Type() {
				case "shorthand_property_identifier_pattern":
					p.mod.Imports = append(p.mod.Imports, ir.Import{Name: c.Content(p.src), From: from, Member: c.Content(p.src), Line: line(d)})
				case "pair_pattern":
					k, v := c.ChildByFieldName("key"), c.ChildByFieldName("value")
					if k != nil && v != nil && v.Type() == "identifier" {
						p.mod.Imports = append(p.mod.Imports, ir.Import{Name: v.Content(p.src), From: from, Member: k.Content(p.src), Line: line(d)})
					}
				}
			}
		}
	}
}

// declaredNames records the names a top-level declaration binds.
func (p *tsParser) declaredNames(n *sitter.Node, names map[string]bool) {
	switch n.Type() {
	case "function_declaration", "generator_function_declaration", "class_declaration", "abstract_class_declaration":
		if name := p.fieldText(n, "name"); name != "" {
			names[name] = true
		}
	case "lexical_declaration", "variable_declaration":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if d := n.NamedChild(i); d.Type() == "variable_declarator" {
				collectPatternNames(d.ChildByFieldName("name"), p.src, names)
			}
		}
	}
}

// collectPatternNames adds every identifier a binding pattern binds.
func collectPatternNames(n *sitter.Node, src []byte, names map[string]bool) {
	if n == nil {
		return
	}
	switch n.Type() {
	case "identifier", "shorthand_property_identifier_pattern":
		names[n.Content(src)] = true
		return
	case "pair_pattern":
		collectPatternNames(n.ChildByFieldName("value"), src, names)
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectPatternNames(n.NamedChild(i), src, names)
	}
}

// tsRefWalker walks the tree for references.
type tsRefWalker struct {
	p         *tsParser
	names     map[string]bool
	emit      func(n *sitter.Node, r ir.Ref)
	serve     [][2]uint32 // byte ranges of top-level serve() arguments
	serveLine int
}

func (w *tsRefWalker) text(n *sitter.Node) string { return n.Content(w.p.src) }

// receiver classifies the object of a member call: "this", an identifier
// or a dotted chain of identifiers as written, or "~" for an expression
// (a call result, an index). plain is false for an expression, whose own
// references are still walked.
func (w *tsRefWalker) receiver(obj *sitter.Node) (string, bool) {
	switch obj.Type() {
	case "this":
		return "this", true
	case "identifier":
		return w.text(obj), true
	case "member_expression":
		if chain := memberChain(obj, w.p.src); chain != nil {
			return strings.Join(chain, "."), true
		}
	}
	return "~", false
}

// schemaOf returns s for a receiver built by .schema('s'), else "".
func (w *tsRefWalker) schemaOf(obj *sitter.Node) string {
	if obj == nil || obj.Type() != "call_expression" {
		return ""
	}
	fn := obj.ChildByFieldName("function")
	if fn == nil || fn.Type() != "member_expression" {
		return ""
	}
	if prop := fn.ChildByFieldName("property"); prop != nil && w.text(prop) == "schema" {
		return w.p.firstStringArg(obj.ChildByFieldName("arguments"))
	}
	return w.schemaOf(fn.ChildByFieldName("object"))
}

// literalFirstArg returns the first argument as a string literal; ok is
// false when there is a first argument that is not one.
func (w *tsRefWalker) literalFirstArg(args *sitter.Node) (string, bool, bool) {
	if args == nil || args.NamedChildCount() == 0 {
		return "", false, false
	}
	first := args.NamedChild(0)
	if first.Type() != "string" {
		return "", false, true
	}
	return w.p.firstStringArg(args), true, true
}

func (w *tsRefWalker) visitChildren(n *sitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		w.visit(n.NamedChild(i))
	}
}

func (w *tsRefWalker) visit(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type() {
	case "comment", "string", "number", "regex", "property_identifier", "import_statement",
		"type_annotation", "type_arguments", "type_parameters", "type_alias_declaration",
		"interface_declaration", "ambient_declaration", "hash_bang_line", "statement_identifier",
		"private_property_identifier", "shorthand_property_identifier_pattern":
		return
	case "template_string":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if c := n.NamedChild(i); c.Type() == "template_substitution" {
				w.visitChildren(c)
			}
		}
		return
	case "export_statement":
		if n.ChildByFieldName("source") != nil {
			return // a re-export, recorded as an import
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if c := n.NamedChild(i); c.Type() != "export_clause" {
				w.visit(c)
			}
		}
		return
	case "expression_statement":
		if call := innerCall(n); call != nil && w.isServe(call) && w.p.owners.At(call.StartByte()) < 0 {
			if args := call.ChildByFieldName("arguments"); args != nil {
				w.serve = append(w.serve, [2]uint32{args.StartByte(), args.EndByte()})
				if w.serveLine == 0 {
					w.serveLine = line(call)
				}
				w.visit(args)
				return
			}
		}
	case "call_expression":
		w.call(n)
		return
	case "new_expression":
		if c := n.ChildByFieldName("constructor"); c != nil {
			switch c.Type() {
			case "identifier":
				w.emit(c, ir.Ref{Name: w.text(c), Kind: ir.RefNew})
			case "member_expression":
				obj, prop := c.ChildByFieldName("object"), c.ChildByFieldName("property")
				if obj != nil && prop != nil {
					q, plain := w.receiver(obj)
					w.emit(prop, ir.Ref{Name: w.text(prop), Qualifier: q, Kind: ir.RefNew})
					if !plain {
						w.visit(obj)
					}
				}
			default:
				w.visit(c)
			}
		}
		w.visit(n.ChildByFieldName("arguments"))
		return
	case "jsx_opening_element", "jsx_self_closing_element":
		if name := n.ChildByFieldName("name"); name != nil {
			switch name.Type() {
			case "identifier":
				if t := w.text(name); t != "" && unicode.IsUpper(rune(t[0])) {
					w.emit(name, ir.Ref{Name: t, Kind: ir.RefJSX})
				}
			case "member_expression", "nested_identifier":
				parts := strings.Split(w.text(name), ".")
				if len(parts) >= 2 {
					w.emit(name, ir.Ref{Name: parts[len(parts)-1], Qualifier: strings.Join(parts[:len(parts)-1], "."), Kind: ir.RefJSX})
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			if n.FieldNameForChild(i) == "attribute" {
				w.visit(n.Child(i))
			}
		}
		return
	case "jsx_closing_element":
		return
	case "identifier", "shorthand_property_identifier":
		if t := w.text(n); w.names[t] {
			w.emit(n, ir.Ref{Name: t, Kind: ir.RefValue})
		}
		return
	case "member_expression":
		// A value through a namespace import (ns.handler passed along).
		obj, prop := n.ChildByFieldName("object"), n.ChildByFieldName("property")
		if obj != nil && prop != nil && obj.Type() == "identifier" && w.names[w.text(obj)] {
			w.emit(prop, ir.Ref{Name: w.text(prop), Qualifier: w.text(obj), Kind: ir.RefValue})
			return
		}
		w.visit(obj)
		if prop != nil && prop.Type() != "property_identifier" && prop.Type() != "private_property_identifier" {
			w.visit(prop)
		}
		return
	case "variable_declarator":
		w.visit(n.ChildByFieldName("value")) // the name binds, it names nothing
		return
	case "function_declaration", "generator_function_declaration", "class_declaration",
		"abstract_class_declaration", "method_definition":
		for i := 0; i < int(n.ChildCount()); i++ {
			if c := n.Child(i); c.IsNamed() && n.FieldNameForChild(i) != "name" {
				w.visit(c)
			}
		}
		return
	case "formal_parameters":
		// Parameters bind names; a default value is code.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			prm := n.NamedChild(i)
			w.visit(prm.ChildByFieldName("value"))
		}
		return
	case "pair":
		w.visit(n.ChildByFieldName("value"))
		if k := n.ChildByFieldName("key"); k != nil && k.Type() == "computed_property_name" {
			w.visit(k)
		}
		return
	}
	w.visitChildren(n)
}

// dynamicImport records import('./x') or an unbound require('./x'): a
// module loaded at run time, of which any export may be used. A computed
// specifier is a reference with no name, which the core counts as
// unresolved.
func (w *tsRefWalker) dynamicImport(call, args *sitter.Node) {
	lit, isLit, hasArg := w.literalFirstArg(args)
	if !hasArg {
		return
	}
	if !isLit {
		if args.NamedChild(0).Type() == "template_string" && args.NamedChild(0).NamedChildCount() == 0 {
			lit = strings.Trim(w.text(args.NamedChild(0)), "`") // import(`./x`)
		} else {
			w.emit(call, ir.Ref{Kind: ir.RefImport})
			return
		}
	}
	w.p.mod.Imports = append(w.p.mod.Imports, ir.Import{From: lit, Dynamic: true, Line: line(call)})
	w.emit(call, ir.Ref{Name: lit, Kind: ir.RefImport})
}

// isServe reports a call of serve(...) or Deno.serve(...).
func (w *tsRefWalker) isServe(call *sitter.Node) bool {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return false
	}
	chain := memberChain(fn, w.p.src)
	return len(chain) == 1 && chain[0] == "serve" || len(chain) == 2 && chain[0] == "Deno" && chain[1] == "serve"
}

func (w *tsRefWalker) call(n *sitter.Node) {
	fn := n.ChildByFieldName("function")
	args := n.ChildByFieldName("arguments")
	switch {
	case fn == nil:
	case fn.Type() == "import":
		w.dynamicImport(n, args)
		w.visit(args)
		return
	case fn.Type() == "identifier":
		t := w.text(fn)
		if t == "require" {
			if !w.p.boundRequires[n.StartByte()] {
				w.dynamicImport(n, args)
			}
			return
		}
		w.emit(fn, ir.Ref{Name: t, Kind: ir.RefCall})
		// A project's own wrapper named rpc ('rpc("fn", args)' around a
		// PostgREST call) names the database function as .rpc() does.
		if lit, isLit, hasArg := w.literalFirstArg(args); t == "rpc" && hasArg && isLit {
			w.emit(fn, ir.Ref{Name: lit, Kind: ir.RefRPC})
		}
	case fn.Type() == "member_expression":
		obj, prop := fn.ChildByFieldName("object"), fn.ChildByFieldName("property")
		if obj == nil || prop == nil {
			break
		}
		name := w.text(prop)
		lit, isLit, hasArg := w.literalFirstArg(args)
		switch {
		case name == "rpc" && hasArg:
			if !isLit {
				lit = ""
			}
			w.emit(prop, ir.Ref{Name: lit, Qualifier: w.schemaOf(obj), Kind: ir.RefRPC})
			w.visit(obj)
		case name == "from" && isLit && !(obj.Type() == "identifier" && notTables[w.text(obj)]):
			w.emit(prop, ir.Ref{Name: lit, Qualifier: w.schemaOf(obj), Kind: ir.RefTable})
			w.visit(obj)
		default:
			q, plain := w.receiver(obj)
			if obj.Type() == "super" {
				q = "this"
			}
			w.emit(prop, ir.Ref{Name: name, Qualifier: q, Kind: ir.RefCall})
			if !plain {
				w.visit(obj)
			}
		}
	default:
		w.visit(fn)
	}
	w.visit(args)
}

// --- helpers ---

// stringText returns the unquoted text of a string node.
func (p *tsParser) stringText(n *sitter.Node) string {
	if n == nil || n.Type() != "string" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == "string_fragment" {
			b.WriteString(c.Content(p.src))
		}
	}
	return b.String()
}

func firstNamed(n *sitter.Node, typ string) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == typ {
			return c
		}
	}
	return nil
}

func lastIdent(n *sitter.Node, src []byte) string {
	out := ""
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == "identifier" {
			out = c.Content(src)
		}
	}
	return out
}

// isDefaultExport reports export default ...
func isDefaultExport(n *sitter.Node) bool {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); !c.IsNamed() && c.Type() == "default" {
			return true
		}
	}
	return false
}

// resolveExts are the extensions an import specifier may leave out or
// write differently from the source file (./wallet.js for wallet.ts).
var resolveExts = []string{".d.ts", ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

// aliasPrefixes are the common path aliases of a bundler or tsconfig
// (@/components/x, ~/lib/y, #/z), resolved by a unique path suffix.
var aliasPrefixes = []string{"@/", "~/", "#/"}

// Resolve sets each import's Module to the workspace module its specifier
// names: a relative specifier by path (with or without an extension, or a
// directory's index file), an aliased one (@/, ~/, #/) by the one module
// whose path ends with it. A bare package name stays outside the
// workspace.
// @enforces reachability:INV-2
func (*Adapter) Resolve(modules []ir.Module) []ir.Module {
	byStem := map[string]string{} // file path without extension -> namespace
	for _, m := range modules {
		if m.Namespace == "" {
			continue
		}
		byStem[stripExt(m.File)] = m.Namespace
	}
	lookup := func(p string) string {
		p = path.Clean(p)
		if ns, ok := byStem[p]; ok {
			return ns
		}
		if ns, ok := byStem[stripExt(p)]; ok {
			return ns
		}
		if ns, ok := byStem[p+"/index"]; ok {
			return ns
		}
		return ""
	}
	suffix := func(rest string) string {
		found := ""
		for stem, ns := range byStem {
			for _, cand := range []string{rest, rest + "/index", stripExt(rest)} {
				if stem == cand || strings.HasSuffix(stem, "/"+cand) {
					if found != "" && found != ns {
						return ""
					}
					found = ns
				}
			}
		}
		return found
	}
	for i := range modules {
		dir := path.Dir(modules[i].File)
		for j := range modules[i].Imports {
			im := &modules[i].Imports[j]
			if im.Module != "" || im.From == "" {
				continue
			}
			switch {
			case strings.HasPrefix(im.From, "./") || strings.HasPrefix(im.From, "../") || im.From == "." || im.From == "..":
				im.Module = lookup(path.Join(dir, im.From))
			default:
				for _, pre := range aliasPrefixes {
					if strings.HasPrefix(im.From, pre) {
						im.Module = suffix(strings.TrimPrefix(im.From, pre))
					}
				}
			}
		}
	}
	return modules
}

func stripExt(p string) string {
	for _, ext := range resolveExts {
		if strings.HasSuffix(p, ext) {
			return strings.TrimSuffix(p, ext)
		}
	}
	return p
}
