package python

import (
	"context"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tspython "github.com/smacker/go-tree-sitter/python"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// knownAnnotations is the set of Lattice decorator names the adapter
// recognizes. Other decorators are ignored.
var knownAnnotations = map[string]bool{
	"feature": true, "feature_capability": true, "capability": true,
	"enforces_invariant": true, "verifies": true, "verifies_capability": true,
	"depends_on_feature": true, "role": true, "suppresses_invariant": true,
	"module_feature": true, "module_enforces_invariant": true,
	"module_depends_on_feature": true,
}

// parse turns Python source into an IR module using tree-sitter.
func (a *Adapter) parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	mod := ir.Module{File: path, Language: a.Name()}

	parser := sitter.NewParser()
	parser.SetLanguage(tspython.GetLanguage())
	tree, err := parser.ParseCtx(ctx, nil, source)
	if err != nil {
		return mod, err
	}
	defer tree.Close()

	root := tree.RootNode()
	modulePath := astutil.ModulePath(path)
	testFile := isTestFile(path)

	p := &pyParser{src: source, mod: &mod, modulePath: modulePath, testFile: testFile}
	p.walkModule(root)
	return mod, nil
}

type pyParser struct {
	src        []byte
	mod        *ir.Module
	modulePath string
	testFile   bool
}

// walkModule walks the top level: module-annotation calls, functions, classes.
func (p *pyParser) walkModule(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		switch n.Type() {
		case "expression_statement":
			if call := firstChildOfType(n, "call"); call != nil {
				if ann, ok := p.callAnnotation(call); ok && strings.HasPrefix(ann.Kind, "module_") {
					p.mod.ModuleAnnotations = append(p.mod.ModuleAnnotations, ann)
				}
			}
		case "decorated_definition":
			p.handleDecorated(n, "", "")
		case "function_definition":
			p.addFunction(n, nil, "", "")
		case "class_definition":
			p.addClass(n, nil)
		}
	}
}

// handleDecorated processes a decorated function or class.
func (p *pyParser) handleDecorated(n *sitter.Node, enclosingFQN, _ string) {
	var anns []ir.Annotation
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() == "decorator" {
			if ann, ok := p.decoratorAnnotation(c); ok {
				anns = append(anns, ann)
			}
		}
	}
	def := n.ChildByFieldName("definition")
	if def == nil {
		return
	}
	switch def.Type() {
	case "function_definition":
		p.addFunction(def, anns, enclosingFQN, "")
	case "class_definition":
		p.addClass(def, anns)
	}
}

// addClass records a class symbol and its methods.
func (p *pyParser) addClass(n *sitter.Node, anns []ir.Annotation) {
	name := p.fieldText(n, "name")
	if name == "" {
		return
	}
	fqn := p.modulePath + "." + name
	sym := ir.Symbol{
		Name: name, FQN: fqn, Kind: ir.KindClass,
		File: p.mod.File, Line: line(n), Annotations: anns,
		BaseClasses: p.baseClasses(n), IsTest: p.testFile,
	}
	p.mod.Symbols = append(p.mod.Symbols, sym)

	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		switch c.Type() {
		case "decorated_definition":
			p.handleDecorated(c, fqn, name)
		case "function_definition":
			p.addFunction(c, nil, fqn, name)
		}
	}
}

// addFunction records a function or method symbol.
func (p *pyParser) addFunction(n *sitter.Node, anns []ir.Annotation, enclosingFQN, className string) {
	name := p.fieldText(n, "name")
	if name == "" {
		return
	}
	kind := ir.KindFunction
	fqn := p.modulePath + "." + name
	if enclosingFQN != "" {
		kind = ir.KindMethod
		fqn = enclosingFQN + "." + name
	}
	isTest := p.testFile || strings.HasPrefix(name, "test_") || hasVerifyAnnotation(anns)
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: kind,
		File: p.mod.File, Line: line(n), EnclosingFQN: enclosingFQN,
		Annotations: anns, IsTest: isTest,
	})
}

