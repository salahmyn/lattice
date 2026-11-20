package cli

// @module-feature impact

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func newImpactCommand(io *IO) *cobra.Command {
	var since, format string
	var maxSymbols int
	var includeDrafts, tests bool
	cmd := &cobra.Command{
		Use:   "impact --since <ref>",
		Short: "Read-only change-impact report for a pull request",
		Long: `Compares merge-base(<ref>, HEAD) with the working tree (HEAD plus
uncommitted changes) and reports, without writing anything:

  1. changed code      symbols whose own lines a hunk touches, on either side
  2. affected criteria BRD criteria whose invariant has a changed enforcer,
                       approved BRDs first, draft/proposed ones apart
  3. removed enforcement  an enforcer that no longer enforces its invariant:
                       deleted, @enforces tag dropped, file deleted or moved
                       out of the scanned tree
  4. manifest changes  features, BRDs and entry points added, removed,
                       renamed or changed, field by field
  5. code roll-up      the SCIP blast radius of each changed enforcer, rolled
                       up to modules, test files and entry points (optional)

Unlike backprop it records nothing (not even a last-runs entry) and raises
no flag, so it is safe to run on every pull request. --format markdown is the PR-comment
form; it starts with ` + impact.MarkdownMarker + ` so a job can update its
own comment.

--tests prints only the verifier tests of the touched promises (touched by an
enforcer change or a verifier change, a changed verifier file included), and
the tests that exercise a touched line no promise guards, grouped by the
configured break.runners: one line per runner with its ready-to-run command,
then each test file no runner matches. A test of an unguarded line is found by
per-test coverage (coverage), else the static call graph (call_graph), else a
test file beside it with the same base name (heuristic), else two hints: a test
file that changed in the same commits as its file, the change included, within
break.co_change (co_change; by default 2 shared commits among the last 200
within 365 days, skipping a commit of more than 50 files), else a test file
that names the symbol (a compound name of 4+ characters, not a common one) or
a route its file declares (mentions). Each test's "why" says which, with the shared commits or the
mentioning line for a hint; a symbol only a hint places is listed as hinted.
An unguarded symbol none of them finds a test for is listed as uncovered: run
the full suite before merge. With --json it is {"runners": [...],
"unmatched": [...], "hinted": [...], "uncovered": [...]}. It only lists; it
never runs a test. A test no break.runners entry matches is listed as
unmatched with the reason, never dropped.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if io.JSON {
				format = "json"
			}
			switch format {
			case "text", "markdown", "md", "json":
			default:
				return io.fail("BAD_FLAG", fmt.Sprintf("--format %q: want text, markdown or json", format), nil)
			}
			if tests && (format == "markdown" || format == "md") {
				return io.fail("BAD_FLAG", "--tests prints text or json, not markdown", nil)
			}
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			kg, calls, err := readOnlyGraphCalls(cmd, ws)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			adCfg, err := config.LoadAdapters(ws.LatticeDir)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			in := impact.Input{
				Workspace: ws, Registry: all.Registry(adCfg), Graph: kg,
				Since: since, MaxSymbols: maxSymbols, IncludeDrafts: includeDrafts,
			}
			if tests {
				cfg, cerr := config.Load(ws.LatticeDir)
				if cerr != nil {
					return io.fail("BAD_CONFIG", cerr.Error(), nil)
				}
				in.Break = &cfg.Break
				in.Calls = calls
				if ix := coverageIndex(ws, kg); ix != nil {
					in.Coverage = ix
				}
			}
			if corpus, cerr := scip.Load(scipIndexPaths(ws)...); cerr != nil {
				in.CorpusError = cerr.Error()
			} else {
				in.Corpus = corpus
			}
			rep, err := impact.Compute(cmd.Context(), in)
			if err != nil {
				return io.fail("GIT_FAILED", err.Error(), nil)
			}
			if tests {
				if format == "json" {
					return io.printJSON(rep.Tests)
				}
				impact.WriteTests(io.Out, *rep.Tests)
				return nil
			}
			switch format {
			case "json":
				return io.printJSON(rep)
			case "markdown", "md":
				impact.Markdown(io.Out, rep)
			default:
				impact.Text(io.Out, rep)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "git ref to compare with; the base is merge-base(<ref>, HEAD) (required)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text, markdown or json (--json implies json)")
	cmd.Flags().IntVar(&maxSymbols, "max-symbols", impact.DefaultMaxSymbols, "cap on enforcer symbols the code roll-up expands")
	cmd.Flags().BoolVar(&includeDrafts, "include-drafts", true, "list criteria of draft and proposed BRDs in their own section")
	cmd.Flags().BoolVar(&tests, "tests", false, "print only the tests the change touches, grouped by break.runners (never runs them)")
	_ = cmd.MarkFlagRequired("since")
	return cmd
}

// readOnlyGraph builds the working tree's graph the way read commands do,
// but with every write switched off: no LLM entry-point labeling (whose
// labels are saved to lattice/entry-points/), no SCIP indexer run, no
// lattice.json, no ledger entry and no flag.
// @enforces impact:INV-1
func readOnlyGraph(cmd *cobra.Command, ws *workspace.Workspace) (schema.KnowledgeGraph, error) {
	return buildGraphWith(cmd.Context(), ws, buildOptions{readOnly: true})
}

// readOnlyGraphCalls is readOnlyGraph with the static call graph it built,
// or nil when there is none (review mode).
func readOnlyGraphCalls(cmd *cobra.Command, ws *workspace.Workspace) (schema.KnowledgeGraph, impact.CallGraph, error) {
	return graphCalls(cmd.Context(), ws)
}

// graphCalls builds the read-only graph and its call graph, as the test
// listing of impact and check reads them.
func graphCalls(ctx context.Context, ws *workspace.Workspace) (schema.KnowledgeGraph, impact.CallGraph, error) {
	kg, rr, err := buildGraphReach(ctx, ws, buildOptions{readOnly: true})
	if err != nil || rr == nil || rr.Graph == nil {
		return kg, nil, err
	}
	return kg, rr.Graph, nil
}
