package cli

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	_ "github.com/salahmyn/lattice/pkg/lattice/entrypoints/fastapi" // registers FastAPI HTTP detector
	_ "github.com/salahmyn/lattice/pkg/lattice/entrypoints/laravel" // registers Laravel detectors
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/importer"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

type extractSummary struct {
	Features         int    `json:"features"`
	Symbols          int    `json:"symbols"`
	Tests            int    `json:"tests"`
	Modules          int    `json:"modules"`
	Initiatives      int    `json:"initiatives"`
	Tasks            int    `json:"tasks"`
	StructuralChecks int    `json:"structural_checks"`
	Violations       int    `json:"violations"`
	Review           bool   `json:"review_mode"`
	Sharded          bool   `json:"sharded"`
	Output           string `json:"output"`
}

func newExtractCommand(io *IO) *cobra.Command {
	var withCodeGraph, withMutation, review bool
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract the knowledge graph and write lattice.json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = withMutation // mutation enrichment is wired by the mutation milestone
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			if review {
				ws.Review = true
			}
			summary, err := runExtract(cmd.Context(), ws, withCodeGraph)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(summary)
			}
			io.printf("Extracted knowledge graph -> %s\n", summary.Output)
			if summary.Review {
				io.printf("  (review mode: no code root accessible — source not parsed)\n")
			}
			io.printf("  features:          %d\n", summary.Features)
			io.printf("  symbols:           %d\n", summary.Symbols)
			io.printf("  tests:             %d\n", summary.Tests)
			io.printf("  modules:           %d\n", summary.Modules)
			io.printf("  initiatives:       %d\n", summary.Initiatives)
			io.printf("  tasks:             %d\n", summary.Tasks)
			io.printf("  structural checks: %d\n", summary.StructuralChecks)
			if summary.Sharded {
				io.printf("  (sharded into %s)\n", filepath.Base(filepath.Dir(summary.Output))+"/graph/")
			}
			if summary.Violations > 0 {
				io.printf("  extraction issues: %d\n", summary.Violations)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&withCodeGraph, "with-code-graph", false, "run SCIP indexers before extraction")
	cmd.Flags().BoolVar(&withMutation, "with-mutation", false, "enrich with mutation scores")
	cmd.Flags().BoolVar(&review, "review", false, "manifest-only mode: do not parse source code")
	return cmd
}

