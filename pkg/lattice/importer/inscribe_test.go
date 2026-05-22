package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// fakeAdapter is a minimal LanguageAdapter for exercising the annotation
// writer without a real tree-sitter grammar.
type fakeAdapter struct {
	renderPrefix string // prepended to each rendered annotation
}

func (fakeAdapter) Name() string             { return "fake" }
func (fakeAdapter) FileExtensions() []string  { return []string{".fk"} }
func (fakeAdapter) CanParse(p string) bool    { return strings.HasSuffix(p, ".fk") }

func (fakeAdapter) Parse(_ context.Context, path string, src []byte) (ir.Module, error) {
	if strings.Contains(string(src), "@@BROKEN@@") {
		return ir.Module{}, &adapters.ParseError{File: path, Message: "syntax error"}
	}
	return ir.Module{File: path, Language: "fake"}, nil
}

func (a fakeAdapter) RenderAnnotationSuggestion(_ ir.Symbol, sug []adapters.AnnotationSuggestion) (string, error) {
	var b strings.Builder
	for _, s := range sug {
		arg := ""
		if len(s.Args) > 0 {
			arg, _ = s.Args[0].(string)
		}
		b.WriteString(a.renderPrefix + "@" + s.Annotation + "(" + arg + ")\n")
	}
	return b.String(), nil
}

func (fakeAdapter) SCIPIndexerCommand(string, string) ([]string, error)   { return nil, nil }
func (fakeAdapter) MutationRunnerCommand(string, []string) ([]string, error) { return nil, nil }

func TestInsertAnnotations(t *testing.T) {
	got := insertAnnotations("line1\nline2\nline3", []InscribeEdit{
		{Line: 2, Text: "INSERTED\n"},
	})
	want := "line1\nINSERTED\nline2\nline3"
	if got != want {
		t.Errorf("insertAnnotations = %q, want %q", got, want)
	}
}

func TestInsertAnnotationsBottomUp(t *testing.T) {
	// Two edits: the lower-line insertion must not shift the higher-line one.
	got := insertAnnotations("a\nb\nc\nd", []InscribeEdit{
		{Line: 1, Text: "X\n"},
		{Line: 3, Text: "Y\n"},
	})
	if got != "X\na\nb\nY\nc\nd" {
		t.Errorf("got %q", got)
	}
}

func TestPlanInscribe(t *testing.T) {
	reg := adapters.NewRegistry(fakeAdapter{})
	modules := []ir.Module{{
		File: "svc/a.fk", Language: "fake",
		Symbols: []ir.Symbol{
			{FQN: "svc.Handler", Kind: ir.KindClass, Line: 10},
			{FQN: "svc.Handler.run", Kind: ir.KindMethod, Line: 12, EnclosingFQN: "svc.Handler"},
			{FQN: "svc.helper", Kind: ir.KindFunction, Line: 30},
		},
	}}
	plan, err := PlanInscribe(modules, reg, []FeatureSymbols{
		{Feature: "svc", Symbols: []string{"svc.Handler", "svc.Handler.run", "svc.helper"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The method is skipped (it inherits from the class); two top-level edits.
	if len(plan.Edits) != 2 {
		t.Fatalf("want 2 edits (class + function), got %d: %+v", len(plan.Edits), plan.Edits)
	}
	for _, e := range plan.Edits {
		if e.Feature != "svc" || !strings.Contains(e.Text, "@feature(svc)") {
			t.Errorf("bad edit %+v", e)
		}
	}
}

func TestPlanInscribeSkipsAlreadyAnnotated(t *testing.T) {
	reg := adapters.NewRegistry(fakeAdapter{})
	modules := []ir.Module{{
		File: "svc/a.fk", Language: "fake",
		Symbols: []ir.Symbol{{
			FQN: "svc.Handler", Kind: ir.KindClass, Line: 10,
			Annotations: []ir.Annotation{{Kind: "feature", Args: []interface{}{"svc"}}},
		}},
	}}
	plan, err := PlanInscribe(modules, reg, []FeatureSymbols{
		{Feature: "svc", Symbols: []string{"svc.Handler"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 0 || len(plan.AlreadyMarked) != 1 {
		t.Errorf("already-annotated symbol must be skipped: %+v", plan)
	}
}

func TestApplyInscribe(t *testing.T) {
	dir := t.TempDir()
	src := "class Handler:\n    pass\n"
	if err := os.WriteFile(filepath.Join(dir, "a.fk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := adapters.NewRegistry(fakeAdapter{})
	plan := InscribePlan{Edits: []InscribeEdit{
		{File: "a.fk", Line: 1, Symbol: "Handler", Feature: "svc", Text: "@feature(svc)\n"},
	}}
	abs := func(f string) string { return filepath.Join(dir, f) }

	res := ApplyInscribe(context.Background(), plan, reg, abs)
	if len(res.Failed) != 0 || res.AnnotationsInserted != 1 {
		t.Fatalf("apply failed: %+v", res)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "a.fk"))
	if !strings.HasPrefix(string(out), "@feature(svc)\nclass Handler:") {
		t.Errorf("annotation not inserted above the declaration:\n%s", out)
	}
}

func TestApplyInscribeRollsBackOnBrokenParse(t *testing.T) {
	dir := t.TempDir()
	src := "class Handler:\n    pass\n"
	path := filepath.Join(dir, "a.fk")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := adapters.NewRegistry(fakeAdapter{})
	plan := InscribePlan{Edits: []InscribeEdit{
		{File: "a.fk", Line: 1, Symbol: "Handler", Feature: "svc", Text: "@@BROKEN@@\n"},
	}}
	res := ApplyInscribe(context.Background(), plan, reg, func(f string) string { return filepath.Join(dir, f) })
	if len(res.Failed) != 1 || len(res.FilesChanged) != 0 {
		t.Fatalf("a parse-breaking edit must be rejected: %+v", res)
	}
	out, _ := os.ReadFile(path)
	if string(out) != src {
		t.Errorf("file must be left untouched after a rejected edit, got:\n%s", out)
	}
}
