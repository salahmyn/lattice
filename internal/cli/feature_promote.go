package cli

// @module-feature cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/flags"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// promoteEntryPoint is one production entry point whose flow names the
// feature, with the path of its handler when the graph knows it.
type promoteEntryPoint struct {
	ID      string `json:"id"`
	Handler string `json:"handler,omitempty"`
	Path    string `json:"path,omitempty"`
}

// promoteCriterion is a criterion level shown as context; it never decides
// whether a feature may be promoted.
type promoteCriterion struct {
	BRD       string `json:"brd"`
	Criterion string `json:"criterion"`
	Level     string `json:"level"`
}

// promoteReport is what `lattice feature promote` states about a feature.
type promoteReport struct {
	Feature     string              `json:"feature"`
	Status      schema.Status       `json:"status"`
	EntryPoints []promoteEntryPoint `json:"entry_points"`
	Criteria    []promoteCriterion  `json:"criteria,omitempty"`
	Note        string              `json:"note"`
	// Promoted is set once the status was written.
	Promoted bool   `json:"promoted,omitempty"`
	By       string `json:"by,omitempty"`
}

// promoteFlagNote is printed with every report: the graph holds no runtime
// state, so a production entry point is a declaration, not proof of rollout.
const promoteFlagNote = "Lattice cannot see runtime flags"

