package validate

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// amaFeature is a minimal Manifest with the colocated implementation
// already populated — saves repetition across the AMA tests where we
// don't care about feature-level integrity beyond ID + Implementations.
func amaFeature(id, file string) schema.Manifest {
	return schema.Manifest{
		ID: id, Version: 1, Status: schema.StatusProduction,
		Purpose: "p", Owners: schema.Owners{Business: "b", Engineering: "e"},
		Implementations: []schema.Implementation{{Symbol: id + ".impl", File: file, Line: 1, Language: "php"}},
	}
}

func TestCrossFeatureImportWarningByDefault(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{amaFeature("checkout", "app/Checkout/X.php"), amaFeature("payments", "app/Payments/Y.php")},
		Symbols: []schema.GraphSymbol{{
			FQN: "Checkout\\X", Feature: "checkout",
			DependsOnFeatures: []string{"payments"}, File: "app/Checkout/X.php",
		}},
	}
	got := Validate(kg, config.Default(), Options{})
	found := false
	for _, v := range got {
		if v.Code == schema.CodeCrossFeatureImport {
			if v.Severity != schema.SeverityWarning {
				t.Errorf("expected warning by default, got %q", v.Severity)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected CROSS_FEATURE_IMPORT to fire")
	}
}

func TestCrossFeatureImportErrorInAMAMode(t *testing.T) {
	cfg := config.Default()
	cfg.Architecture.AMAMode = true
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{amaFeature("checkout", "app/Checkout/X.php"), amaFeature("payments", "app/Payments/Y.php")},
		Symbols: []schema.GraphSymbol{{
			FQN: "Checkout\\X", Feature: "checkout",
			DependsOnFeatures: []string{"payments"}, File: "app/Checkout/X.php",
		}},
	}
	got := Validate(kg, cfg, Options{})
	for _, v := range got {
		if v.Code == schema.CodeCrossFeatureImport && v.Severity != schema.SeverityError {
			t.Errorf("ama_mode should escalate CROSS_FEATURE_IMPORT to error, got %q", v.Severity)
		}
	}
}

func TestCrossFeatureImportDedupesPerEdge(t *testing.T) {
	// 3 symbols in the same file all import the same target feature.
	// Should produce one violation, not three.
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{amaFeature("checkout", "app/Checkout/X.php"), amaFeature("payments", "app/Payments/Y.php")},
		Symbols: []schema.GraphSymbol{
			{FQN: "Checkout\\A", Feature: "checkout", DependsOnFeatures: []string{"payments"}, File: "app/Checkout/X.php"},
			{FQN: "Checkout\\B", Feature: "checkout", DependsOnFeatures: []string{"payments"}, File: "app/Checkout/X.php"},
			{FQN: "Checkout\\C", Feature: "checkout", DependsOnFeatures: []string{"payments"}, File: "app/Checkout/X.php"},
		},
	}
	count := 0
	for _, v := range Validate(kg, config.Default(), Options{}) {
		if v.Code == schema.CodeCrossFeatureImport {
			count++
		}
	}
	if count != 1 {
		t.Errorf("CROSS_FEATURE_IMPORT dedupe broken: got %d, want 1", count)
	}
}

func TestFeatureNotColocated(t *testing.T) {
	f := amaFeature("checkout", "app/Checkout/A.php")
	f.Implementations = append(f.Implementations, schema.Implementation{
		Symbol: "Modules\\Checkout\\B", File: "Modules/Checkout/B.php", Line: 1, Language: "php",
	})
	kg := schema.KnowledgeGraph{Features: []schema.Manifest{f}}
	if !codes(Validate(kg, config.Default(), Options{}))[schema.CodeFeatureNotColocated] {
		t.Error("expected FEATURE_NOT_COLOCATED for feature spanning two top-level dirs")
	}
}

func TestFeatureColocatedDoesNotFire(t *testing.T) {
	f := amaFeature("checkout", "app/Checkout/A.php")
	f.Implementations = append(f.Implementations, schema.Implementation{
		Symbol: "App\\Checkout\\B", File: "app/Checkout/B.php", Line: 1, Language: "php",
	})
	kg := schema.KnowledgeGraph{Features: []schema.Manifest{f}}
	if codes(Validate(kg, config.Default(), Options{}))[schema.CodeFeatureNotColocated] {
		t.Error("FEATURE_NOT_COLOCATED fired on a feature in one top-level dir")
	}
}

func TestFileLineCap(t *testing.T) {
	cfg := config.Default()
	cfg.Architecture.FileLineCap = 100
	kg := schema.KnowledgeGraph{
		Modules: []schema.GraphModule{{File: "app/Big.php", LineCount: 250}},
	}
	if !codes(Validate(kg, cfg, Options{}))[schema.CodeFileLineCap] {
		t.Error("expected FILE_LINE_CAP for a 250-line file with cap 100")
	}
}

func TestFileLineCapWithinLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Architecture.FileLineCap = 150
	kg := schema.KnowledgeGraph{
		Modules: []schema.GraphModule{{File: "app/Ok.php", LineCount: 80}},
	}
	if codes(Validate(kg, cfg, Options{}))[schema.CodeFileLineCap] {
		t.Error("FILE_LINE_CAP fired on a file within the cap")
	}
}

