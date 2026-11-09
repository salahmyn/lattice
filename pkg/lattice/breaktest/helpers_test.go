package breaktest

import (
	sitter "github.com/smacker/go-tree-sitter"
	gogrammar "github.com/smacker/go-tree-sitter/golang"
	tsts "github.com/smacker/go-tree-sitter/typescript/typescript"
)

func goGrammar() *sitter.Language { return gogrammar.GetLanguage() }

func tsGrammar() *sitter.Language { return tsts.GetLanguage() }
