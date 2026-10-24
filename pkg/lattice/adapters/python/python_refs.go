package python

// @module-feature reachability

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// collectRefs records the module's imports and the raw references of every
// symbol and of the top-level code, for the core's call graph
// (docs/rnd/0009): calls (f(), obj.m(), self.m(), a class called to build
// it), .rpc('fn') and .table('t') / .from_('t') of supabase-py, and a value
// named without a call when it is an import or a top-level name of the
// module. The body of `if __name__ == "__main__":` is a root of its own
// (<module>.<main>). A class reaches its dunder methods (__init__,
// __str__, ...), which Python calls by convention.
// @enforces reachability:INV-1
func (p *pyParser) collectRefs(root *sitter.Node) {
	m := p.mod
	m.RefsKnown, m.Namespace = true, p.modulePath
	names := map[string]bool{}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		p.topLevel(root.NamedChild(i), names)
	}
	for i := range m.Symbols {
		s := &m.Symbols[i]
		if s.Kind != ir.KindClass {
			continue
		}
		for _, o := range m.Symbols {
			if o.EnclosingFQN == s.FQN && strings.HasPrefix(o.Name, "__") && strings.HasSuffix(o.Name, "__") {
				s.Refs = append(s.Refs, ir.Ref{Name: o.Name, Target: o.FQN, Kind: ir.RefValue, Line: o.Line})
			}
		}
	}
	w := &pyRefWalker{p: p, names: names}
	var top, main []ir.Ref
	mainLine := 0
	w.emit = func(n *sitter.Node, r ir.Ref) {
		r.Line = line(n)
		off := n.StartByte()
		if idx := p.owners.At(off); idx >= 0 {
			m.Symbols[idx].Refs = append(m.Symbols[idx].Refs, r)
			return
		}
		if w.main != nil && off >= w.main[0] && off < w.main[1] {
			main = append(main, r)
			return
		}
		top = append(top, r)
	}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		c := root.NamedChild(i)
		if c.Type() == "if_statement" && isMainGuard(c, p.src) && w.main == nil {
			w.main = []uint32{c.StartByte(), c.EndByte()}
			mainLine = line(c)
		}
		w.visit(c)
	}
	m.Nodes = append(m.Nodes, ir.Node{FQN: ir.TopFQN(p.modulePath, p.testFile), Kind: ir.NodeTop,
		IsTest: p.testFile, Line: 1, Refs: top})
	if w.main != nil {
		m.Nodes = append(m.Nodes, ir.Node{FQN: p.modulePath + ".<main>", Kind: ir.NodeTop,
			Root: "main", IsTest: p.testFile, Line: mainLine, Refs: main})
	}
}

// isMainGuard reports `if __name__ == "__main__":`.
func isMainGuard(n *sitter.Node, src []byte) bool {
	cond := n.ChildByFieldName("condition")
	if cond == nil {
		return false
	}
	t := strings.Join(strings.Fields(cond.Content(src)), "")
	return t == `__name__=="__main__"` || t == `__name__=='__main__'` || t == `"__main__"==__name__` || t == `'__main__'==__name__`
}