// buildGraph runs extraction and graph building without writing to disk.
func buildGraph(ctx context.Context, ws *workspace.Workspace, withCodeGraph bool) (schema.KnowledgeGraph, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	adCfg, err := config.LoadAdapters(ws.LatticeDir)
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}
	reg := all.Registry(adCfg)

	if withCodeGraph && !ws.Review {
		cfg, _ := config.Load(ws.LatticeDir)
		for _, root := range ws.CodeRoots {
			if root.Available {
				scip.Orchestrate(ctx, root.Abs, ws.SCIPDir(), reg, cfg.Subprocess.DefaultTimeoutDuration())
			}
		}
	}

	res, err := extract.Extract(ctx, ws, reg, extract.Options{})
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}

	// Merge the brownfield sidecar map: features link to code without
	// annotations having to be written into source.
	if am, amErr := importer.LoadAnnotationMap(filepath.Join(ws.ImportDir(), importer.AnnotationMapFileName)); amErr == nil {
		importer.ApplyAnnotationMap(res.Modules, am)
	}

	kg := graph.Build(graph.Input{
		Manifests:   res.Manifests,
		Modules:     res.Modules,
		Initiatives: res.Initiatives,
		Tasks:       res.Tasks,
		Violations:  res.Violations,
		Review:      res.Review,
	}, graph.Options{
		Commit:          gitCommit(ws.LatticeDir),
		LanguageIndexes: indexPaths(reg.Names()),
	})

	// v0.3.0 entry-point detection + v0.3.1 SCIP/persistence/labeling.
	// Skipped in review mode (no source to walk).
	if !ws.Review {
		if eps, derr := entrypoints.DetectAll(ctx, ws, res.Modules); derr == nil {
			scipCorpus, _ := scip.Load(scipIndexPaths(ws)...)
			detected := entrypoints.TraceWithSCIP(eps, kg.Features, scipCorpus)
			// v0.3.1: merge with any EPs already persisted to disk so
			// a human-authored purpose / status survives re-extracts.
			persisted, _ := entrypoints.LoadEntryPoints(ws.EntryPointsDir())
			merged := entrypoints.Merge(detected, persisted)

			// v0.3.1: LLM-label EPs that don't yet carry a purpose.
			// Tone-aware via the same agentic.ToneContract the
			// importer uses, so a single tone setting steers both
			// feature and entry-point prose.
			cfg, _ := config.Load(ws.LatticeDir)
			provider := agentic.FromConfig(cfg.Agentic.LLM)
			if agentic.Enabled(provider) {
				skip := map[string]bool{}
				for _, p := range persisted {
					if p.Purpose != "" {
						skip[p.ID] = true
					}
				}
				merged = entrypoints.LabelEntryPoints(ctx, merged, entrypoints.LabelOptions{
					Provider:     provider,
					SystemPrompt: agentic.ToneContract(cfg.Agentic.Tone),
					MaxTokens:    cfg.Agentic.LLM.MaxTokens,
					Skip:         skip,
				})
				// Persist newly-labeled EPs so the next extract is fast
				// (no LLM round-trip) and a human can edit on disk.
				for _, ep := range merged {
					if ep.Purpose != "" && !skip[ep.ID] {
						_, _ = entrypoints.SaveEntryPoint(ws.EntryPointsDir(), ep)
					}
				}
			}
			kg.EntryPoints = merged
		}
	}

	return kg, nil
}

// runExtract performs extraction + graph build and writes the knowledge graph.
func runExtract(ctx context.Context, ws *workspace.Workspace, withCodeGraph bool) (extractSummary, error) {
	kg, err := buildGraph(ctx, ws, withCodeGraph)
	if err != nil {
		return extractSummary{}, err
	}
	cfg, _ := config.Load(ws.LatticeDir)

	sharded, err := writeGraph(ws, cfg, kg)
	if err != nil {
		return extractSummary{}, err
	}

	return extractSummary{
		Features:         len(kg.Features),
		Symbols:          len(kg.Symbols),
		Tests:            len(kg.Tests),
		Modules:          len(kg.Modules),
		Initiatives:      len(kg.Initiatives),
		Tasks:            len(kg.Tasks),
		StructuralChecks: len(kg.StructuralChecks),
		Violations:       len(kg.Violations),
		Review:           kg.Review,
		Sharded:          sharded,
		Output:           ws.GraphPath(),
	}, nil
}

// writeGraph emits the knowledge graph, sharded when configured.
func writeGraph(ws *workspace.Workspace, cfg config.Config, kg schema.KnowledgeGraph) (bool, error) {
	if cfg.Knowledge.Sharding.Enabled {
		if err := graph.WriteSharded(ws.GraphPath(), ws.GraphShardDir(), kg, graph.ShardOptions{
			Strategy:            cfg.Knowledge.Sharding.Strategy,
			MaxFeaturesPerShard: cfg.Knowledge.Sharding.MaxFeaturesPerShard,
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, graph.Write(ws.GraphPath(), kg)
}

// gitCommit returns the current commit SHA, or "" when not in a git repo.
func gitCommit(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// indexPaths returns the per-language SCIP index references, relative to the
// lattice/ directory.
func indexPaths(languages []string) map[string]string {
	paths := map[string]string{}
	for _, lang := range languages {
		paths[lang] = filepath.ToSlash(filepath.Join(".cache", "scip", lang+".scip"))
	}
	return paths
}

