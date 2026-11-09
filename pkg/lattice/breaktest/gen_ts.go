package breaktest

// @module-feature breaktest

import (
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tstsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	tsts "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// tsFunctionNodes are the nodes that hold a function with a body: function
// declarations, methods, and function or arrow expressions.
var tsFunctionNodes = map[string]bool{
	"function_declaration": true, "generator_function_declaration": true,
	"method_definition": true, "function": true, "function_expression": true,
	"generator_function": true, "arrow_function": true,
}

// generateTS inserts `if (true) { return undefined as any; }` (TypeScript) or
// `if (true) { return undefined; }` (JavaScript) after the opening brace of a
// function with a block body. Expression-bodied arrows, constructors and
// setters read unknown.
func generateTS(file string, src []byte, sym Symbol) (Break, error) {
	lang := tstsx.GetLanguage()
	isTS := true
	switch strings.ToLower(filepath.Ext(file)) {
	case ".ts", ".mts", ".cts":
		lang = tsts.GetLanguage()
	case ".js", ".jsx", ".mjs", ".cjs":
		isTS = false
	}
	tree, err := parse(lang, src)
	if err != nil {
		return Break{}, noBreak("%v", err)
	}
	defer tree.Close()

	type cand struct {
		n    *sitter.Node
		body *sitter.Node
		name string
	}
	var cands []cand
	var expressionBody bool
	walk(tree.RootNode(), func(n *sitter.Node) bool {
		if !tsFunctionNodes[n.Type()] {
			return true
		}
		body := n.ChildByFieldName("body")
		if body == nil || !within(sym.Line, tsHeaderRow(n), body) {
			return true
		}
		name := tsFunctionName(n, src)
		if body.Type() != "statement_block" {
			if name == sym.Name {
				expressionBody = true
			}
			return true
		}
		cands = append(cands, cand{n, body, name})
		return true
	})
	var pick *cand
	for i := range cands {
		if cands[i].name == sym.Name {
			pick = &cands[i]
			break
		}
	}
	if pick == nil && len(cands) == 1 {
		pick = &cands[0]
	}
	if pick == nil {
		if expressionBody {
			return Break{}, noBreak("%s has an expression body, not a block", sym.Name)
		}
		return Break{}, noBreak("no function %s with a block body at line %d (kind %s)", sym.Name, sym.Line, sym.Kind)
	}
	if pick.n.Type() == "method_definition" {
		if pick.name == "constructor" {
			return Break{}, noBreak("constructor: no generated break; add a script")
		}
		for i := 0; i < int(pick.n.ChildCount()); i++ {
			if pick.n.Child(i).Type() == "set" {
				return Break{}, noBreak("setter: no generated break; add a script")
			}
		}
	}
	stmt := "if (true) { return undefined; }"
	if isTS {
		stmt = "if (true) { return undefined as any; }"
	}
	return Break{Offset: int(pick.body.StartByte()) + 1, Text: " " + stmt + " "}, nil
}

// tsFunctionName is the name a function is known by: its own, or what it is
// assigned to (const f = () => {}, { f: function () {} }, f = function () {}).
func tsFunctionName(n *sitter.Node, src []byte) string {
	if name := n.ChildByFieldName("name"); name != nil {
		return text(name, src)
	}
	p := n.Parent()
	if p == nil {
		return ""
	}
	switch p.Type() {
	case "variable_declarator", "public_field_definition":
		return text(p.ChildByFieldName("name"), src)
	case "pair":
		return text(p.ChildByFieldName("key"), src)
	case "assignment_expression":
		l := text(p.ChildByFieldName("left"), src)
		return l[strings.LastIndex(l, ".")+1:]
	}
	return ""
}

// tsHeaderRow is the first row of the declaration a function belongs to,
// counting an export, a const declaration or a class field around it.
func tsHeaderRow(n *sitter.Node) int {
	row := int(n.StartPoint().Row)
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Type() {
		case "export_statement", "lexical_declaration", "variable_declaration",
			"variable_declarator", "public_field_definition", "pair",
			"assignment_expression", "expression_statement":
			row = int(p.StartPoint().Row)
		default:
			return row
		}
	}
	return row
}
