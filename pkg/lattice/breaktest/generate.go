// Package breaktest answers "does a verifier notice when the code it checks
// is broken?" for each promise (invariant). It breaks an enforcer, on a
// throwaway git worktree, and runs only that promise's verifiers: a verifier
// that passes both before and after the break cannot tell the code is
// broken. Nothing here edits the user's working tree.
package breaktest

// @module-feature breaktest

import (
	"context"
	"errors"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
)

// Symbol names the function a break is generated for. Line is the 1-based
// line the graph gives the symbol, which the generators locate it by.
type Symbol struct {
	Name string
	Kind string
	Line int
}

// Break is one insertion into a source file: Text goes in at byte Offset and
// the rest of the file stays as it was, so imports and variables stay used
// and the code still compiles.
type Break struct {
	Offset int
	Text   string
}

// Apply returns src with the break inserted.
func (b Break) Apply(src []byte) []byte {
	out := make([]byte, 0, len(src)+len(b.Text))
	out = append(out, src[:b.Offset]...)
	out = append(out, b.Text...)
	return append(out, src[b.Offset:]...)
}

// NoBreak is why no break could be generated. The outcome is unknown, never
// killed or survived.
type NoBreak struct{ Reason string }

func (e *NoBreak) Error() string { return e.Reason }

func noBreak(format string, a ...any) error {
	return &NoBreak{Reason: fmt.Sprintf(format, a...)}
}

// Generate builds the "early return" break for a function: an early return
// at the start of its body. Languages without a generator (SQL, PHP, ...)
// and symbols that are not plain function bodies return a *NoBreak.
// @enforces breaktest:INV-1
func Generate(language, file string, src []byte, sym Symbol) (Break, error) {
	switch language {
	case "go":
		return generateGo(src, sym)
	case "typescript", "javascript":
		return generateTS(file, src, sym)
	case "python":
		return generatePython(src, sym)
	}
	return Break{}, noBreak("no generated break for %s; add a script", language)
}

// parse runs a tree-sitter grammar over src.
func parse(lang *sitter.Language, src []byte) (*sitter.Tree, error) {
	p := sitter.NewParser()
	p.SetLanguage(lang)
	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, err
	}
	if tree.RootNode().HasError() {
		// A tree with errors may still hold the function, but its offsets
		// cannot be trusted to give code that compiles.
		tree.Close()
		return nil, errors.New("source does not parse cleanly")
	}
	return tree, nil
}

// walk visits n and its descendants depth first, skipping a node's children
// when visit returns false.
func walk(n *sitter.Node, visit func(*sitter.Node) bool) {
	if n == nil || !visit(n) {
		return
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		walk(n.Child(i), visit)
	}
}

func text(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

// within reports whether the 1-based line falls in the header of a function,
// from the first row of its declaration to the row its body opens on.
func within(line, hdrRow int, body *sitter.Node) bool {
	return hdrRow <= line-1 && line-1 <= int(body.StartPoint().Row)
}
