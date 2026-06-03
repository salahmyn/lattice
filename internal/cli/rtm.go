package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
)

// newRTMCommand exposes the v0.6 Requirements Traceability Matrix:
// for each BRD success_criterion, walk down to the backing invariant,
// its enforcers and verifiers, and report the verification status.
//
// The CLI mirrors the UI and MCP shapes — same matrix from rtm.Build,
// rendered three different ways. Operators get a quick CI-friendly
// answer to "is what we built what we asked for?" without spinning
// up the server.
func newRTMCommand(io *IO) *cobra.Command {
	var filterStatus, filterBRD string
	var summaryOnly bool
	cmd := &cobra.Command{
		Use:   "rtm",
		Short: "Print the requirements traceability matrix (BRD → SC → invariant → tests)",
		Long: `Walks each BRD's success_criteria via maps_to_invariant to the backing
invariant, its enforcer symbols and verifier tests, and the mutation
score (if available). Reports a per-row status:

  verified   — enforcer + verifier present, mutation OK
  partial    — enforcer + verifier present, mutation below threshold
  unenforced — invariant exists but no symbol enforces it
  unverified — invariant exists but no test verifies it
  unmapped   — SC has no maps_to_invariant
  phantom    — maps_to_invariant points at a missing invariant

The same rows back the BRD_CRITERION_* validation rules and the
5th Coverage card on the UI, so all three surfaces agree.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(io.Repo)
			matrix := rtm.Build(kg, rtm.Options{
				MutationThreshold: cfg.MutationTesting.Thresholds.Default,
			})
			coverage := rtm.ComputeCoverage(matrix)

			// Apply filters in-place — we rebuild slices so JSON output
			// reflects only what the operator asked for.
			rows := matrix.Rows[:0]
			for _, r := range matrix.Rows {
				if filterStatus != "" && string(r.Status) != filterStatus {
					continue
				}
				if filterBRD != "" && r.BRDID != filterBRD {
					continue
				}
				rows = append(rows, r)
			}
			matrix.Rows = rows

			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"coverage":  coverage,
					"matrix":    matrix,
				})
			}

			if summaryOnly || len(matrix.Rows) == 0 {
				renderRTMSummary(io, coverage, matrix)
				if len(matrix.Rows) == 0 && filterStatus == "" && filterBRD == "" {
					io.printf("\n(no BRDs have success_criteria yet — `lattice brd new` or `lattice brd from-code`)\n")
				}
				return nil
			}

			renderRTMSummary(io, coverage, matrix)
			io.printf("\n")
			io.printf("%-32s %-6s %-10s %-25s %-8s %-8s\n",
				"BRD", "SC", "status", "invariant", "enforcer", "verifier")
			io.printf("%s\n", strRepeat("-", 92))
			for _, r := range matrix.Rows {
				ref := r.MapsTo
				if ref == "" {
					ref = "(unmapped)"
				}
				io.printf("%-32s %-6s %-10s %-25s %-8d %-8d\n",
					truncate(r.BRDID, 32), r.CriterionID, string(r.Status),
					truncate(ref, 25), len(r.Enforcers), len(r.Verifiers))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&filterStatus, "status", "",
		"filter rows by status (verified|partial|unenforced|unverified|unmapped|phantom)")
	cmd.Flags().StringVar(&filterBRD, "brd", "", "filter rows to a single BRD id")
	cmd.Flags().BoolVar(&summaryOnly, "summary", false, "show per-BRD verification ratios only")
	return cmd
}

// renderRTMSummary prints the top-level coverage line plus the
// per-BRD verification ratios. Always emitted; the row table is the
// expandable detail below it.
func renderRTMSummary(io *IO, c rtm.Coverage, m rtm.Matrix) {
	io.printf("BRD verification coverage: %.1f%%  (%d/%d criteria verified)\n",
		c.Ratio*100, c.VerifiedCriteria, c.TotalCriteria)
	if len(m.Summaries) == 0 {
		return
	}
	io.printf("\nPer-BRD:\n")
	for _, s := range m.Summaries {
		io.printf("  %-32s  %5.1f%%  v:%d p:%d u-enf:%d u-ver:%d unmap:%d phantom:%d  worst=%s\n",
			truncate(s.BRDID, 32), s.VerificationRate*100,
			s.Verified, s.Partial, s.Unenforced, s.Unverified, s.Unmapped, s.Phantom,
			string(s.WorstStatus))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func strRepeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
