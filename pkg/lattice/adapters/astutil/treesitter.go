package astutil

// @module-feature adapters

import (
	"context"
	"errors"
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// ErrParseCancelled is returned when tree-sitter gives up on a parse
// without the context being done.
var ErrParseCancelled = errors.New("tree-sitter parse cancelled")

// Parse runs a tree-sitter grammar over src. The parser is freed before
// Parse returns; the caller owns the tree and must Close it, and must not
// use any of its nodes after that.
//
// A context that can be cancelled stops the parse part way through, as the
// earlier bindings did; a context that cannot skips the progress callback.
func Parse(ctx context.Context, lang *sitter.Language, src []byte) (*sitter.Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("tree-sitter grammar: %w", err)
	}
	var tree *sitter.Tree
	if ctx.Done() == nil {
		tree = parser.Parse(src, nil)
	} else {
		n := len(src)
		tree = parser.ParseWithOptions(func(i int, _ sitter.Point) []byte {
			if i < n {
				return src[i:]
			}
			return []byte{}
		}, nil, &sitter.ParseOptions{
			ProgressCallback: func(sitter.ParseState) bool { return ctx.Err() != nil },
		})
	}
	if tree == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrParseCancelled
	}
	return tree, nil
}
