package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/flags"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/revision"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/runsclean"
	"github.com/salahmyn/lattice/pkg/lattice/sweep"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// demonstrateCheck is one row of the sign-off table.
type demonstrateCheck struct {
	Check    string   `json:"check"`
	Pass     bool     `json:"pass"`
	Skipped  bool     `json:"skipped,omitempty"`
	Findings []string `json:"findings,omitempty"`
}

// newDemonstrateCommand composes the QAE demonstration ritual (v0.8.1):
// scope comes from the graph, every check executes now, and the
// sign-off is a ledgered, named act. Checks, in gate order:
//
//	V0  — runs-clean (the app installs, builds, boots, answers)
//	V4  — every verifier passed on ingested results (existence ≠ evidence)
//	V5  — every scenario reaches a declared entry point AND is demonstrated
//	V10 — every tier-2+ criterion carries mutation evidence
//	sweep — no verifier disappeared without a CR retirement item
//
// Open flags are reported alongside the verdict — a demonstrated-but-
// flagged unit is reported as BOTH; the flag routes to a human, the
// sign-off does not clear it.
func newDemonstrateCommand(io *IO) *cobra.Command {
	var brdFilter string
	var skipV0 bool
	cmd := &cobra.Command{
		Use:   "demonstrate",
		Short: "QAE sign-off: V0 + V4 + V5 + V10 + sweep, executed now, ledgered",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			if cfg.IsLite() {
				return io.fail("PROFILE_LITE",
					"the lite profile's ceiling is wired — it cannot sign off a demonstration; adopt the full profile first", nil)
			}

			set := results.Load(ws.LatticeDir)
			openFlags := flags.OpenByUnit(ws.LatticeDir)
			m := rtm.Build(kg, rtm.Options{
				MutationThreshold: cfg.MutationTesting.Thresholds.Default,
				ResultOf:          resultOfFrom(set),
				FlagsOf:           flagReasons(openFlags),
			})

			var checks []demonstrateCheck

			// V0 — gate zero. Nothing else matters while the app won't run.
			switch {
			case skipV0:
				checks = append(checks, demonstrateCheck{Check: "V0 runs-clean", Skipped: true,
					Findings: []string{"skipped by --skip-v0 — the sign-off is weaker for it"}})
			case !cfg.Runtime.Configured():
				checks = append(checks, demonstrateCheck{Check: "V0 runs-clean", Skipped: true,
					Findings: []string{"no runtime: block configured — add one; a green suite atop an app that won't boot is not a demonstration"}})
			default:
				rep := runsclean.Run(cmd.Context(), io.Repo, cfg.Runtime)
				c := demonstrateCheck{Check: "V0 runs-clean", Pass: rep.Pass}
				for _, s := range rep.Steps {
					if !s.OK && !s.Skipped {
						c.Findings = append(c.Findings, s.Step+": "+s.Detail)
					}
				}
				checks = append(checks, c)
				if !rep.Pass {
					// Critical finding: report and stop — per gate zero,
					// running the rest against a dead app proves nothing.
					return finishDemonstrate(io, ws, m, checks, brdFilter)
				}
			}

			// V4 — verifiers passed, executed now (ingested), not merely present.
			v4 := demonstrateCheck{Check: "V4 verifiers passed", Pass: true}
			for _, r := range m.Rows {
				if brdFilter != "" && r.BRDID != brdFilter {
					continue
				}
				switch r.Status {
				case rtm.StatusFailing:
					v4.Pass = false
					v4.Findings = append(v4.Findings, r.BRDID+"/"+r.CriterionID+": a verifier is red")
				case rtm.StatusVerified:
					v4.Pass = false
					v4.Findings = append(v4.Findings, r.BRDID+"/"+r.CriterionID+": verifier exists but no result ingested — run the suite and `lattice results ingest`")
				case rtm.StatusDemonstrated:
				default:
					v4.Pass = false
					v4.Findings = append(v4.Findings, r.BRDID+"/"+r.CriterionID+": "+string(r.Status)+" — "+r.StatusReason)
				}
			}
			checks = append(checks, v4)

			// V5 — journeys: scenario demonstrated THROUGH a declared entry point.
			v5 := demonstrateCheck{Check: "V5 journeys covered", Pass: true}
			for _, s := range m.Scenarios {
				if brdFilter != "" && s.BRDID != brdFilter {
					continue
				}
				unit := s.BRDID + "/" + s.ScenarioID
				if !s.TouchesEntryPoint {
					v5.Pass = false
					v5.Findings = append(v5.Findings, unit+": verified only below its entry point — declare and exercise the real trigger")
				}
				if s.Status != rtm.StatusDemonstrated {
					v5.Pass = false
					v5.Findings = append(v5.Findings, unit+": "+string(s.Status)+" — "+s.StatusReason)
				}
			}
			checks = append(checks, v5)

			// V10 — tier-2+ criteria carry mutation evidence.
			v10 := demonstrateCheck{Check: "V10 mutation evidence (tier 2+)", Pass: true}
			for _, r := range m.Rows {
				if brdFilter != "" && r.BRDID != brdFilter {
					continue
				}
				if r.Tier >= 2 && !r.HasMutation && !r.DirectWire {
					v10.Pass = false
					v10.Findings = append(v10.Findings,
						fmt.Sprintf("%s/%s is tier %d with no mutation evidence — `lattice mutation run`", r.BRDID, r.CriterionID, r.Tier))
				}
			}
			checks = append(checks, v10)

			// Forbidden-move sweep — no silently retired verifiers.
			sw := demonstrateCheck{Check: "sweep (forbidden moves)", Pass: true}
			if baseline, ok := sweep.Load(ws.LatticeDir); ok {
				revs, _ := revision.LoadAll(ws.LatticeDir)
				for _, fqn := range sweep.Disappeared(baseline, sweep.Inventory(kg)) {
					if _, covered := revision.RetirementCovered(revs, fqn); !covered {
						sw.Pass = false
						sw.Findings = append(sw.Findings, fqn+" disappeared without a CR retirement item")
					}
				}
			} else {
				sw.Skipped = true
				sw.Findings = []string{"no baseline yet — run `lattice sweep` once to record it"}
			}
			checks = append(checks, sw)

			return finishDemonstrate(io, ws, m, checks, brdFilter)
		},
	}
	cmd.Flags().StringVar(&brdFilter, "brd", "", "limit the sign-off scope to one BRD")
	cmd.Flags().BoolVar(&skipV0, "skip-v0", false, "skip the runs-clean gate (weakens the sign-off; says so)")
	return cmd
}