// productionEntryPoints lists the entry points with status production whose
// flow names the feature, sorted by id, each with its handler's path.
func productionEntryPoints(kg schema.KnowledgeGraph, feature string) []promoteEntryPoint {
	fileOf := map[string]string{}
	for _, s := range kg.Symbols {
		fileOf[s.FQN] = s.File
	}
	var out []promoteEntryPoint
	for _, ep := range kg.EntryPoints {
		if ep.Status != schema.StatusProduction {
			continue
		}
		for _, step := range ep.Flow {
			if step.Feature != feature {
				continue
			}
			path := ep.Handler.File
			if path == "" {
				path = fileOf[ep.Handler.Symbol]
			}
			out = append(out, promoteEntryPoint{ID: ep.ID, Handler: ep.Handler.Symbol, Path: path})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// newFeaturePromoteCommand moves a proposal or accepted feature to
// production. Without --by it prints the evidence and writes nothing. With
// --by <email> it writes `status: production` and one ledger entry, and only
// when a production entry point's flow names the feature: the same fact that
// raises STATUS_DRIFT. It is a human act: no mandate covers it.
// @enforces cli:INV-7
func newFeaturePromoteCommand(io *IO) *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Show the evidence that a feature is in production; with --by <email>, set its status",
		Long: `Without --by, prints the production entry points whose flow names the
feature, their handler paths and the levels of the criteria that map to it,
and writes nothing, so an agent may run it.

With --by <email>, sets the feature's status to production and appends a
ledger entry (by, actor, the transition and the entry points printed). It
refuses an agent identity in --by or --actor, a feature whose status is not
proposal or accepted, and a feature no production entry point's flow names.
Only the status key changes; the rest of the manifest stays as written.
No mandate covers it, and it never touches a BRD.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			write := cmd.Flags().Changed("by")
			if write {
				for _, who := range []string{by, io.actor()} {
					if schema.IsAgentActor(who) {
						return io.fail("PROMOTE_HUMAN_ONLY",
							"a feature is promoted by a human — pass --by <email>, not an agent actor ("+who+")", nil)
					}
				}
				if strings.TrimSpace(by) == "" {
					return io.fail("PROMOTE_HUMAN_ONLY", "--by needs the email of the human promoting the feature", nil)
				}
			}
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			var feat *schema.Manifest
			for i := range kg.Features {
				if kg.Features[i].ID == id {
					feat = &kg.Features[i]
				}
			}
			if feat == nil {
				return io.fail("FEATURE_NOT_FOUND", "feature not found: "+id,
					&schema.NextAction{Kind: "create_manifest", Ref: id})
			}
			rep := promoteReport{
				Feature: id, Status: feat.Status,
				EntryPoints: productionEntryPoints(kg, id), Note: promoteFlagNote,
			}
			if rep.EntryPoints == nil {
				rep.EntryPoints = []promoteEntryPoint{}
			}
			rep.Criteria = promoteCriteria(io, ws.LatticeDir, kg, id)

			if write {
				if feat.Status != schema.StatusProposal && feat.Status != schema.StatusAccepted {
					return io.fail("PROMOTE_STATUS",
						fmt.Sprintf("feature %q has status %s; only a proposal or accepted feature is promoted", id, feat.Status), nil)
				}
				if len(rep.EntryPoints) == 0 {
					return io.fail("PROMOTE_NO_EVIDENCE",
						fmt.Sprintf("no production entry point's flow names %q; declare the entry point, or edit the manifest by hand", id),
						&schema.NextAction{Kind: "edit_manifest", Field: "flow"})
				}
				path := joinLattice(ws.LatticeDir, feat.SourcePath)
				body, err := os.ReadFile(path)
				if err != nil {
					return io.fail("PROMOTE_FAILED", err.Error(), nil)
				}
				out, err := setTopLevel(body, "status", string(schema.StatusProduction))
				if err != nil {
					return io.fail("PROMOTE_FAILED", err.Error(), nil)
				}
				if err := os.WriteFile(path, out, 0o644); err != nil {
					return io.fail("PROMOTE_FAILED", err.Error(), nil)
				}
				e := ledgerEntry(io, ledger.EventGate, id, "promoted by "+by+"; production entry points: "+entryPointIDs(rep.EntryPoints))
				if io.actor() == "" {
					e.Actor = by
				}
				e.Transition = string(feat.Status) + "→" + string(schema.StatusProduction)
				if err := ledger.Record(ws.LatticeDir, e); err != nil {
					io.printf("warning: ledger append failed: %v\n", err)
				}
				rep.Promoted, rep.By = true, by
			}

			if io.JSON {
				return io.printJSON(rep)
			}
			renderPromote(io, rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "the human promoting the feature, e.g. sal@example.com; without it nothing is written")
	return cmd
}

func entryPointIDs(eps []promoteEntryPoint) string {
	ids := make([]string, len(eps))
	for i, e := range eps {
		ids[i] = e.ID
	}
	return strings.Join(ids, ", ")
}

// promoteCriteria returns the levels of the criteria whose invariants the
// feature declares, as context for the person deciding.
func promoteCriteria(io *IO, latticeDir string, kg schema.KnowledgeGraph, feature string) []promoteCriterion {
	cfg, _ := config.Load(latticeDir)
	set := results.Load(latticeDir).WithTests(kg.Tests)
	matrix := rtm.Build(kg, rtm.Options{
		MutationThreshold: cfg.MutationTesting.Thresholds.Default,
		ResultOf:          resultOfFrom(set),
		FlagsOf:           flagReasons(flags.OpenByUnit(latticeDir)),
		ProfileLite:       cfg.IsLite(),
	})
	var out []promoteCriterion
	for _, r := range matrix.Rows {
		if r.FeatureID == feature {
			out = append(out, promoteCriterion{BRD: r.BRDID, Criterion: r.CriterionID, Level: string(r.Status)})
		}
	}
	return out
}

func renderPromote(io *IO, r promoteReport) {
	if r.Promoted {
		io.printf("%s: status %s -> production (by %s)\n", r.Feature, r.Status, r.By)
	} else {
		io.printf("%s  [%s]\n", r.Feature, r.Status)
	}
	if len(r.EntryPoints) == 0 {
		io.printf("  no production entry point's flow names this feature\n")
	}
	for _, e := range r.EntryPoints {
		path := e.Path
		if path == "" {
			path = "(path not in the graph)"
		}
		io.printf("  production entry point  %s  handler %s  %s\n", e.ID, e.Handler, path)
	}
	for _, c := range r.Criteria {
		io.printf("  criterion  %s %s  %s\n", c.BRD, c.Criterion, c.Level)
	}
	io.printf("  %s.\n", r.Note)
	if !r.Promoted {
		io.printf("  Nothing was written. A human sets the status with: lattice feature promote %s --by <email>\n", r.Feature)
	}
}
