package analyze

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Analyzer runs conflict and impact analysis against a workspace.
type Analyzer struct {
	ws       *workspace.Workspace
	embedder Embedder
}

// NewAnalyzer returns an analyzer for the workspace using the dependency-free
// lexical embedder.
func NewAnalyzer(ws *workspace.Workspace) *Analyzer {
	return &Analyzer{ws: ws, embedder: NewLexicalEmbedder()}
}

// WithEmbedder overrides the semantic embedder (e.g. an ONNX-backed one).
func (a *Analyzer) WithEmbedder(e Embedder) *Analyzer {
	a.embedder = e
	return a
}

// AnalyzeProposal loads a proposal manifest and analyzes it against the
// repository corpus.
func (a *Analyzer) AnalyzeProposal(ctx context.Context, proposalPath string) (ImpactReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	proposal, err := schema.LoadManifest(proposalPath)
	if err != nil {
		return ImpactReport{}, fmt.Errorf("load proposal: %w", err)
	}

	adCfg, _ := config.LoadAdapters(a.ws.LatticeDir)
	cfg, _ := config.Load(a.ws.LatticeDir)
	reg := all.Registry(adCfg)

	res, err := extract.Extract(ctx, a.ws, reg, extract.Options{})
	if err != nil {
		return ImpactReport{}, err
	}

	mode := "create"
	for _, m := range res.Manifests {
		if m.ID == proposal.ID {
			mode = "extend"
			break
		}
	}

	th := thresholds{
		warn:      cfg.Analysis.SimilarityWarnThreshold,
		duplicate: cfg.Analysis.SimilarityDuplicateThreshold,
	}

	report := ImpactReport{
		Proposal:              proposalPath,
		Target:                proposal.ID,
		Mode:                  mode,
		DeterministicFindings: deterministicChecks(*proposal, res.Manifests),
		SemanticFindings:      semanticChecks(*proposal, res.Manifests, a.embedder, th),
		BlastRadius:           a.blastRadius(res, proposal.ID),
	}
	report.OpenInvariants = openInvariants(*proposal, cfg)
	report.ResolutionsRequired = resolutions(report)
	return report, nil
}

// blastRadius derives the code-level impact of changing a feature by querying
// SCIP for the symbols that implement it. Returns Available=false when no
// SCIP indexes are present.
func (a *Analyzer) blastRadius(res extract.Result, featureID string) *BlastRadius {
	corpus, err := scip.Load(scipPaths(a.ws)...)
	if err != nil || corpus.Empty() {
		return &BlastRadius{Available: false}
	}
	kg := graph.Build(graph.Input{
		Manifests: res.Manifests, Modules: res.Modules,
		Initiatives: res.Initiatives, Tasks: res.Tasks,
	}, graph.Options{})

	br := &BlastRadius{Available: true}
	fileSet := map[string]bool{}
	for _, m := range kg.Features {
		if m.ID != featureID {
			continue
		}
		for _, impl := range m.Implementations {
			hit := corpus.BlastRadius(impl.Symbol)
			if hit.Resolved {
				br.Modifies = append(br.Modifies, impl.Symbol)
			}
			for _, f := range hit.Files {
				fileSet[f] = true
			}
		}
	}
	br.ExternalConsumers = len(consumersOf(featureID, res.Manifests))
	br.AffectedTests = len(fileSet)
	return br
}

func scipPaths(ws *workspace.Workspace) []string {
	langs := []string{"python", "typescript", "php"}
	out := make([]string, 0, len(langs))
	for _, l := range langs {
		out = append(out, filepath.Join(ws.SCIPDir(), l+".scip"))
	}
	return out
}

// openInvariants lists what each proposed invariant still requires.
func openInvariants(proposal schema.Manifest, cfg config.Config) []InvariantRequirement {
	var out []InvariantRequirement
	for _, inv := range proposal.Invariants {
		needs := []string{"at least one @enforces_invariant"}
		for _, vb := range inv.EffectiveVerifiableBy() {
			switch vb {
			case schema.VerifiableByTest:
				needs = append(needs, "at least one @verifies test")
			case schema.VerifiableByStructural:
				needs = append(needs, "a structural_check")
			case schema.VerifiableByMutation:
				th := cfg.MutationTesting.Thresholds.ThresholdFor(proposal.ID + ":" + inv.ID)
				needs = append(needs, fmt.Sprintf("mutation score >= %.0f%%", th))
			}
		}
		out = append(out, InvariantRequirement{Invariant: inv.ID, Needs: needs})
	}
	return out
}

// resolutions derives the human resolutions a proposal needs before
// reaching production status.
func resolutions(r ImpactReport) []string {
	var out []string
	for _, f := range r.DeterministicFindings {
		switch f.Code {
		case "BREAKING_SURFACE_CHANGE":
			out = append(out, "Migration plan with consumer phases")
		case "SURFACE_COLLISION":
			out = append(out, "Resolve the surface collision before merge")
		case "DEPENDENCY_CYCLE":
			out = append(out, "Break the introduced dependency cycle")
		}
	}
	for _, f := range r.SemanticFindings {
		if f.Code == "CAPABILITY_OVERLAP" || f.Code == "FEATURE_PURPOSE_OVERLAP" {
			out = append(out, "Acknowledge the semantic overlap decision")
			break
		}
	}
	if len(r.OpenInvariants) > 0 {
		out = append(out, fmt.Sprintf("Enforce and verify %d proposed invariant(s)", len(r.OpenInvariants)))
	}
	return dedupeStrings(out)
}

func dedupeStrings(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
