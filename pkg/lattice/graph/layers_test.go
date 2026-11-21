package graph

import (
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func layerOf(kg schema.KnowledgeGraph, fqn string) (string, string) {
	for _, s := range kg.Symbols {
		if s.FQN == fqn {
			return s.Layer, s.LayerSource
		}
	}
	return "?", "?"
}

// A symbol's layer comes from an adapter fact or a declared rule; a symbol no
// rule matches is unknown, with no source.
// @verifies graph:INV-6
func TestAssignLayersFactRuleOrUnknown(t *testing.T) {
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{
		{FQN: "web.cart.total", File: "web/cart.ts", Language: "typescript"},
		{FQN: "sql.policy.public.orders.own", File: "db/migrations/1.sql", Language: "postgres"},
		{FQN: "lib.helper", File: "lib/helper.go", Language: "go"},
	}}
	rules := []schema.LayerRule{{Glob: "web/**", Layer: "ui"}}
	if v := AssignLayers(&kg, rules); len(v) != 0 {
		t.Fatalf("violations: %+v", v)
	}
	for fqn, want := range map[string][2]string{
		"web.cart.total":               {"ui", "rule"},
		"sql.policy.public.orders.own": {"data", "adapter"},
		"lib.helper":                   {"unknown", ""},
	} {
		if l, src := layerOf(kg, fqn); l != want[0] || src != want[1] {
			t.Errorf("%s = %s/%q, want %s/%q", fqn, l, src, want[0], want[1])
		}
	}
}

// When an adapter fact and a path rule both apply, the adapter fact wins; the
// first matching rule wins among rules.
// @verifies graph:INV-6
func TestAssignLayersAdapterBeatsRule(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{
			{FQN: "sql.fn", File: "web/schema.sql", Language: "postgres"},
			{FQN: "web.page", File: "web/page.ts", Language: "typescript"},
			{FQN: "web.handler", File: "web/api.ts", Language: "typescript"},
		},
		EntryPoints: []schema.EntryPoint{{ID: "ep.get", Kind: schema.EntryPointKindHTTP, Handler: schema.Handler{Symbol: "web.handler"}}},
	}
	rules := []schema.LayerRule{{Glob: "web/**", Layer: "ui"}, {Glob: "web/**", Layer: "edge"}}
	AssignLayers(&kg, rules)
	for fqn, want := range map[string][2]string{
		"sql.fn":      {"data", "adapter"},
		"web.page":    {"ui", "rule"},
		"web.handler": {"api", "adapter"},
	} {
		if l, src := layerOf(kg, fqn); l != want[0] || src != want[1] {
			t.Errorf("%s = %s/%q, want %s/%q", fqn, l, src, want[0], want[1])
		}
	}
}

// The handler of a declared entry point takes its layer from the entry
// point's kind, for every kind; what the handler calls does not.
// @verifies graph:INV-6
func TestAssignLayersHandlerFromEntryPointKind(t *testing.T) {
	want := map[string]string{
		schema.EntryPointKindHTTP:          "api",
		schema.EntryPointKindGRPC:          "api",
		schema.EntryPointKindCron:          "async",
		schema.EntryPointKindQueue:         "async",
		schema.EntryPointKindEventConsumer: "async",
		schema.EntryPointKindWebhook:       "async",
		schema.EntryPointKindCLI:           "service",
	}
	var kg schema.KnowledgeGraph
	for kind := range want {
		kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "h." + kind, File: "src/" + kind + ".php", Language: "php"})
		kg.EntryPoints = append(kg.EntryPoints, schema.EntryPoint{ID: "ep." + kind, Kind: kind, Handler: schema.Handler{Symbol: "h." + kind}})
	}
	kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "callee", File: "src/callee.php", Language: "php"})
	AssignLayers(&kg, nil)
	for kind, layer := range want {
		if l, src := layerOf(kg, "h."+kind); l != layer || src != "adapter" {
			t.Errorf("handler of %s = %s/%q, want %s/adapter", kind, l, src, layer)
		}
	}
	if l, src := layerOf(kg, "callee"); l != "unknown" || src != "" {
		t.Errorf("a callee inherits nothing: %s/%q", l, src)
	}
}

// A rule naming a layer outside the vocabulary, or with a bad glob, is a
// VALIDATE_POLICY error and matches nothing.
// @verifies graph:INV-6
func TestAssignLayersReportsBadRules(t *testing.T) {
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{{FQN: "a", File: "web/a.ts", Language: "typescript"}}}
	v := AssignLayers(&kg, []schema.LayerRule{{Glob: "web/**", Layer: "frontend"}, {Glob: "[", Layer: "ui"}})
	if len(v) != 2 {
		t.Fatalf("violations = %+v, want 2", v)
	}
	for _, x := range v {
		if x.Code != schema.CodeValidatePolicy || x.Severity != schema.SeverityError {
			t.Errorf("%+v", x)
		}
	}
	if !strings.Contains(v[0].Message, `"frontend"`) {
		t.Errorf("message = %s", v[0].Message)
	}
	if l, _ := layerOf(kg, "a"); l != "unknown" {
		t.Errorf("a bad rule must match nothing, got %s", l)
	}
}

// A feature block's layers are the set of its implementation symbols' layers,
// an invariant block's the set of its enforcers'.
// @verifies graph:INV-6
func TestDeriveBlockLayersIsTheSet(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{
			{FQN: "a", Feature: "cart", Kind: "function", Exported: true, Layer: "ui", EnforcesInvariants: []string{"cart:INV-1"}},
			{FQN: "b", Feature: "cart", Kind: "function", Exported: true, Layer: "data", EnforcesInvariants: []string{"cart:INV-1"}},
			{FQN: "c", Feature: "cart", Kind: "function", Exported: true, Layer: "ui"},
			{FQN: "d", Feature: "cart", Kind: "function", Exported: true, Layer: "data", EnforcesInvariants: []string{"INV-2"}},
			{FQN: "e", Feature: "cart", Kind: "function", Exported: false, Layer: "edge"}, // private: not an implementation
		},
		EntryPoints: []schema.EntryPoint{{ID: "ep.x", Kind: schema.EntryPointKindCron}},
		Blocks:      []schema.Block{{ID: "feat:cart"}, {ID: "inv:cart.INV-1"}, {ID: "inv:cart.INV-2"}, {ID: "inv:cart.INV-3"}, {ID: "ep:ep.x"}, {ID: "other"}},
	}
	DeriveBlockLayers(&kg)
	got := map[string][]string{}
	for _, b := range kg.Blocks {
		got[b.ID] = b.Layers
	}
	want := map[string][]string{
		"feat:cart":      {"data", "ui"},
		"inv:cart.INV-1": {"data", "ui"},
		"inv:cart.INV-2": {"data"},
		"inv:cart.INV-3": nil,
		"ep:ep.x":        {"async"},
		"other":          nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layers = %v, want %v", got, want)
	}
}
