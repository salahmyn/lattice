package cli

// @module-feature cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/flags"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
)

// flagReasons adapts the open-flag map to rtm.Options.FlagsOf.
func flagReasons(open map[string][]flags.Flag) func(string) []string {
	if len(open) == 0 {
		return nil
	}
	return func(unit string) []string {
		fl := open[unit]
		if len(fl) == 0 {
			return nil
		}
		reasons := make([]string, len(fl))
		for i, f := range fl {
			reasons[i] = f.Reason
		}
		return reasons
	}
}

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

  exercised  — demonstrated, and a verifier's own per-test coverage ran an
               enforcer (needs per-test coverage: lattice results ingest --per-test)
  demonstrated — verified, and every verifier passed on the commit
  verified   — enforcer + verifier present, mutation OK
  partial    — enforcer + verifier present, mutation below threshold
  unenforced — invariant exists but no symbol enforces it
  unverified — invariant exists but no test verifies it
  unmapped   — SC has no maps_to_invariant
  phantom    — maps_to_invariant points at a missing invariant

The same rows back the BRD_CRITERION_* validation rules and the
5th Coverage card on the UI, so all three surfaces agree.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			set := results.Load(ws.LatticeDir)
			openFlags := flags.OpenByUnit(ws.LatticeDir)
			matrix := rtm.Build(kg, rtm.Options{
				MutationThreshold: cfg.MutationTesting.Thresholds.Default,
				ResultOf:          resultOfFrom(set),
				FlagsOf:           flagReasons(openFlags),
				ProfileLite:       cfg.IsLite(),
				Coverage:          coverageEvidence(ws, kg),
			})
			coverage := rtm.ComputeCoverage(matrix)
			journey := rtm.ComputeJourneyCoverage(matrix)
			attestation := cfg.Autonomy.AttestationLevel()

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
					"attestation":      attestation,
					"coverage":         coverage,
					"journey_coverage": journey,
					"matrix":           matrix,
				})
			}

			if summaryOnly || len(matrix.Rows) == 0 {
				renderRTMSummary(io, attestation, coverage, journey, matrix)
				if len(matrix.Rows) == 0 && filterStatus == "" && filterBRD == "" {
					io.printf("\n(no BRDs have success_criteria yet — `lattice brd new` or `lattice brd from-code`)\n")
				}
				return nil
			}

			renderRTMSummary(io, attestation, coverage, journey, matrix)
			io.printf("\n")
			io.printf("%-32s %-6s %-2s %-12s %-25s %-8s %-8s\n",
				"BRD", "SC", "T", "status", "invariant", "enforcer", "verifier")
			io.printf("%s\n", strRepeat("-", 96))
			for _, r := range matrix.Rows {
				ref := r.MapsTo
				if ref == "" {
					ref = "(unmapped)"
				}
				if r.DirectWire {
					ref = "(direct-wired)"
				}
				status := string(r.Status)
				if r.Flagged {
					// A flag rides alongside the status — both, always.
					status += "⚑"
				}
				io.printf("%-32s %-6s %-2d %-12s %-25s %-8d %-8d\n",
					truncate(r.BRDID, 32), r.CriterionID, r.Tier, status,
					truncate(ref, 25), len(r.Enforcers), len(r.Verifiers))
				for _, fl := range r.Flags {
					io.printf("    ⚑ %s\n", fl)
				}
				renderRowCoverage(io, r)
			}
			// Scenario rows (v0.8 α) beneath the criteria — what the user does.
			if len(matrix.Scenarios) > 0 {
				io.printf("\n%-32s %-6s %-12s %-8s %-s\n",
					"BRD", "US", "status", "reaches", "verifiers")
				io.printf("%s\n", strRepeat("-", 96))
				for _, s := range matrix.Scenarios {
					reach := "—"
					if s.TouchesEntryPoint {
						reach = "ep"
					}
					io.printf("%-32s %-6s %-12s %-8s %d\n",
						truncate(s.BRDID, 32), s.ScenarioID, string(s.Status), reach, len(s.Verifiers))
					if s.Status == rtm.StatusDemonstrated && s.CoverageReason != "" {
						io.printf("    not exercised: %s\n", s.CoverageReason)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&filterStatus, "status", "",
		"filter rows by status (exercised|demonstrated|verified|partial|failing|unenforced|unverified|unmapped|phantom)")
	cmd.Flags().StringVar(&filterBRD, "brd", "", "filter rows to a single BRD id")
	cmd.Flags().BoolVar(&summaryOnly, "summary", false, "show per-BRD verification ratios only")
	return cmd
}

// renderRTMSummary prints the attestation header, the top-level
// coverage line, and the per-BRD verification ratios. Always emitted;
// the row table is the expandable detail below it. An RTM without an
// attestation header is invalid — claims must never exceed who actually
// ran the checks.
func renderRTMSummary(io *IO, attestation string, c rtm.Coverage, j rtm.JourneyCoverage, m rtm.Matrix) {
	if attestation == "self" {
		io.printf("attestation: self — SELF-ATTESTED RUN, governance simulated\n")
	} else {
		io.printf("attestation: %s\n", attestation)
	}
	io.printf("BRD verification coverage: %.1f%%  (%d/%d criteria verified)\n",
		c.Ratio*100, c.VerifiedCriteria, c.TotalCriteria)
	if ex, dem, ok := exercisedCount(m); ok {
		io.printf("exercised:                 %d/%d demonstrated criteria (a verifier's per-test run executed an enforcer)\n", ex, dem)
	}
	if j.TotalScenarios > 0 {
		io.printf("journey coverage:          %.1f%%  (%d/%d scenarios reach a declared entry point; %d demonstrated)\n",
			j.Ratio*100, j.ReachedScenarios, j.TotalScenarios, j.Demonstrated)
	}
	if len(m.Summaries) == 0 {
		return
	}
	io.printf("\nPer-BRD:\n")
	for _, s := range m.Summaries {
		flagged := ""
		if s.Flagged > 0 {
			flagged = fmt.Sprintf("  ⚑%d", s.Flagged)
		}
		exercised := ""
		if s.Exercised > 0 {
			exercised = fmt.Sprintf(" ex:%d", s.Exercised)
		}
		io.printf("  %-32s  %5.1f%%  dem:%d%s v:%d p:%d fail:%d u-enf:%d u-ver:%d unmap:%d phantom:%d  worst=%s%s\n",
			truncate(s.BRDID, 32), s.VerificationRate*100,
			s.Demonstrated, exercised, s.Verified, s.Partial, s.Failing, s.Unenforced, s.Unverified, s.Unmapped, s.Phantom,
			string(s.WorstStatus), flagged)
	}
}

// exercisedCount counts exercised and demonstrated rows; ok is false when no
// row carries coverage evidence (none was ingested).
func exercisedCount(m rtm.Matrix) (ex, dem int, ok bool) {
	for _, r := range m.Rows {
		if r.Evidence != "" {
			ok = true
		}
		if r.Status.IsDemonstrated() {
			dem++
		}
		if r.Status == rtm.StatusExercised {
			ex++
		}
	}
	return ex, dem, ok
}

// renderRowCoverage prints, under a row with coverage evidence, what ran:
// who exercised it, or why a demonstrated row is not exercised, and how much
// of its enforcers ran.
func renderRowCoverage(io *IO, r rtm.Row) {
	if r.Evidence == "" {
		return
	}
	switch {
	case r.Status == rtm.StatusExercised:
		io.printf("    exercised by %s\n", strings.Join(r.ExercisedBy, ", "))
	case r.Status == rtm.StatusDemonstrated && r.CoverageReason != "":
		io.printf("    not exercised: %s\n", r.CoverageReason)
	}
	if ec := r.EnforcerCoverage; ec != nil {
		io.printf("    enforcer lines: %.1f%% (%d/%d) over %s; evidence %s\n", ec.Percent, ec.Hit, ec.Lines, ec.Over, r.Evidence)
	} else {
		io.printf("    evidence %s\n", r.Evidence)
	}
}
