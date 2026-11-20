package breaktest

// @module-feature breaktest

import (
	"bytes"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// generatePython inserts `return None` as the first statement of a function
// body, indented to match, ahead of any docstring.
func generatePython(src []byte, sym Symbol) (Break, error) {
	tree, err := parse(pythonLanguage, src)
	if err != nil {
		return Break{}, noBreak("%v", err)
	}
	defer tree.Close()

	var found *sitter.Node
	walk(tree.RootNode(), func(n *sitter.Node) bool {
		if found != nil {
			return false
		}
		if n.Kind() != "function_definition" {
			return true
		}
		body := n.ChildByFieldName("body")
		row := int(n.StartPosition().Row)
		if p := n.Parent(); p != nil && p.Kind() == "decorated_definition" {
			row = int(p.StartPosition().Row)
		}
		if body != nil && text(n.ChildByFieldName("name"), src) == sym.Name && within(sym.Line, row, body) {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		return Break{}, noBreak("no function %s at line %d (kind %s)", sym.Name, sym.Line, sym.Kind)
	}
	first := found.ChildByFieldName("body").NamedChild(0)
	if first == nil {
		return Break{}, noBreak("empty body")
	}
	start := int(first.StartByte())
	lineStart := bytes.LastIndexByte(src[:start], '\n') + 1
	indent := src[lineStart:start]
	if len(bytes.TrimSpace(indent)) != 0 {
		return Break{}, noBreak("body shares the def line; add a script")
	}
	return Break{Offset: start, Text: "return None\n" + string(indent)}, nil
}