// topLevel records one top-level statement's imports and bound names.
func (p *pyParser) topLevel(n *sitter.Node, names map[string]bool) {
	m := p.mod
	switch n.Type() {
	case "import_statement":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case "dotted_name":
				// import a.b binds a.b as written (and a); refs say a.b.f().
				mod := c.Content(p.src)
				names[strings.SplitN(mod, ".", 2)[0]] = true
				m.Imports = append(m.Imports, ir.Import{Name: mod, From: mod, Line: line(n)})
			case "aliased_import":
				mod, alias := p.fieldText(c, "name"), p.fieldText(c, "alias")
				names[alias] = true
				m.Imports = append(m.Imports, ir.Import{Name: alias, From: mod, Line: line(n)})
			}
		}
	case "import_from_statement":
		from := p.absoluteModule(n.ChildByFieldName("module_name"))
		if from == "" {
			return
		}
		if firstChildOfType(n, "wildcard_import") != nil {
			m.Imports = append(m.Imports, ir.Import{From: from, Member: "*", Export: "*", Line: line(n)})
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			if n.FieldNameForChild(i) != "name" {
				continue
			}
			c := n.Child(i)
			member, local := c.Content(p.src), c.Content(p.src)
			if c.Type() == "aliased_import" {
				member, local = p.fieldText(c, "name"), p.fieldText(c, "alias")
			}
			names[local] = true
			m.Imports = append(m.Imports, ir.Import{Name: local, From: from, Member: member, Line: line(n)})
		}
	case "function_definition", "class_definition":
		names[p.fieldText(n, "name")] = true
	case "decorated_definition":
		if d := n.ChildByFieldName("definition"); d != nil {
			names[p.fieldText(d, "name")] = true
		}
	case "expression_statement":
		if a := firstChildOfType(n, "assignment"); a != nil {
			if l := a.ChildByFieldName("left"); l != nil && l.Type() == "identifier" {
				names[l.Content(p.src)] = true
			}
		}
	}
}

// absoluteModule turns a from-import's module into a dotted path: a
// relative one (from .x, from ..) against this file's package, an absolute
// one as written (Resolve matches it against the workspace's modules).
func (p *pyParser) absoluteModule(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	if n.Type() != "relative_import" {
		return n.Content(p.src)
	}
	dots, rest := 0, ""
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		switch c.Type() {
		case "import_prefix":
			dots = strings.Count(c.Content(p.src), ".")
		case "dotted_name":
			rest = c.Content(p.src)
		}
	}
	pkg := strings.Split(p.modulePath, ".")
	pkg = pkg[:len(pkg)-1] // the file's package
	for i := 1; i < dots && len(pkg) > 0; i++ {
		pkg = pkg[:len(pkg)-1]
	}
	parts := pkg
	if rest != "" {
		parts = append(append([]string(nil), pkg...), rest)
	}
	return "." + strings.Join(parts, ".") // a leading dot marks it workspace-relative
}

type pyRefWalker struct {
	p     *pyParser
	names map[string]bool
	main  []uint32
	emit  func(n *sitter.Node, r ir.Ref)
}

func (w *pyRefWalker) text(n *sitter.Node) string { return n.Content(w.p.src) }

// receiver classifies an attribute's object: self and cls are "this", a
// dotted chain of names stays as written, anything else is "~".
func (w *pyRefWalker) receiver(obj *sitter.Node) (string, bool) {
	switch obj.Type() {
	case "identifier":
		if t := w.text(obj); t == "self" || t == "cls" {
			return "this", true
		}
		return w.text(obj), true
	case "attribute":
		parts := strings.Split(w.text(obj), ".")
		for _, s := range parts {
			if s == "" || strings.ContainsAny(s, "()[] \n\t") {
				return "~", false
			}
		}
		return strings.Join(parts, "."), true
	}
	return "~", false
}

