package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
)

// scipIndexPaths returns the absolute .scip index paths for every adapter.
func scipIndexPaths(repo string) []string {
	adCfg, _ := config.LoadAdapters(repo)
	reg := all.Registry(adCfg)
	var paths []string
	for _, lang := range reg.Names() {
		paths = append(paths, filepath.Join(repo, ".lattice", "scip", lang+".scip"))
	}
	return paths
}

func newBlastRadiusCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "blast-radius <fqn>",
		Short: "Show the code-level impact of a symbol via SCIP",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			corpus, err := scip.Load(scipIndexPaths(io.Repo)...)
			if err != nil {
				return io.fail("SCIP_LOAD_FAILED", err.Error(), nil)
			}
			if corpus.Empty() {
				na := &schema.NextAction{Kind: "run_command", Command: []string{"lattice", "extract", "--with-code-graph"}}
				return io.fail("SCIP_NOT_INDEXED", "no SCIP indexes found; run extract --with-code-graph", na)
			}
			br := corpus.BlastRadius(args[0])
			if io.JSON {
				return io.printJSON(br)
			}
			if !br.Resolved {
				io.printf("%s: not found in SCIP indexes\n", args[0])
				return nil
			}
			io.printf("Blast radius for %s\n", args[0])
			io.printf("  definitions: %d\n", len(br.Definitions))
			io.printf("  references:  %d\n", len(br.References))
			io.printf("  files:\n")
			for _, f := range br.Files {
				io.printf("    %s\n", f)
			}
			return nil
		},
	}
}

func newSymbolCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "symbol <fqn>",
		Short: "Show the Lattice context of a code symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			for _, s := range append(kg.Symbols, kg.Tests...) {
				if s.FQN == args[0] {
					if io.JSON {
						return io.printJSON(s)
					}
					io.printf("%s (%s, %s)\n", s.FQN, s.Kind, s.Language)
					io.printf("  file:      %s:%d\n", s.File, s.Line)
					io.printf("  feature:   %s\n", s.Feature)
					io.printf("  enforces:  %v\n", s.EnforcesInvariants)
					io.printf("  verifies:  %v\n", s.Verifies)
					io.printf("  capabilities: %v\n", s.Capabilities)
					return nil
				}
			}
			return io.fail("SYMBOL_NOT_FOUND", "symbol not found: "+args[0], nil)
		},
	}
}
