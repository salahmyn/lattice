// Package lattice is the public Go API for embedding Lattice. It is a thin
// wrapper over the same internals the CLI uses; the CLI, this library, and the
// MCP server are peers over one engine.
package lattice

import (
	"context"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/analyze"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/patch"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/validate"
)

// Lattice is an open handle to one repository.
type Lattice struct {
	repo string
	cfg  config.Config
}

// Open returns a Lattice handle for the repository at path.
func Open(_ context.Context, path string) (*Lattice, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return &Lattice{repo: path, cfg: cfg}, nil
}

// Extract builds the knowledge graph from the repository.
func (l *Lattice) Extract(ctx context.Context) (schema.KnowledgeGraph, error) {
	adCfg, err := config.LoadAdapters(l.repo)
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}
	res, err := extract.Extract(ctx, l.repo, all.Registry(adCfg), extract.Options{})
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}
	return graph.Build(graph.Input{
		Manifests:   res.Manifests,
		Modules:     res.Modules,
		Initiatives: res.Initiatives,
		Tasks:       res.Tasks,
		Violations:  res.Violations,
	}, graph.Options{}), nil
}

// Validate extracts and validates the repository.
func (l *Lattice) Validate(ctx context.Context) ([]schema.Violation, error) {
	kg, err := l.Extract(ctx)
	if err != nil {
		return nil, err
	}
	return validate.Validate(kg, l.cfg), nil
}

// ListFeatures returns every feature manifest.
func (l *Lattice) ListFeatures(ctx context.Context) ([]schema.Manifest, error) {
	kg, err := l.Extract(ctx)
	if err != nil {
		return nil, err
	}
	return kg.Features, nil
}

// GetFeature returns one feature manifest by id.
func (l *Lattice) GetFeature(ctx context.Context, id string) (*schema.Manifest, error) {
	kg, err := l.Extract(ctx)
	if err != nil {
		return nil, err
	}
	for i := range kg.Features {
		if kg.Features[i].ID == id {
			return &kg.Features[i], nil
		}
	}
	return nil, nil
}

// GetSymbolContext returns the resolved graph symbol for an FQN.
func (l *Lattice) GetSymbolContext(ctx context.Context, fqn string) (*schema.GraphSymbol, error) {
	kg, err := l.Extract(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range append(kg.Symbols, kg.Tests...) {
		if s.FQN == fqn {
			return &s, nil
		}
	}
	return nil, nil
}

// GetBlastRadius returns the SCIP-derived blast radius of a symbol.
func (l *Lattice) GetBlastRadius(_ context.Context, fqn string) (scip.BlastRadius, error) {
	corpus, err := scip.Load(scipPaths(l.repo)...)
	if err != nil {
		return scip.BlastRadius{}, err
	}
	return corpus.BlastRadius(fqn), nil
}

// PreviewPatch evaluates a patch without writing.
func (l *Lattice) PreviewPatch(ctx context.Context, p schema.Patch) (schema.PatchPreview, error) {
	return patch.New(l.repo).Preview(ctx, p)
}

// ApplyPatch applies a patch atomically.
func (l *Lattice) ApplyPatch(ctx context.Context, p schema.Patch) (schema.PatchResult, error) {
	return patch.New(l.repo).Apply(ctx, p)
}

// AnalyzeProposal runs conflict and impact analysis on a proposal manifest.
func (l *Lattice) AnalyzeProposal(ctx context.Context, proposalPath string) (analyze.ImpactReport, error) {
	return analyze.NewAnalyzer(l.repo).AnalyzeProposal(ctx, proposalPath)
}

// SuggestAnnotation proposes annotations for the symbol at file:line.
func (l *Lattice) SuggestAnnotation(ctx context.Context, file string, line int) (agentic.AnnotationResult, error) {
	return agentic.New(l.repo, l.cfg).SuggestAnnotation(ctx, file, line)
}

func scipPaths(repo string) []string {
	langs := []string{"python", "typescript", "php"}
	out := make([]string, 0, len(langs))
	for _, lang := range langs {
		out = append(out, repo+"/.lattice/scip/"+lang+".scip")
	}
	return out
}
