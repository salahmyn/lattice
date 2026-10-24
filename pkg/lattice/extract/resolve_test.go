package extract

import (
	"context"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// resolvingAdapter keeps only the last module's symbols, standing in for an
// adapter whose later files redefine what earlier ones declared.
type resolvingAdapter struct{ calls int }

func (r *resolvingAdapter) Name() string             { return "fake" }
func (r *resolvingAdapter) FileExtensions() []string { return []string{".fake"} }
func (r *resolvingAdapter) CanParse(string) bool     { return true }
func (r *resolvingAdapter) Parse(context.Context, string, []byte) (ir.Module, error) {
	return ir.Module{}, nil
}
func (r *resolvingAdapter) RenderAnnotationSuggestion(ir.Symbol, []adapters.AnnotationSuggestion) (string, error) {
	return "", nil
}
func (r *resolvingAdapter) SCIPIndexerCommand(string, string) ([]string, error) { return nil, nil }
func (r *resolvingAdapter) MutationRunnerCommand(string, []string) ([]string, error) {
	return nil, nil
}
func (r *resolvingAdapter) Resolve(mods []ir.Module) []ir.Module {
	r.calls++
	for i := range mods[:len(mods)-1] {
		mods[i].Symbols = nil
	}
	return mods
}

// A Resolver sees only its own language's modules, once, and the module
// order of the whole set is kept.
// @verifies extraction:INV-3
func TestResolveAcrossFiles(t *testing.T) {
	fake := &resolvingAdapter{}
	reg := adapters.NewRegistry(fake)
	sym := []ir.Symbol{{FQN: "x"}}
	mods := []ir.Module{
		{File: "a.fake", Language: "fake", Symbols: sym},
		{File: "b.ts", Language: "typescript", Symbols: sym},
		{File: "c.fake", Language: "fake", Symbols: sym},
	}
	out := resolveAcrossFiles(reg, mods)
	if fake.calls != 1 {
		t.Fatalf("Resolve called %d times, want 1", fake.calls)
	}
	if out[0].File != "a.fake" || len(out[0].Symbols) != 0 || len(out[1].Symbols) != 1 || len(out[2].Symbols) != 1 {
		t.Errorf("modules after resolve = %+v", out)
	}
}
