package php

// @module-feature adapters.php

import (
	"context"
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsphp "github.com/tree-sitter/tree-sitter-php/bindings/go"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// attributeKinds maps PHP attribute class names to Lattice annotation kinds.
var attributeKinds = map[string]string{
	"Feature":                 "feature",
	"Capability":              "capability",
	"EnforcesInvariant":       "enforces_invariant",
	"Verifies":                "verifies",
	"VerifiesCapability":      "verifies_capability",
	"DependsOnFeature":        "depends_on_feature",
	"Role":                    "role",
	"SuppressesInvariant":     "suppresses_invariant",
	"Surface":                 "surface",
	"Error":                   "error",
	"ModuleFeature":           "module_feature",
	"ModuleEnforcesInvariant": "module_enforces_invariant",
	"ModuleDependsOnFeature":  "module_depends_on_feature",
}

// phpLanguage is the grammar; it is static C data and needs no Close.
var phpLanguage = sitter.NewLanguage(tsphp.LanguagePHP())

// parse turns PHP source into an IR module.
func (a *Adapter) parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	mod := ir.Module{File: path, Language: a.Name()}

	tree, err := astutil.Parse(ctx, phpLanguage, source)
	if err != nil {
		return mod, err
	}
	defer tree.Close()

	p := &phpParser{src: source, mod: &mod, testFile: isTestFile(path)}
	p.walk(tree.RootNode(), "")
	return mod, nil
}

type phpParser struct {
	src      []byte
	mod      *ir.Module
	testFile bool
}

// walk recursively processes statements, threading the current namespace.
func (p *phpParser) walk(node *sitter.Node, namespace string) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		n := node.NamedChild(uint(i))
		switch n.Kind() {
		case "namespace_definition":
			ns := p.fieldText(n, "name")
			if body := n.ChildByFieldName("body"); body != nil {
				p.walk(body, ns)
			} else {
				namespace = ns // semicolon form: applies to following siblings
			}
		case "attribute_list":
			// Free-floating attributes are module-level annotations.
			for _, ann := range p.attributes(n) {
				p.mod.ModuleAnnotations = append(p.mod.ModuleAnnotations, ann)
			}
		case "function_definition":
			p.addSymbol(p.fieldText(n, "name"), ir.KindFunction, namespace, "", n)
		case "class_declaration":
			p.addClass(n, namespace, ir.KindClass)
		case "trait_declaration":
			p.addClass(n, namespace, ir.KindTrait)
		case "interface_declaration":
			p.addClass(n, namespace, ir.KindInterface)
		case "expression_statement", "ERROR":
			// Recurse: error-tolerant parsing may nest declarations here.
			p.walk(n, namespace)
		}
	}
}

// addClass records a class/trait/interface symbol and its methods.
// @enforces adapters.php:INV-1
func (p *phpParser) addClass(n *sitter.Node, namespace string, kind ir.SymbolKind) {
	name := p.fieldText(n, "name")
	if name == "" {
		return
	}
	fqn := qualify(namespace, name)
	sym := ir.Symbol{
		Name: name, FQN: fqn, Kind: kind,
		File: p.mod.File, Line: line(n), EndLine: endLine(n), IsTest: p.testFile,
		Annotations: p.declAttributes(n),
		BaseClasses: p.baseClasses(n, namespace),
		Exported:    true, // top-level classes/traits/interfaces are public
	}
	p.mod.Symbols = append(p.mod.Symbols, sym)

	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(uint(i))
		if c.Kind() == "method_declaration" {
			mname := p.fieldText(c, "name")
			if mname == "" {
				continue
			}
			p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
				Name: mname, FQN: fqn + "::" + mname, Kind: ir.KindMethod,
				File: p.mod.File, Line: line(c), EndLine: endLine(c), EnclosingFQN: fqn,
				Annotations: p.declAttributes(c),
				IsTest:      p.testFile,
				Exported:    phpMethodExported(c, p.src),
			})
		}
	}
}

// addSymbol records a single (non-class) symbol.
func (p *phpParser) addSymbol(name string, kind ir.SymbolKind, namespace, enclosing string, n *sitter.Node) {
	if name == "" {
		return
	}
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: qualify(namespace, name), Kind: kind,
		File: p.mod.File, Line: line(n), EndLine: endLine(n), EnclosingFQN: enclosing,
		Annotations: p.declAttributes(n), IsTest: p.testFile,
		Exported: true, // top-level functions are public
	})
}

// phpMethodExported reports whether a method is part of the public surface:
// true unless it carries a private/protected visibility modifier.
func phpMethodExported(n *sitter.Node, src []byte) bool {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(uint(i))
		if c.Kind() == "visibility_modifier" {
			switch c.Utf8Text(src) {
			case "private", "protected":
				return false
			}
		}
	}
	return true
}

