package breaktest

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func goGrammar() *sitter.Language { return goLanguage }

func tsGrammar() *sitter.Language { return tsLanguage }
