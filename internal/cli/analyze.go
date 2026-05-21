package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/analyze"
)

func newAnalyzeCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Conflict and impact analysis",
	}
	cmd.AddCommand(newAnalyzeProposalCommand(io), newAnalyzeEvalCommand(io))
	return cmd
}

func newAnalyzeProposalCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "proposal <file>",
		Short: "Analyze a proposal manifest against the corpus",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := analyze.NewAnalyzer(io.Repo).AnalyzeProposal(cmd.Context(), args[0])
			if err != nil {
				return io.fail("ANALYZE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(report)
			}
			renderImpactReport(io, report)
			return nil
		},
	}
}

func newAnalyzeEvalCommand(io *IO) *cobra.Command {
	var baseline string
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the conflict-analysis calibration harness",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if baseline == "" {
				return io.fail("EVAL_NO_BASELINE", "--baseline directory is required", nil)
			}
			result, err := analyze.NewAnalyzer(io.Repo).Eval(cmd.Context(), baseline)
			if err != nil {
				return io.fail("EVAL_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(result)
			}
			io.printf("Calibration: %d fixture(s)\n", len(result.Fixtures))
			for _, f := range result.Fixtures {
				io.printf("  %-24s tp=%d fp=%d fn=%d\n", f.Name, f.TruePos, f.FalsePos, f.FalseNeg)
			}
			io.printf("\nprecision: %.2f   recall: %.2f\n", result.Precision, result.Recall)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseline, "baseline", "", "directory of calibration fixtures")
	return cmd
}

func renderImpactReport(io *IO, r analyze.ImpactReport) {
	io.printf("=== IMPACT ANALYSIS ===\n")
	io.printf("Proposal: %s\n", r.Proposal)
	io.printf("Target:   %s (%s)\n\n", r.Target, r.Mode)

	io.printf("DETERMINISTIC FINDINGS\n")
	for _, f := range r.DeterministicFindings {
		io.printf("  %s %s\n", levelMark(f.Level), f.Message)
	}

	io.printf("\nSEMANTIC FINDINGS (embedding-based; review carefully)\n")
	for _, f := range r.SemanticFindings {
		io.printf("  %s %s\n", levelMark(f.Level), f.Message)
	}

	if r.BlastRadius != nil && r.BlastRadius.Available {
		io.printf("\nCODE BLAST RADIUS (from SCIP)\n")
		io.printf("  Modifies: %v\n", r.BlastRadius.Modifies)
		io.printf("  Adds:     %v\n", r.BlastRadius.Adds)
		io.printf("  Affects:  %d tests, %d external consumers\n",
			r.BlastRadius.AffectedTests, r.BlastRadius.ExternalConsumers)
	}

	if len(r.OpenInvariants) > 0 {
		io.printf("\nOPEN INVARIANT REQUIREMENTS\n")
		for _, oi := range r.OpenInvariants {
			io.printf("  %s needs: %v\n", oi.Invariant, oi.Needs)
		}
	}

	if len(r.ResolutionsRequired) > 0 {
		io.printf("\nRESOLUTIONS REQUIRED BEFORE STATUS=PRODUCTION\n")
		for _, res := range r.ResolutionsRequired {
			io.printf("  - %s\n", res)
		}
	}
}

func levelMark(l analyze.FindingLevel) string {
	switch l {
	case analyze.LevelError:
		return "[X]"
	case analyze.LevelWarning:
		return "[!]"
	default:
		return "[ok]"
	}
}