func (w *pyRefWalker) visit(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Type() {
	case "comment", "string", "integer", "float", "import_statement", "import_from_statement",
		"keyword_argument_name":
		return
	case "call":
		fn, args := n.ChildByFieldName("function"), n.ChildByFieldName("arguments")
		switch {
		case fn == nil:
		case fn.Type() == "identifier":
			w.emit(fn, ir.Ref{Name: w.text(fn), Kind: ir.RefCall})
		case fn.Type() == "attribute":
			obj, attr := fn.ChildByFieldName("object"), fn.ChildByFieldName("attribute")
			if obj == nil || attr == nil {
				break
			}
			name := w.text(attr)
			lit, isLit := firstStringArg(args, w.p)
			switch {
			case name == "rpc" && args != nil && args.NamedChildCount() > 0:
				if !isLit {
					lit = ""
				}
				w.emit(attr, ir.Ref{Name: lit, Kind: ir.RefRPC})
				w.visit(obj)
			case (name == "table" || name == "from_") && isLit:
				w.emit(attr, ir.Ref{Name: lit, Kind: ir.RefTable})
				w.visit(obj)
			default:
				q, plain := w.receiver(obj)
				w.emit(attr, ir.Ref{Name: name, Qualifier: q, Kind: ir.RefCall})
				if !plain {
					w.visit(obj)
				}
			}
		default:
			w.visit(fn)
		}
		w.visit(args)
		return
	case "identifier":
		if t := w.text(n); w.names[t] {
			w.emit(n, ir.Ref{Name: t, Kind: ir.RefValue})
		}
		return
	case "attribute":
		obj, attr := n.ChildByFieldName("object"), n.ChildByFieldName("attribute")
		if obj != nil && attr != nil && obj.Type() == "identifier" && w.names[w.text(obj)] {
			w.emit(attr, ir.Ref{Name: w.text(attr), Qualifier: w.text(obj), Kind: ir.RefValue})
			return
		}
		w.visit(obj)
		return
	case "function_definition", "class_definition":
		for i := 0; i < int(n.ChildCount()); i++ {
			if c := n.Child(i); c.IsNamed() && n.FieldNameForChild(i) != "name" && n.FieldNameForChild(i) != "parameters" {
				w.visit(c)
			}
		}
		if params := n.ChildByFieldName("parameters"); params != nil {
			for i := 0; i < int(params.NamedChildCount()); i++ {
				w.visit(params.NamedChild(i).ChildByFieldName("value")) // defaults are code
			}
		}
		return
	case "keyword_argument":
		w.visit(n.ChildByFieldName("value"))
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		w.visit(n.NamedChild(i))
	}
}

// firstStringArg returns the first argument when it is a string literal.
func firstStringArg(args *sitter.Node, p *pyParser) (string, bool) {
	if args == nil || args.NamedChildCount() == 0 {
		return "", false
	}
	if a := args.NamedChild(0); a.Type() == "string" {
		return p.stringValue(a), true
	}
	return "", false
}

// Resolve sets each import's Module to the workspace module it names: a
// relative import by path, an absolute one exactly or by the one module
// whose path ends with it (a src/ layout), a package by its __init__.py.
// `from pkg import mod` of a submodule binds the submodule itself.
// @enforces reachability:INV-2
func (*Adapter) Resolve(modules []ir.Module) []ir.Module {
	ns := map[string]bool{}
	for _, m := range modules {
		if m.Namespace != "" {
			ns[m.Namespace] = true
		}
	}
	find := func(dotted string) string {
		relative := strings.HasPrefix(dotted, ".")
		dotted = strings.TrimPrefix(dotted, ".")
		for _, c := range []string{dotted, dotted + ".__init__"} {
			if ns[c] {
				return c
			}
		}
		if relative || dotted == "" {
			return ""
		}
		found := ""
		for n := range ns {
			for _, c := range []string{dotted, dotted + ".__init__"} {
				if strings.HasSuffix(n, "."+c) {
					if found != "" && found != n {
						return ""
					}
					found = n
				}
			}
		}
		return found
	}
	symbols := map[string]bool{}
	for _, m := range modules {
		for _, s := range m.Symbols {
			symbols[s.FQN] = true
		}
	}
	for i := range modules {
		for j := range modules[i].Imports {
			im := &modules[i].Imports[j]
			target := find(im.From)
			im.Module = target
			if im.Member == "" || im.Member == "*" || target != "" && symbols[target+"."+im.Member] {
				continue
			}
			// from pkg import mod, where pkg defines no mod: the submodule.
			if sub := find(strings.TrimSuffix(im.From, ".") + "." + im.Member); sub != "" {
				im.Module, im.Member = sub, ""
			}
		}
	}
	return modules
}
