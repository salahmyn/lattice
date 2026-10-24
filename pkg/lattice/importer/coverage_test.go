package importer

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func coverageGraph() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		Modules: []schema.GraphModule{
			{File: "src/cart.ts", Feature: "cart", SymbolFQNs: []string{"src.cart.total", "src.cart.round"}},
			{File: "src/util.ts", SymbolFQNs: []string{"src.util.pad"}},
			{File: "src/pay.ts", SymbolFQNs: []string{"src.pay.charge"}},
			{File: "src/cart.test.ts", SymbolFQNs: []string{"src.cart.test.test.adds"}},
			{File: "src/types.ts", SymbolFQNs: []string{"src.types.Row"}}, // types only: not production
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "src.cart.total", Kind: "function", Feature: "cart"},
			{FQN: "src.cart.round", Kind: "function", Feature: "cart"},
			{FQN: "src.util.pad", Kind: "function"},
			{FQN: "src.types.Row", Kind: "interface"},
			{FQN: "src.pay.charge", Kind: "const", Feature: "payments"},
		},
		Tests: []schema.GraphSymbol{{FQN: "src.cart.test.test.adds", IsTest: true}},
	}
}

// Without any import session, documentation and attribution come from the graph.
func TestCoverageFromGraph(t *testing.T) {
	kg := coverageGraph()
	doc := ComputeDocumentationGraph(kg, nil, nil)
	if doc.TotalSymbols != 5 || doc.DocumentedSymbols != 3 {
		t.Errorf("documentation = %+v, want 3/5", doc)
	}
	at := ComputeAttribution(kg)
	if at.TotalModules != 3 || at.AttributedModules != 2 || len(at.Unattributed) != 1 || at.Unattributed[0] != "src/util.ts" {
		t.Errorf("attribution = %+v, want 2/3 with src/util.ts unattributed", at)
	}
	// An accepted import candidate also documents its symbols.
	cf := CandidatesFile{Candidates: []Candidate{{ID: "c1", Symbols: []string{"src.util.pad"}}}}
	doc = ComputeDocumentationGraph(kg, &cf, map[string]string{"c1": DecisionAccepted})
	if doc.DocumentedSymbols != 4 {
		t.Errorf("accepted candidate not counted: %+v", doc)
	}
}

// Ambient declarations and tool configuration are not product code.
func TestAttributionSkipsDeclarationsAndConfig(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Modules: []schema.GraphModule{
			{File: "src/vite-env.d.ts", SymbolFQNs: []string{"a"}},
			{File: "vite.config.ts", SymbolFQNs: []string{"b"}},
			{File: "apps/web/playwright.config.mjs", SymbolFQNs: []string{"c"}},
			{File: "src/configure.ts", SymbolFQNs: []string{"d"}},
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "a", Kind: "const"}, {FQN: "b", Kind: "const"},
			{FQN: "c", Kind: "const"}, {FQN: "d", Kind: "function"},
		},
	}
	at := ComputeAttribution(kg)
	if at.TotalModules != 1 || len(at.Unattributed) != 1 || at.Unattributed[0] != "src/configure.ts" {
		t.Errorf("attribution = %+v, want only src/configure.ts counted", at)
	}
}

// A module with no behaviour symbols but top-level code (an entry file that
// only renders, a dictionary of plain consts) is production code: attributed
// by its header, or listed as unattributed without one.
func TestAttributionCountsTopLevelCode(t *testing.T) {
	kg := schema.KnowledgeGraph{Modules: []schema.GraphModule{
		{File: "src/main.tsx", HasCode: true, Feature: "app.shell"},
		{File: "src/i18n.ts", HasCode: true},
		{File: "src/types.ts"}, // no code, no symbols
	}}
	at := ComputeAttribution(kg)
	if at.TotalModules != 2 || at.AttributedModules != 1 || len(at.Unattributed) != 1 || at.Unattributed[0] != "src/i18n.ts" {
		t.Errorf("attribution = %+v, want 1/2 with src/i18n.ts unattributed", at)
	}
}
