// Package lattice is the public Go API for embedding Lattice. It is a thin
// wrapper over the same internals the CLI uses; the CLI, this library, and the
// MCP server are peers over one engine.
package lattice

import (
	"context"
	"path/filepath"

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
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Lattice is an open handle to one workspace.
type Lattice struct {
	ws  *workspace.Workspace
	cfg config.Config
}

// Open returns a Lattice handle for the workspace found from path.
func Open(_ context.Context, path string) (*Lattice, error) {
	ws, err := workspace.Open(path)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(ws.LatticeDir)
	if err != nil {
		return nil, err
	}
	return &Lattice{ws: ws, cfg: cfg}, nil
}

// Workspace returns the resolved workspace.
func (l *Lattice) Workspace() *workspace.Workspace { return l.ws }

// Extract builds the knowledge graph from the workspace.
func (l *Lattice) Extract(ctx context.Context) (schema.KnowledgeGraph, error) {
	adCfg, err := config.LoadAdapters(l.ws.LatticeDir)
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}
	res, err := extract.Extract(ctx, l.ws, all.Registry(adCfg), extract.Options{})
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

// Validate extracts and validates the workspace.
func (l *Lattice) Validate(ctx context.Context) ([]schema.Violation, error) {
	kg, err := l.Extract(ctx)
	if err != nil {
		return nil, err
	}
	return validate.Validate(kg, l.cfg, validate.Options{ReviewMode: l.ws.Review}), nil
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
	corpus, err := scip.Load(scipPaths(l.ws)...)
	if err != nil {
		return scip.BlastRadius{}, err
	}
	return corpus.BlastRadius(fqn), nil
}

// PreviewPatch evaluates a patch without writing.
func (l *Lattice) PreviewPatch(ctx context.Context, p schema.Patch) (schema.PatchPreview, error) {
	return patch.New(l.ws).Preview(ctx, p)
}

// ApplyPatch applies a patch atomically.
func (l *Lattice) ApplyPatch(ctx context.Context, p schema.Patch) (schema.PatchResult, error) {
	return patch.New(l.ws).Apply(ctx, p)
}

// AnalyzeProposal runs conflict and impact analysis on a proposal manifest.
func (l *Lattice) AnalyzeProposal(ctx context.Context, proposalPath string) (analyze.ImpactReport, error) {
	return analyze.NewAnalyzer(l.ws).AnalyzeProposal(ctx, proposalPath)
}

// SuggestAnnotation proposes annotations for the symbol at file:line.
func (l *Lattice) SuggestAnnotation(ctx context.Context, file string, line int) (agentic.AnnotationResult, error) {
	return agentic.New(l.ws, l.cfg).SuggestAnnotation(ctx, file, line)
}

func scipPaths(ws *workspace.Workspace) []string {
	langs := []string{"python", "typescript", "php"}
	out := make([]string, 0, len(langs))
	for _, lang := range langs {
		out = append(out, filepath.Join(ws.SCIPDir(), lang+".scip"))
	}
	return out
}