// finishDemonstrate renders the verdict, reports open flags alongside
// it, and — only on green — ledgers the sign-off.
func finishDemonstrate(io *IO, ws *workspace.Workspace, m rtm.Matrix, checks []demonstrateCheck, brdFilter string) error {
	green := true
	for _, c := range checks {
		if !c.Pass && !c.Skipped {
			green = false
		}
	}

	var flagged []string
	for _, r := range m.Rows {
		if r.Flagged && (brdFilter == "" || r.BRDID == brdFilter) {
			flagged = append(flagged, r.BRDID+"/"+r.CriterionID+": "+strings.Join(r.Flags, "; "))
		}
	}
	for _, s := range m.Scenarios {
		if s.Flagged && (brdFilter == "" || s.BRDID == brdFilter) {
			flagged = append(flagged, s.BRDID+"/"+s.ScenarioID+": "+strings.Join(s.Flags, "; "))
		}
	}

	if io.JSON {
		_ = io.printJSON(map[string]interface{}{
			"checks": checks, "open_flags": flagged, "signed_off": green,
		})
	} else {
		io.printf("demonstration — %s\n", scopeLabel(brdFilter))
		for _, c := range checks {
			mark := "PASS"
			switch {
			case c.Skipped:
				mark = "skip"
			case !c.Pass:
				mark = "FAIL"
			}
			io.printf("  %-34s %s\n", c.Check, mark)
			for _, f := range c.Findings {
				io.printf("      - %s\n", f)
			}
		}
		for _, f := range flagged {
			io.printf("  ⚑ open flag (routes to a human, not cleared by this sign-off): %s\n", f)
		}
	}

	if !green {
		return io.fail("DEMONSTRATION_INCOMPLETE",
			"sign-off withheld — findings above are the work list, in gate order", nil)
	}

	scope := scopeLabel(brdFilter)
	appendLedgerEvent(io, ws, ledger.EventSignOff, scope,
		fmt.Sprintf("demonstration signed off: V0/V4/V5/V10/sweep green, %d open flag(s) routed to humans", len(flagged)))
	if !io.JSON {
		io.printf("signed off: %s — V0/V4/V5/V10/sweep green\n", scope)
	}
	return nil
}

func scopeLabel(brdFilter string) string {
	if brdFilter == "" {
		return "workspace"
	}
	return brdFilter
}
