package breaktest

import (
	sitter "github.com/smacker/go-tree-sitter"
	gogrammar "github.com/smacker/go-tree-sitter/golang"
)

func goGrammar() *sitter.Language { return gogrammar.GetLanguage() }