// declAttributes collects attributes attached to a declaration node.
func (p *phpParser) declAttributes(n *sitter.Node) []ir.Annotation {
	var anns []ir.Annotation
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(uint(i)); c.Kind() == "attribute_list" {
			anns = append(anns, p.attributes(c)...)
		}
	}
	return anns
}

// baseClasses returns extends targets plus used traits (trait attributes
// propagate to consuming classes — an explicit Lattice semantic).
func (p *phpParser) baseClasses(n *sitter.Node, namespace string) []string {
	var bases []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(uint(i))
		if c.Kind() == "base_clause" {
			for j := 0; j < int(c.NamedChildCount()); j++ {
				bases = append(bases, qualify(namespace, c.NamedChild(uint(j)).Utf8Text(p.src)))
			}
		}
	}
	if body := n.ChildByFieldName("body"); body != nil {
		for i := 0; i < int(body.NamedChildCount()); i++ {
			c := body.NamedChild(uint(i))
			if c.Kind() == "use_declaration" {
				for j := 0; j < int(c.NamedChildCount()); j++ {
					tn := c.NamedChild(uint(j))
					if t := tn.Kind(); t == "name" || t == "qualified_name" {
						bases = append(bases, qualify(namespace, tn.Utf8Text(p.src)))
					}
				}
			}
		}
	}
	return bases
}

// attributes turns an attribute_list node into annotations.
func (p *phpParser) attributes(list *sitter.Node) []ir.Annotation {
	var anns []ir.Annotation
	var collect func(n *sitter.Node)
	collect = func(n *sitter.Node) {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(uint(i))
			switch c.Kind() {
			case "attribute_group":
				collect(c)
			case "attribute":
				if ann, ok := p.attribute(c); ok {
					anns = append(anns, ann)
				}
			}
		}
	}
	collect(list)
	return anns
}

// attribute extracts one annotation from an attribute node.
func (p *phpParser) attribute(n *sitter.Node) (ir.Annotation, bool) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		nameNode = firstChildOfType(n, "name")
	}
	if nameNode == nil {
		return ir.Annotation{}, false
	}
	short := nameNode.Utf8Text(p.src)
	if idx := strings.LastIndex(short, "\\"); idx >= 0 {
		short = short[idx+1:]
	}
	kind, ok := attributeKinds[short]
	if !ok {
		return ir.Annotation{}, false
	}
	ann := ir.Annotation{Kind: kind, Line: line(n)}
	args := n.ChildByFieldName("parameters")
	if args == nil {
		args = firstChildOfType(n, "arguments")
	}
	if args == nil {
		return ann, true
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		arg := args.NamedChild(uint(i))
		if arg.Kind() != "argument" {
			continue
		}
		name := ""
		if nm := arg.ChildByFieldName("name"); nm != nil {
			name = nm.Utf8Text(p.src)
		}
		val := lastNamedChild(arg)
		if val == nil {
			continue
		}
		if !isPHPString(val.Kind()) {
			p.mod.Diagnostics = append(p.mod.Diagnostics, ir.Diagnostic{
				Line: line(arg), Code: "ANNOTATION_ARG_NOT_LITERAL",
				Message: "attribute argument is not a string literal",
			})
			continue
		}
		sv := p.stringValue(val)
		if name != "" {
			if ann.Kwargs == nil {
				ann.Kwargs = map[string]interface{}{}
			}
			ann.Kwargs[name] = sv
		} else {
			ann.Args = append(ann.Args, sv)
		}
	}
	return ann, true
}

func (p *phpParser) stringValue(n *sitter.Node) string {
	if c := firstChildOfType(n, "string_content"); c != nil {
		return c.Utf8Text(p.src)
	}
	return strings.Trim(n.Utf8Text(p.src), "\"'")
}

func (p *phpParser) fieldText(n *sitter.Node, field string) string {
	if c := n.ChildByFieldName(field); c != nil {
		return c.Utf8Text(p.src)
	}
	return ""
}

// --- helpers ---

func line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }

func endLine(n *sitter.Node) int { return int(n.EndPosition().Row) + 1 }

// @enforces adapters.php:INV-1
func qualify(namespace, name string) string {
	name = strings.TrimPrefix(name, "\\")
	if namespace == "" {
		return name
	}
	return strings.TrimSuffix(namespace, "\\") + "\\" + name
}

func isPHPString(t string) bool {
	return t == "string" || t == "encapsed_string" || t == "string_value"
}

func firstChildOfType(n *sitter.Node, t string) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(uint(i)); c.Kind() == t {
			return c
		}
	}
	return nil
}

func lastNamedChild(n *sitter.Node) *sitter.Node {
	count := int(n.NamedChildCount())
	if count == 0 {
		return nil
	}
	return n.NamedChild(uint(count - 1))
}

func isTestFile(path string) bool {
	if astutil.IsTestPath(path) {
		return true
	}
	base := filepath.Base(path)
	return strings.HasSuffix(base, "Test.php") || strings.HasSuffix(base, "_test.php")
}