// baseClasses returns the superclass names of a class definition.
func (p *pyParser) baseClasses(n *sitter.Node) []string {
	sup := n.ChildByFieldName("superclasses")
	if sup == nil {
		return nil
	}
	var bases []string
	for i := 0; i < int(sup.NamedChildCount()); i++ {
		c := sup.NamedChild(i)
		if t := c.Type(); t == "identifier" || t == "attribute" {
			// Resolve same-module bases to a FQN; leave others bare.
			name := c.Content(p.src)
			bases = append(bases, p.modulePath+"."+name)
		}
	}
	return bases
}

// decoratorAnnotation extracts an annotation from a decorator node.
func (p *pyParser) decoratorAnnotation(dec *sitter.Node) (ir.Annotation, bool) {
	for i := 0; i < int(dec.NamedChildCount()); i++ {
		c := dec.NamedChild(i)
		switch c.Type() {
		case "call":
			return p.callAnnotation(c)
		case "identifier":
			name := c.Content(p.src)
			if knownAnnotations[name] {
				return ir.Annotation{Kind: name, Line: line(dec)}, true
			}
		}
	}
	return ir.Annotation{}, false
}

// callAnnotation extracts an annotation from a call expression.
func (p *pyParser) callAnnotation(call *sitter.Node) (ir.Annotation, bool) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "identifier" {
		return ir.Annotation{}, false
	}
	name := fn.Content(p.src)
	if !knownAnnotations[name] {
		return ir.Annotation{}, false
	}
	ann := ir.Annotation{Kind: name, Line: line(call)}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return ann, true
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		arg := args.NamedChild(i)
		switch arg.Type() {
		case "string":
			ann.Args = append(ann.Args, p.stringValue(arg))
		case "list":
			ann.Args = append(ann.Args, p.listValues(arg))
		case "keyword_argument":
			k := arg.ChildByFieldName("name")
			val := arg.ChildByFieldName("value")
			if k == nil || val == nil {
				continue
			}
			if ann.Kwargs == nil {
				ann.Kwargs = map[string]interface{}{}
			}
			switch val.Type() {
			case "string":
				ann.Kwargs[k.Content(p.src)] = p.stringValue(val)
			case "list":
				ann.Kwargs[k.Content(p.src)] = p.listValues(val)
			default:
				p.diag(arg, "annotation keyword argument is not a string literal")
			}
		default:
			p.diag(arg, "annotation argument is not a string literal")
		}
	}
	return ann, true
}

// stringValue returns the content of a Python string literal.
func (p *pyParser) stringValue(n *sitter.Node) string {
	if content := firstChildOfType(n, "string_content"); content != nil {
		return content.Content(p.src)
	}
	return strings.Trim(n.Content(p.src), "\"'")
}

// listValues returns the string elements of a list literal.
func (p *pyParser) listValues(n *sitter.Node) []string {
	var out []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() == "string" {
			out = append(out, p.stringValue(c))
		} else {
			p.diag(c, "annotation list element is not a string literal")
		}
	}
	return out
}

func (p *pyParser) fieldText(n *sitter.Node, field string) string {
	if c := n.ChildByFieldName(field); c != nil {
		return c.Content(p.src)
	}
	return ""
}

func (p *pyParser) diag(n *sitter.Node, msg string) {
	p.mod.Diagnostics = append(p.mod.Diagnostics, ir.Diagnostic{
		Line: line(n), Code: "ANNOTATION_ARG_NOT_LITERAL", Message: msg,
	})
}

// --- shared small helpers ---

func line(n *sitter.Node) int { return int(n.StartPoint().Row) + 1 }

func firstChildOfType(n *sitter.Node, t string) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == t {
			return c
		}
	}
	return nil
}

func hasVerifyAnnotation(anns []ir.Annotation) bool {
	for _, a := range anns {
		if a.Kind == "verifies" || a.Kind == "verifies_capability" {
			return true
		}
	}
	return false
}

// isTestFile reports whether a Python file path is test code.
func isTestFile(path string) bool {
	if astutil.IsTestPath(path) {
		return true
	}
	base := filepath.Base(path)
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
}
