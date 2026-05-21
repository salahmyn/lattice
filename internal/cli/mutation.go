package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/mutation"
)

func newMutationCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mutation",
		Short: "Mutation testing of invariant-enforcing code",
	}
	cmd.AddCommand(newMutationRunCommand(io), newMutationScoresCommand(io))
	return cmd
}

func newMutationRunCommand(io *IO) *cobra.Command {
	var feature, scope string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run mutation tests and record per-invariant scores",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			adCfg, _ := config.LoadAdapters(io.Repo)
			cfg, _ := config.Load(io.Repo)
			runner := mutation.NewRunner(io.Repo, all.Registry(adCfg), cfg)

			report := runner.Run(cmd.Context(), kg, mutation.Options{
				Scope:     mutation.Scope(scope),
				FeatureID: feature,
			})
			writeMutationScores(io.Repo, report.PerInvariant)

			if io.JSON {
				return io.printJSON(report)
			}
			io.printf("Mutation testing\n")
			for _, lr := range report.Languages {
				status := "ok"
				if lr.Skipped {
					status = "skipped: " + lr.Error
				} else if !lr.OK {
					status = "error: " + lr.Error
				}
				io.printf("  %-12s %d file(s)  %s\n", lr.Language, len(lr.Files), status)
			}
			if len(report.PerInvariant) > 0 {
				io.printf("\nPer-invariant scores:\n")
				for _, k := range sortedKeys(report.PerInvariant) {
					io.printf("  %-32s %.0f%%\n", k, report.PerInvariant[k])
				}
			}
			for _, m := range report.BelowThreshold {
				io.printf("  BELOW THRESHOLD %s: %.0f%% < %.0f%%\n", m.Invariant, m.Score, m.Threshold)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "limit to one feature id")
	cmd.Flags().StringVar(&scope, "scope", "all", "changed|all")
	return cmd
}

func newMutationScoresCommand(io *IO) *cobra.Command {
	var feature string
	cmd := &cobra.Command{
		Use:   "scores",
		Short: "Show recorded mutation scores",
		RunE: func(_ *cobra.Command, _ []string) error {
			scores := readMutationScores(io.Repo)
			if feature != "" {
				filtered := map[string]float64{}
				for k, v := range scores {
					if len(k) > len(feature) && k[:len(feature)] == feature {
						filtered[k] = v
					}
				}
				scores = filtered
			}
			if io.JSON {
				return io.printJSON(scores)
			}
			if len(scores) == 0 {
				io.printf("no mutation scores recorded\n")
				return nil
			}
			for _, k := range sortedKeys(scores) {
				io.printf("  %-32s %.0f%%\n", k, scores[k])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "limit to one feature id")
	return cmd
}

func mutationScoresPath(repo string) string {
	return filepath.Join(repo, ".lattice", "mutation-scores.json")
}

func writeMutationScores(repo string, scores map[string]float64) {
	if scores == nil {
		scores = map[string]float64{}
	}
	data, _ := json.MarshalIndent(scores, "", "  ")
	_ = os.WriteFile(mutationScoresPath(repo), append(data, '\n'), 0o644)
}

func readMutationScores(repo string) map[string]float64 {
	scores := map[string]float64{}
	data, err := os.ReadFile(mutationScoresPath(repo))
	if err != nil {
		return scores
	}
	_ = json.Unmarshal(data, &scores)
	return scores
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
