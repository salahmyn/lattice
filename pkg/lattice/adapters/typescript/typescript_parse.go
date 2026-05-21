package typescript

import (
	"context"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsts "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// tagKinds maps JSDoc tag names to Lattice annotation kinds.
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

// parse turns TypeScript/JavaScript source into an IR module.
func (a *Adapter) parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	mod := ir.Module{File: path, Language: a.Name()}

	parser := sitter.NewParser()
	parser.SetLanguage(tsts.GetLanguage())
	tree, err := parser.ParseCtx(ctx, nil, source)
	if err != nil {
		return mod, err
	}
	defer tree.Close()

	p := &tsParser{src: source, mod: &mod, modulePath: astutil.ModulePath(path), testFile: isTestFile(path)}
	p.walk(tree.RootNode())
	return mod, nil
}

type tsParser struct {
	src        []byte
	mod        *ir.Module
	modulePath string
	testFile   bool
}

// walk processes the top level, threading pending JSDoc annotations onto the
// next declaration.
func (p *tsParser) walk(root *sitter.Node) {
	var pending []ir.Annotation
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		switch n.Type() {
		case "comment":
			if anns := p.parseJSDoc(n); anns != nil {
				pending = append(pending, anns...)
			}
		case "import_statement":
			// imports may sit between the module-header JSDoc and code.
		case "export_statement":
			if decl := exportedDecl(n); decl != nil {
				p.handleDecl(decl, pending)
			}
			pending = nil
		case "function_declaration", "class_declaration", "abstract_class_declaration",
			"lexical_declaration", "variable_declaration", "interface_declaration":
			p.handleDecl(n, pending)
			pending = nil
		default:
			pending = nil
		}
	}
}

// handleDecl records a declaration's symbol(s) with the given annotations.
func (p *tsParser) handleDecl(n *sitter.Node, anns []ir.Annotation) {
	switch n.Type() {
	case "function_declaration":
		p.addSymbol(p.fieldText(n, "name"), ir.KindFunction, "", n, anns)
	case "class_declaration", "abstract_class_declaration":
		p.addClass(n, anns)
	case "interface_declaration":
		p.addSymbol(p.fieldText(n, "name"), ir.KindInterface, "", n, anns)
	case "lexical_declaration", "variable_declaration":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			d := n.NamedChild(i)
			if d.Type() != "variable_declarator" {
				continue
			}
			val := d.ChildByFieldName("value")
			if val == nil {
				continue
			}
			switch val.Type() {
			case "arrow_function", "function", "function_expression":
				p.addSymbol(p.fieldText(d, "name"), ir.KindFunction, "", d, anns)
			}
		}
	}
}

// addClass records a class symbol and its method symbols.
func (p *tsParser) addClass(n *sitter.Node, anns []ir.Annotation) {
	name := p.fieldText(n, "name")
	if name == "" {
		return
	}
	fqn := p.modulePath + "." + name
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: ir.KindClass,
		File: p.mod.File, Line: line(n), Annotations: anns,
		BaseClasses: p.extends(n), IsTest: p.testFile,
	})

	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	var pending []ir.Annotation
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		switch c.Type() {
		case "comment":
			if a := p.parseJSDoc(c); a != nil {
				pending = append(pending, a...)
			}
		case "method_definition":
			mname := p.fieldText(c, "name")
			p.addSymbol(mname, ir.KindMethod, fqn, c, pending)
			pending = nil
		default:
			pending = nil
		}
	}
}

// addSymbol appends one symbol to the module.
func (p *tsParser) addSymbol(name string, kind ir.SymbolKind, enclosingFQN string, n *sitter.Node, anns []ir.Annotation) {
	if name == "" {
		return
	}
	fqn := p.modulePath + "." + name
	if enclosingFQN != "" {
		fqn = enclosingFQN + "." + name
	}
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: kind,
		File: p.mod.File, Line: line(n), EnclosingFQN: enclosingFQN,
		Annotations: anns, IsTest: p.testFile || hasVerify(anns),
	})
}

// extends returns the base classes from a class heritage clause.
func (p *tsParser) extends(n *sitter.Node) []string {
	var bases []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() != "class_heritage" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			ec := c.NamedChild(j)
			if ec.Type() == "extends_clause" {
				for k := 0; k < int(ec.NamedChildCount()); k++ {
					id := ec.NamedChild(k)
					if t := id.Type(); t == "identifier" || t == "member_expression" {
						bases = append(bases, p.modulePath+"."+id.Content(p.src))
					}
				}
			}
		}
	}
	return bases
}

// parseJSDoc turns a /** ... */ comment into annotations. Module-level tags
// are routed straight to the module; the rest are returned for the next
// declaration.
func (p *tsParser) parseJSDoc(n *sitter.Node) []ir.Annotation {
	text := n.Content(p.src)
	if !strings.HasPrefix(text, "/**") {
		return nil
	}
	startLine := line(n)
	var symbolAnns []ir.Annotation
	for _, raw := range strings.Split(text, "\n") {
		ln := strings.TrimSpace(raw)
		ln = strings.TrimPrefix(ln, "*")
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "@") {
			continue
		}
		fields := strings.SplitN(ln[1:], " ", 2)
		kind, ok := tagKinds[fields[0]]
		if !ok {
			continue
		}
		ann := ir.Annotation{Kind: kind, Line: startLine}
		if len(fields) == 2 {
			p.applyArgs(&ann, strings.TrimSpace(fields[1]))
		}
		if strings.HasPrefix(kind, "module_") {
			p.mod.ModuleAnnotations = append(p.mod.ModuleAnnotations, ann)
		} else {
			symbolAnns = append(symbolAnns, ann)
		}
	}
	return symbolAnns
}

// applyArgs splits a JSDoc tag's argument text into annotation args. A
// "reason:" segment becomes a reason kwarg (used by @suppresses).
func (p *tsParser) applyArgs(ann *ir.Annotation, text string) {
	if idx := strings.Index(text, "reason:"); idx >= 0 {
		reason := strings.TrimSpace(text[idx+len("reason:"):])
		text = strings.TrimSpace(text[:idx])
		ann.Kwargs = map[string]interface{}{"reason": reason}
	}
	for _, part := range strings.Split(text, ",") {
		if v := strings.TrimSpace(part); v != "" {
			ann.Args = append(ann.Args, v)
		}
	}
}

func (p *tsParser) fieldText(n *sitter.Node, field string) string {
	if c := n.ChildByFieldName(field); c != nil {
		return c.Content(p.src)
	}
	return ""
}

// --- helpers ---

func line(n *sitter.Node) int { return int(n.StartPoint().Row) + 1 }

// exportedDecl unwraps the declaration inside an export_statement.
func exportedDecl(n *sitter.Node) *sitter.Node {
	if d := n.ChildByFieldName("declaration"); d != nil {
		return d
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		switch c.Type() {
		case "function_declaration", "class_declaration", "abstract_class_declaration",
			"lexical_declaration", "variable_declaration", "interface_declaration":
			return c
		}
	}
	return nil
}

func hasVerify(anns []ir.Annotation) bool {
	for _, a := range anns {
		if a.Kind == "verifies" || a.Kind == "verifies_capability" {
			return true
		}
	}
	return false
}

// isTestFile reports whether a TS/JS path is test code.
func isTestFile(path string) bool {
	if astutil.IsTestPath(path) {
		return true
	}
	base := filepath.Base(path)
	for _, suf := range []string{".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx",
		".test.js", ".test.jsx", ".spec.js", ".spec.jsx"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}
