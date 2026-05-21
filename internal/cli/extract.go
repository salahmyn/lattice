package cli

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
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
	Output           string `json:"output"`
}

func newExtractCommand(io *IO) *cobra.Command {
	var withCodeGraph, withMutation bool
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract the knowledge graph and write lattice.json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = withMutation // mutation enrichment is wired by the mutation milestone
			summary, err := runExtract(cmd.Context(), io.Repo, withCodeGraph)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(summary)
			}
			io.printf("Extracted knowledge graph -> %s\n", summary.Output)
			io.printf("  features:          %d\n", summary.Features)
			io.printf("  symbols:           %d\n", summary.Symbols)
			io.printf("  tests:             %d\n", summary.Tests)
			io.printf("  modules:           %d\n", summary.Modules)
			io.printf("  initiatives:       %d\n", summary.Initiatives)
			io.printf("  tasks:             %d\n", summary.Tasks)
			io.printf("  structural checks: %d\n", summary.StructuralChecks)
			if summary.Violations > 0 {
				io.printf("  extraction issues: %d\n", summary.Violations)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&withCodeGraph, "with-code-graph", false, "run SCIP indexers before extraction")
	cmd.Flags().BoolVar(&withMutation, "with-mutation", false, "enrich with mutation scores")
	return cmd
}

// buildGraph runs extraction and graph building without writing to disk.
func buildGraph(ctx context.Context, repo string, withCodeGraph bool) (schema.KnowledgeGraph, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	adCfg, err := config.LoadAdapters(repo)
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}
	reg := all.Registry(adCfg)

	if withCodeGraph {
		cfg, _ := config.Load(repo)
		scip.Orchestrate(ctx, repo, reg, cfg.Subprocess.DefaultTimeoutDuration())
	}

	res, err := extract.Extract(ctx, repo, reg, extract.Options{})
	if err != nil {
		return schema.KnowledgeGraph{}, err
	}

	return graph.Build(graph.Input{
		Manifests:   res.Manifests,
		Modules:     res.Modules,
		Initiatives: res.Initiatives,
		Tasks:       res.Tasks,
		Violations:  res.Violations,
	}, graph.Options{
		Commit:          gitCommit(repo),
		LanguageIndexes: indexPaths(repo, reg.Names(), withCodeGraph),
	}), nil
}

// runExtract performs extraction + graph build and writes lattice.json.
func runExtract(ctx context.Context, repo string, withCodeGraph bool) (extractSummary, error) {
	kg, err := buildGraph(ctx, repo, withCodeGraph)
	if err != nil {
		return extractSummary{}, err
	}

	out := filepath.Join(repo, "lattice.json")
	if err := graph.Write(out, kg); err != nil {
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
		Output:           out,
	}, nil
}

// gitCommit returns the current commit SHA, or "" when not in a git repo.
func gitCommit(repo string) string {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// indexPaths returns the per-language SCIP index references.
func indexPaths(repo string, languages []string, _ bool) map[string]string {
	paths := map[string]string{}
	for _, lang := range languages {
		paths[lang] = filepath.ToSlash(filepath.Join(".lattice", "scip", lang+".scip"))
	}
	return paths
}