func TestMethodLineCap(t *testing.T) {
	cfg := config.Default()
	cfg.Architecture.MethodLineCap = 20
	kg := schema.KnowledgeGraph{
		Modules: []schema.GraphModule{{File: "app/X.php", LineCount: 200}},
		Symbols: []schema.GraphSymbol{
			{FQN: "X::a", Kind: "method", File: "app/X.php", Line: 10},
			{FQN: "X::b", Kind: "method", File: "app/X.php", Line: 100}, // ~90 lines for a
		},
	}
	if !codes(Validate(kg, cfg, Options{}))[schema.CodeMethodLineCap] {
		t.Error("expected METHOD_LINE_CAP for a ~90-line method with cap 20")
	}
}

func TestMixedCommandQueryOnlyInAMAMode(t *testing.T) {
	// `mixed` capability — silent outside AMA mode, fires in AMA mode.
	f := amaFeature("checkout", "app/Checkout/X.php")
	f.Capabilities = []schema.Capability{{
		ID: "refund", Summary: "refund", Rules: []string{"r"}, // Kind blank → effective mixed
	}}
	kg := schema.KnowledgeGraph{Features: []schema.Manifest{f}}

	if codes(Validate(kg, config.Default(), Options{}))[schema.CodeMixedCommandQuery] {
		t.Error("MIXED_COMMAND_QUERY should be silent when ama_mode is off")
	}
	cfg := config.Default()
	cfg.Architecture.AMAMode = true
	if !codes(Validate(kg, cfg, Options{}))[schema.CodeMixedCommandQuery] {
		t.Error("MIXED_COMMAND_QUERY should fire on `mixed` capability in ama_mode")
	}
}

func TestMixedCommandQuerySuppressedByCommandKind(t *testing.T) {
	f := amaFeature("checkout", "app/Checkout/X.php")
	f.Capabilities = []schema.Capability{{
		ID: "refund", Kind: schema.CapabilityCommand,
		Summary: "refund", Rules: []string{"r"},
	}}
	cfg := config.Default()
	cfg.Architecture.AMAMode = true
	kg := schema.KnowledgeGraph{Features: []schema.Manifest{f}}
	if codes(Validate(kg, cfg, Options{}))[schema.CodeMixedCommandQuery] {
		t.Error("MIXED_COMMAND_QUERY should not fire when capability.kind is command")
	}
}

// Without ama_mode or an explicit cap, the line caps stay silent: AMA's
// 150/25 defaults are noise for a project that never chose AMA.
// @verifies validation:INV-10
func TestLineCapsNeedOptIn(t *testing.T) {
	kg := schema.KnowledgeGraph{
		Modules: []schema.GraphModule{{File: "src/Big.tsx", LineCount: 900, SymbolFQNs: []string{"src.Big.Page"}}},
		Symbols: []schema.GraphSymbol{{FQN: "src.Big.Page", Kind: "function", File: "src/Big.tsx", Line: 1, EndLine: 400}},
	}
	got := codes(Validate(kg, config.Default(), Options{}))
	if got[schema.CodeFileLineCap] || got[schema.CodeMethodLineCap] {
		t.Errorf("line caps fired without opt-in: %v", got)
	}
	cfg := config.Default()
	cfg.Architecture.AMAMode = true
	got = codes(Validate(kg, cfg, Options{}))
	if !got[schema.CodeFileLineCap] || !got[schema.CodeMethodLineCap] {
		t.Errorf("line caps should fire under ama_mode: %v", got)
	}
}

// A short function that is last in its file is measured by its own end
// line, not by the distance to the end of the file.
func TestMethodLineCapUsesEndLine(t *testing.T) {
	cfg := config.Default()
	cfg.Architecture.MethodLineCap = 25
	kg := schema.KnowledgeGraph{
		Modules: []schema.GraphModule{{File: "src/hooks.ts", LineCount: 200}},
		Symbols: []schema.GraphSymbol{
			{FQN: "src.hooks.useClock", Kind: "function", File: "src/hooks.ts", Line: 10, EndLine: 60},
			{FQN: "src.hooks.useDuration", Kind: "function", File: "src/hooks.ts", Line: 190, EndLine: 196},
		},
	}
	var flagged []string
	for _, v := range Validate(kg, cfg, Options{}) {
		if v.Code == schema.CodeMethodLineCap {
			flagged = append(flagged, v.Message)
		}
	}
	if len(flagged) != 1 || !strings.Contains(flagged[0], "useClock") || !strings.Contains(flagged[0], "~51 lines") {
		t.Errorf("METHOD_LINE_CAP = %v, want only useClock at ~51 lines", flagged)
	}
}

// A file holding only tests is not held to the file cap.
// @verifies validation:INV-10
func TestFileLineCapSkipsTestFiles(t *testing.T) {
	cfg := config.Default()
	cfg.Architecture.FileLineCap = 100
	kg := schema.KnowledgeGraph{
		Modules: []schema.GraphModule{
			{File: "src/cart.test.ts", LineCount: 400, SymbolFQNs: []string{"src.cart.test.test.adds"}},
			{File: "src/cart.ts", LineCount: 400, SymbolFQNs: []string{"src.cart.total"}},
		},
		Symbols: []schema.GraphSymbol{{FQN: "src.cart.total", Kind: "function", File: "src/cart.ts", Line: 1, EndLine: 5}},
		Tests:   []schema.GraphSymbol{{FQN: "src.cart.test.test.adds", Kind: "function", File: "src/cart.test.ts", Line: 1, IsTest: true}},
	}
	var files []string
	for _, v := range Validate(kg, cfg, Options{}) {
		if v.Code == schema.CodeFileLineCap {
			files = append(files, v.Location.File)
		}
	}
	if len(files) != 1 || files[0] != "src/cart.ts" {
		t.Errorf("FILE_LINE_CAP files = %v, want [src/cart.ts]", files)
	}
}
