package cli

// @module-feature audit

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/lines"
)

// newAuditCommand is `lattice audit`: one row per invariant, saying whether
// there is evidence the promise is kept or it only looks done.
func newAuditCommand(io *IO) *cobra.Command {
	var markdown, withLines bool
	var class, feature string
	var promises []string
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
which are shown and never change a class. Nothing here blocks a merge.

Each row lists its enforcers with what breaking each did (killed, survived,
unknown, not_run), so a fake names the enforcer no test notices.

--promise, --feature and --class only narrow what is printed: the audit is
always computed over the whole workspace. An unknown --promise id is a usage
error (exit 2).

--lines adds what each block of code is for, and how far the verifiers of
that intent reach into its lines. A block is a function, method, policy or
trigger. Its intent is the invariant it enforces (@enforces), serves (every
caller in the call graph enforces or serves it), or shares (also reached from
blocks with no intent); else none. Each instrumented line is checked (a
killed break of the intent edited it), run (an unshared invocation of the
intent's verifiers ran it, recorded by 'lattice break --lines'), run_shared
(only an invocation that also ran other tests did), other (the suite, or
other verifiers, ran it), not_run, or unknown (no line data: never counted as
run). Verified lines are (checked + run) over the lines with data, always
printed with the unknown count; with no line data it says so. The JSON output
gains a lines section (lattice.audit-lines/1). --feature, --promise and
--class narrow it too.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			kg, rr, err := buildGraphReach(cmd.Context(), ws, buildOptions{})
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			if class != "" && !knownAuditClass(class) {
				return io.fail("AUDIT_BAD_CLASS", "unknown class "+class, map[string]interface{}{"classes": audit.Classes})
			}
			dirty, _ := gitDirty(filepath.Dir(ws.LatticeDir), ws.LatticeDir)
			rep := computeAudit(ws, kg, rr, dirty)
			ids := splitPromises(promises)
			if unknown := rep.UnknownPromises(ids); len(unknown) > 0 {
				return usageError(io, "AUDIT_UNKNOWN_PROMISE", "unknown promise "+strings.Join(unknown, ", ")+" (want <feature>:INV-n, as listed by lattice audit)")
			}
			rep = audit.Filter{Class: class, Feature: feature, Promises: ids}.Apply(rep)
			var lr *lines.Report
			if withLines {
				cfg, _ := config.Load(ws.LatticeDir)
				full := linesReport(ws, kg, cfg, rr, dirty)
				var kept []string
				for _, row := range rep.Rows {
					kept = append(kept, row.Promise)
				}
				full = lines.Narrow(full, feature, kept, class != "" || len(ids) > 0)
				lr = &full
				rep.Lines = lr
			}
			switch {
			case io.JSON:
				return io.printJSON(rep)
			case markdown:
				audit.WriteMarkdown(io.Out, rep)
				if lr != nil {
					io.printf("\n")
					lines.WriteMarkdown(io.Out, *lr)
				}
			default:
				audit.WriteText(io.Out, rep)
				if lr != nil {
					io.printf("\n")
					lines.WriteText(io.Out, *lr)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&markdown, "markdown", false, "print Markdown for a PR comment or a report")
	cmd.Flags().BoolVar(&withLines, "lines", false, "also show each block's intent and how far its verifiers reach into its lines (needs lattice break --lines)")
	cmd.Flags().StringVar(&class, "class", "", "show only rows of this class")
	cmd.Flags().StringVar(&feature, "feature", "", "show only rows of this feature")
	cmd.Flags().StringSliceVar(&promises, "promise", nil, "show only this promise, as <feature>:INV-n (repeatable, or a comma list)")
	return cmd
}

// splitPromises trims the --promise values and drops empty ones.
func splitPromises(in []string) []string {
	var out []string
	for _, p := range in {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func knownAuditClass(c string) bool {
	for _, k := range audit.Classes {
		if k == c {
			return true
		}
	}
	return false
}
