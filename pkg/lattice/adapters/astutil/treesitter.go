package astutil

// @module-feature adapters

import (
	"context"
	"errors"
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// ErrParseFailed is returned when tree-sitter returns no tree.
var ErrParseFailed = errors.New("tree-sitter returned no tree")

// Parse runs a tree-sitter grammar over src. The parser is freed before
// Parse returns; the caller owns the tree and must Close it, and must not
// use any of its nodes after that.
//
// A done context stops the work before the parse, so cancellation is per
// file (a file parses in milliseconds). Parse does not cancel part way
// through a file: the binding's ParseWithOptions registers the options it
// is given and never releases them, which would leak a little memory on
// every parse.
func Parse(ctx context.Context, lang *sitter.Language, src []byte) (*sitter.Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("tree-sitter grammar: %w", err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, ErrParseFailed
	}
	return tree, nil
}
