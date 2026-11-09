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
// function with a block body. An arrow with an expression body, `(a) => expr`,
// gets its body wrapped as `(true ? (undefined as any) : (expr))` (no `as
// any` in JavaScript), so the expression is still referenced. Constructors,
// setters, classes and consts that are not functions read unknown.
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
		expr bool // an arrow with an expression body
	}
	var cands []cand
	walk(tree.RootNode(), func(n *sitter.Node) bool {
		if !tsFunctionNodes[n.Type()] {
			return true
		}
		body := n.ChildByFieldName("body")
		if body == nil || !within(sym.Line, tsHeaderRow(n), body) {
			return true
		}
		cands = append(cands, cand{n, body, tsFunctionName(n, src), body.Type() != "statement_block"})
		return true
	})
	var pick *cand
	for i := range cands {
		if cands[i].name == sym.Name {
			pick = &cands[i]
			break
		}
	}
	// An unnamed function is taken as the symbol's only when it is the one
	// block-bodied candidate; an expression body is never guessed at.
	if pick == nil && len(cands) == 1 && !cands[0].expr {
		pick = &cands[0]
	}
	if pick == nil {
		if sym.Kind == "const" || sym.Kind == "variable" {
			return Break{}, noBreak("%s is not a function; add a script", sym.Name)
		}
		if sym.Kind == "class" {
			return Break{}, noBreak("%s is a class; add a script", sym.Name)
		}
		return Break{}, noBreak("no function %s at line %d (kind %s)", sym.Name, sym.Line, sym.Kind)
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
	if pick.expr {
		// (true ? undefined : (expr)): the expression stays referenced and
		// the whole is any, so the code still type-checks.
		head, ret := "(true ? ", "undefined"
		if isTS {
			ret = "(undefined as any)"
		}
		return Break{
			Offset: int(pick.body.StartByte()), Text: head + ret + " : (",
			More: []Break{{Offset: int(pick.body.EndByte()), Text: "))"}},
		}, nil
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
