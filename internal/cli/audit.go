package cli

// @module-feature audit

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
)

// newAuditCommand is `lattice audit`: one row per invariant, saying whether
// there is evidence the promise is kept or it only looks done.
func newAuditCommand(io *IO) *cobra.Command {
	var markdown bool
	var class, feature string
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Classify every invariant: fake, break_tested, exercised, demonstrated_only, a gap, or unknown",
		Long: `Answers one question about every promise (invariant): is there evidence it
is kept, or does it only look done? It joins what the kernel already has:
the graph, ingested results and per-test coverage, reach, and the break
outcomes in lattice/.cache/results/breaks.json (written by 'lattice break').

Each row carries eight signals (declared, enforced, verified, demonstrated,
exercised, live, break_tested, confirmed), each labelled fact, upper_bound or
proxy, and one class, in this precedence:

  fake               never_runs_enforcer: per-test coverage shows no verifier ran
                     an enforcer; passes_when_broken: a break of the enforcer
                     left the verifiers green
  break_tested       breaking the enforcer made the verifiers fail
  exercised          a verifier passed and its own run executed an enforcer
  demonstrated_only  a verifier passed; nothing shows it runs an enforcer
  unenforced | unverified | not_demonstrated   a gap
  unknown            nothing to judge (kept apart from fake)

Only break_tested and exercised are positive. Unknown is never a pass and
never a fail. 'confirmed' is a fact about the approval record, not proof the
intent is right. lattice/audit-notes.yaml holds optional second-reader notes,
which are shown and never change a class. Nothing here blocks a merge.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			if class != "" && !knownAuditClass(class) {
				return io.fail("AUDIT_BAD_CLASS", "unknown class "+class, map[string]interface{}{"classes": audit.Classes})
			}
			dirty, _ := gitDirty(filepath.Dir(ws.LatticeDir))
			rep := audit.Compute(audit.Collect(ws.LatticeDir, kg, dirty))
			rep = audit.Filter{Class: class, Feature: feature}.Apply(rep)
			switch {
			case io.JSON:
				return io.printJSON(rep)
			case markdown:
				audit.WriteMarkdown(io.Out, rep)
			default:
				audit.WriteText(io.Out, rep)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&markdown, "markdown", false, "print Markdown for a PR comment or a report")
	cmd.Flags().StringVar(&class, "class", "", "show only rows of this class")
	cmd.Flags().StringVar(&feature, "feature", "", "show only rows of this feature")
	return cmd
}

func knownAuditClass(c string) bool {
	for _, k := range audit.Classes {
		if k == c {
			return true
		}
	}
	return false
}
