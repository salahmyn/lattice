package breaktest

// @module-feature breaktest

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	gogrammar "github.com/smacker/go-tree-sitter/golang"
)

// generateGo inserts `if true { var r0 T0; ...; return r0, ... }` after the
// opening brace of a function or method, or `if true { return }` when it has
// no results or named results. The result types come from the tree-sitter
// node, so generics and qualified types keep working.
func generateGo(src []byte, sym Symbol) (Break, error) {
	tree, err := parse(gogrammar.GetLanguage(), src)
	if err != nil {
		return Break{}, noBreak("%v", err)
	}
	defer tree.Close()

	var found *sitter.Node
	root := tree.RootNode()
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		if n.Type() != "function_declaration" && n.Type() != "method_declaration" {
			continue
		}
		body := n.ChildByFieldName("body")
		if body == nil || text(n.ChildByFieldName("name"), src) != sym.Name ||
			!within(sym.Line, int(n.StartPoint().Row), body) {
			continue
		}
		found = n
		break
	}
	if found == nil {
		return Break{}, noBreak("no function %s at line %d (kind %s)", sym.Name, sym.Line, sym.Kind)
	}
	body := found.ChildByFieldName("body")
	return Break{Offset: int(body.StartByte()) + 1, Text: " " + goEarlyReturn(found, src) + "; "}, nil
}

func goEarlyReturn(fn *sitter.Node, src []byte) string {
	res := fn.ChildByFieldName("result")
	if res == nil {
		return "if true { return }"
	}
	var types []string
	if res.Type() != "parameter_list" {
		types = []string{text(res, src)}
	} else {
		for i := 0; i < int(res.NamedChildCount()); i++ {
			p := res.NamedChild(i)
			if p.Type() != "parameter_declaration" {
				continue
			}
			if p.ChildByFieldName("name") != nil {
				// Named results: a bare return gives their zero values.
				return "if true { return }"
			}
			types = append(types, text(p.ChildByFieldName("type"), src))
		}
	}
	if len(types) == 0 {
		return "if true { return }"
	}
	var decl, names []string
	for i, t := range types {
		decl = append(decl, fmt.Sprintf("var r%d %s", i, t))
		names = append(names, fmt.Sprintf("r%d", i))
	}
	return "if true { " + strings.Join(decl, "; ") + "; return " + strings.Join(names, ", ") + " }"
}
